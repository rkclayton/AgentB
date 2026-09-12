import assert from "node:assert/strict";
import test from "node:test";

import { createFileChip, filesFromResponse, probeFile } from "./deliverables.js";

class FakeNode {
  constructor(tagName) {
    this.tagName = tagName;
    this.children = [];
    this.className = "";
    this.disabled = false;
    this.textContent = "";
  }
  append(...nodes) { this.children.push(...nodes); }
}

const document = { createElement: (tag) => new FakeNode(tag) };

test("file chips derive only successful writes from a canned projected response", () => {
  const items = [
    { type: "agent", run_id: "r7", toolCallIDs: ["write", "read", "script", "edit"] },
    { type: "tool", callID: "write", name: "write_file", args: { path: "draft.txt" }, result: { ok: true, file: { path: "reports/final.txt", bytes: 1536 } } },
    { type: "tool", callID: "read", name: "read_file", args: { path: "input.txt" }, result: { ok: true } },
    { type: "tool", callID: "script", name: "run_script", args: { language: "python" }, result: { ok: true } },
    { type: "tool", callID: "edit", name: "edit_file", args: { path: "legacy.md" }, result: { ok: true } },
  ];
  const files = filesFromResponse(items);
  assert.deepEqual(files, [
    { path: "reports/final.txt", bytes: 1536, callID: "write", runID: "r7", openScope: "workspace", openPath: "reports/final.txt" },
    { path: "legacy.md", bytes: null, callID: "edit", runID: "r7", openScope: "workspace", openPath: "legacy.md" },
  ]);
  const chip = createFileChip(document, files[0], { state: "ready", bytes: 1536 }, { openFolder() {} });
  assert.equal(chip.children[0].textContent, "final.txt");
  assert.equal(chip.children[1].textContent, "1.5 KiB");
  assert.equal(chip.children[2].tagName, "a");
  assert.equal(chip.children[2].textContent, "folder");
  assert.equal(chip.children.some((child) => child.textContent === "download" || child.textContent === "open folder"), false);
});

test("both mode points open-folder at the durable exchange copy", () => {
  const items = [
    { type: "agent", run_id: "r8", toolCallIDs: ["write"] },
    { type: "tool", callID: "write", name: "write_file", args: { path: "reports/final.txt" }, result: { ok: true, file: { path: "reports/final.txt", bytes: 8 } } },
    { type: "notice", event: { type: "files.delivered", data: { mode: "both", items: [{ source_path: "reports/final.txt", exchange_path: "final (2).txt", status: "copied" }] } } },
  ];
  const [file] = filesFromResponse(items);
  assert.equal(file.openScope, "exchange");
  assert.equal(file.openPath, "final (2).txt");
});

test("a replay file absent from the workspace renders missing", async () => {
  const status = await probeFile("/api/files/gone.txt?session=main", async () => ({ ok: false, headers: new Map() }));
  const chip = createFileChip(document, { path: "gone.txt", bytes: 12 }, status, { openFolder() {} });
  assert.deepEqual(status, { state: "missing", bytes: null });
  assert.equal(chip.className, "file-chip missing");
  assert.equal(chip.children[1].textContent, "missing");
  assert.equal(chip.children.some((child) => child.textContent === "download"), false);
  assert.equal(chip.children.some((child) => child.textContent === "folder"), false);
});

test("a malformed write entry cannot abort deliverable discovery for its neighbors", () => {
  const malformed = new Proxy({ type: "tool", name: "write_file", result: { ok: true } }, {
    get(target, key) {
      if (key === "args") throw new Error("malformed projected args");
      return target[key];
    },
  });
  assert.deepEqual(filesFromResponse([
    malformed,
    { type: "tool", callID: "kept", name: "edit_file", args: { path: "kept.txt" }, result: { ok: true } },
  ]), [
    { path: "kept.txt", bytes: null, callID: "kept", runID: "", openScope: "workspace", openPath: "kept.txt" },
  ]);
});
