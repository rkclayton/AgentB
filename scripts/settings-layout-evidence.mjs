import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { mkdir, writeFile } from "node:fs/promises";
import { resolve } from "node:path";
import { chromium } from "playwright";

const argv = process.argv.slice(2);
const args = Object.fromEntries(Array.from({ length: Math.floor(argv.length / 2) }, (_, index) => [argv[index * 2].replace(/^--/, ""), argv[index * 2 + 1]]));
for (const name of ["exe", "app-root", "data", "config", "base", "expected-commit", "evidence"]) assert.ok(args[name], `missing --${name}`);
const sleep = (ms) => new Promise((done) => setTimeout(done, ms));
const app = spawn(resolve(args.exe), ["-config", resolve(args.config), "-app-root", resolve(args["app-root"]), "-data-root", resolve(args.data)], { windowsHide: true, stdio: ["ignore", "ignore", "pipe"] });
let stderr = "";
app.stderr.on("data", (chunk) => { stderr += String(chunk); });
let browser;
try {
  let state;
  const deadline = Date.now() + 15000;
  while (!state && Date.now() < deadline) {
    try { const response = await fetch(`${args.base}/api/state`); if (response.ok) state = await response.json(); } catch {}
    if (!state) await sleep(50);
  }
  assert.ok(state, `candidate startup timed out: ${stderr}`);
  assert.equal(state.build.commit, args["expected-commit"]);
  assert.equal(state.build.dirty, false);
  browser = await chromium.launch({ channel: "msedge", headless: true });
  const page = await browser.newPage({ viewport: { width: 1250, height: 975 } });
  await page.goto(`${args.base}/`);
  await page.locator(".shell-settings").click();
  const rows = page.locator(".profile-list > .profile-row");
  const rowsBefore = await rows.count();
  await page.locator('[data-action="profile-toggle"]').first().click();
  const wide = await page.evaluate(() => {
    const editor = document.querySelector(".profile-fields");
    const group = document.querySelector(".settings-group");
    const content = document.querySelector(".settings-content");
    const list = document.querySelector(".profile-list");
    const paths = [...editor.querySelectorAll("[data-path]")].map((node) => node.dataset.path);
    return {
      profile_fields_height: editor.getBoundingClientRect().height,
      settings_group_height: group.scrollHeight,
      list_children: list.children.length,
      editor_after_list: list.nextElementSibling?.classList.contains("profile-editor") === true,
      path_count: paths.length,
      unique_path_count: new Set(paths).size,
      sampling_labels: [...editor.querySelectorAll(".sampling-label")].map((node) => node.textContent),
      horizontal_overflow: content.scrollWidth - content.clientWidth,
    };
  });
  const remove = page.locator('[data-action="remove-server"]').first();
  await remove.click();
  const removeFirstClick = { text: await remove.textContent(), class: await remove.getAttribute("class"), rows_before: rowsBefore, rows_after: await rows.count() };
  await page.setViewportSize({ width: 700, height: 800 });
  const narrow = await page.evaluate(() => {
    const content = document.querySelector(".settings-content");
    const sheet = document.querySelector(".settings-page");
    return { content_overflow: content.scrollWidth - content.clientWidth, page_overflow: sheet.scrollWidth - sheet.clientWidth, profile_fields_height: document.querySelector(".profile-fields").getBoundingClientRect().height };
  });
  const output = { schema: 1, measured_at: new Date().toISOString(), build: state.build, wide, remove_first_click: removeFirstClick, narrow };
  assert.equal(wide.editor_after_list, true);
  assert.ok(wide.path_count >= 28);
  assert.equal(wide.unique_path_count, 27);
  assert.deepEqual(wide.sampling_labels, ["temperature", "top_p", "top_k", "min_p", "presence penalty", "repeat penalty"]);
  assert.equal(wide.horizontal_overflow, 0);
  assert.equal(narrow.content_overflow, 0);
  assert.equal(narrow.page_overflow, 0);
  assert.equal(removeFirstClick.rows_after, removeFirstClick.rows_before);
  assert.match(removeFirstClick.text, /Confirm/);
  await mkdir(resolve(args.evidence), { recursive: true });
  await writeFile(resolve(args.evidence, "layout.json"), JSON.stringify(output, null, 2));
  process.stdout.write(`${JSON.stringify(output, null, 2)}\n`);
} finally {
  try { await browser?.close(); } catch {}
  try { app.kill(); } catch {}
}
