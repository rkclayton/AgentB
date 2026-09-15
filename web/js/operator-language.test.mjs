import assert from "node:assert/strict";
import { readdir, readFile } from "node:fs/promises";
import test from "node:test";

const root = new URL("../", import.meta.url);
const standalone = /(^|[^a-z0-9_-])workspace([^a-z0-9_-]|$)/i;
const protocol = new Set(["workspace", "workspace.conflict", "workspace.bound"]);

function literals(source) {
  const found = [];
  for (let at = 0; at < source.length; at++) {
    const quote = source[at];
    if (quote !== '"' && quote !== "'" && quote !== "`") continue;
    let value = "";
    for (at++; at < source.length; at++) {
      if (source[at] === "\\") { at++; continue; }
      if (quote === "`" && source[at] === "$" && source[at + 1] === "{") {
        let depth = 1;
        at += 2;
        while (at < source.length && depth) {
          if (source[at] === "{") depth++;
          else if (source[at] === "}") depth--;
          at++;
        }
        at--;
        continue;
      }
      if (source[at] === quote) break;
      value += source[at];
    }
    found.push(value);
  }
  return found;
}

test("shipped web text and Settings use folder vocabulary", async () => {
  const names = await readdir(root);
  const jsNames = (await readdir(new URL("./", import.meta.url))).filter((name) => name.endsWith(".js") && !name.includes("test"));
  for (const name of jsNames) {
    const source = await readFile(new URL(name, import.meta.url), "utf8");
    for (const value of literals(source)) {
      if (protocol.has(value.trim().toLowerCase())) continue;
      assert.doesNotMatch(value, standalone, `${name}: ${value}`);
    }
  }
  for (const name of names.filter((name) => name.endsWith(".html"))) {
    const source = await readFile(new URL(`../${name}`, import.meta.url), "utf8");
    const text = source.replace(/<script[\s\S]*?<\/script>/gi, "").replace(/<style[\s\S]*?<\/style>/gi, "").replace(/<[^>]+>/g, " ");
    assert.doesNotMatch(text, standalone, name);
  }
});
