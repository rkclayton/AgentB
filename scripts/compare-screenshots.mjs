import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { chromium } from "@playwright/test";

assert.equal(process.argv.length, 4, "usage: node scripts/compare-screenshots.mjs BASELINE.png CANDIDATE.png");
const images = await Promise.all(process.argv.slice(2).map(async (path) => `data:image/png;base64,${(await readFile(path)).toString("base64")}`));
const browser = await chromium.launch({ channel: "msedge", headless: true });
try {
  const page = await browser.newPage();
  const result = await page.evaluate(async ([baselineURL, candidateURL]) => {
    const load = (src) => new Promise((resolve, reject) => {
      const image = new Image();
      image.onload = () => resolve(image);
      image.onerror = reject;
      image.src = src;
    });
    const [baseline, candidate] = await Promise.all([load(baselineURL), load(candidateURL)]);
    if (baseline.width !== candidate.width || baseline.height !== candidate.height) {
      throw new Error(`image dimensions differ: ${baseline.width}x${baseline.height} vs ${candidate.width}x${candidate.height}`);
    }
    const pixels = (image) => {
      const canvas = document.createElement("canvas");
      canvas.width = image.width;
      canvas.height = image.height;
      const context = canvas.getContext("2d");
      context.drawImage(image, 0, 0);
      return context.getImageData(0, 0, image.width, image.height).data;
    };
    const before = pixels(baseline);
    const after = pixels(candidate);
    let changed = 0;
    let channelDelta = 0;
    let maxChannelDelta = 0;
    let top32Changed = 0;
    let top32MinX = baseline.width;
    let top32MaxX = -1;
    let minX = baseline.width;
    let minY = baseline.height;
    let maxX = -1;
    let maxY = -1;
    for (let offset = 0; offset < before.length; offset += 4) {
      let pixelChanged = false;
      for (let channel = 0; channel < 4; channel++) {
        const delta = Math.abs(before[offset + channel] - after[offset + channel]);
        channelDelta += delta;
        maxChannelDelta = Math.max(maxChannelDelta, delta);
        pixelChanged ||= delta !== 0;
      }
      if (!pixelChanged) continue;
      changed++;
      const pixel = offset / 4;
      const x = pixel % baseline.width;
      const y = Math.floor(pixel / baseline.width);
      if (y < 32) {
        top32Changed++;
        top32MinX = Math.min(top32MinX, x);
        top32MaxX = Math.max(top32MaxX, x);
      }
      minX = Math.min(minX, x);
      minY = Math.min(minY, y);
      maxX = Math.max(maxX, x);
      maxY = Math.max(maxY, y);
    }
    const total = baseline.width * baseline.height;
    return {
      dimensions: `${baseline.width}x${baseline.height}`,
      changed_pixels: changed,
      changed_percent: changed / total * 100,
      mean_abs_channel_delta: channelDelta / (total * 4),
      max_channel_delta: maxChannelDelta,
      top_32_changed_pixels: top32Changed,
      top_32_difference_x: top32Changed ? { min_x: top32MinX, max_x: top32MaxX } : null,
      difference_bounds: changed ? { min_x: minX, min_y: minY, max_x: maxX, max_y: maxY } : null,
    };
  }, images);
  process.stdout.write(`${JSON.stringify(result, null, 2)}\n`);
} finally {
  await browser.close();
}
