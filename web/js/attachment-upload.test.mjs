import test from "node:test";
import assert from "node:assert/strict";

import { attachmentChipFile, attachmentMetadata, exchangeFiles, exchangeUpload, uploadAttachment } from "./attachment-upload.js";
import { createFileChip, probeFile } from "./deliverables.js";

test("attachment upload sends multipart session and file under mutation guard", async () => {
  const file = new Blob(["hello"], { type: "text/plain" });
  file.name = "note.txt";
  let request;
  const result = await uploadAttachment(file, "main", {
    token: "mutation",
    fetchImpl: async (url, options) => {
      request = { url, options };
      return { ok: true, json: async () => ({ path: "attachments/note.txt", bytes: 5, sha256: "abc" }) };
    },
  });
  assert.equal(request.url, "/api/attachments");
  assert.equal(request.options.headers["X-AgentB-Mutation-Token"], "mutation");
  assert.equal(request.options.body.get("session_id"), "main");
  assert.equal(request.options.body.get("file").name, "note.txt");
  assert.deepEqual(attachmentMetadata(result), { path: "attachments/note.txt", bytes: 5, sha256: "abc" });
});

test("operator attachments selection reads server-projected bytes then feeds attachment ingest", async () => {
  const calls = [];
  const fetchImpl = async (url, options = {}) => {
    calls.push({ url, options });
    if (url === "/api/operator-attachments") return { ok: true, json: async () => ({ files: [{ path: "ready.txt", bytes: 5, sha256: "one" }] }) };
    if (String(url).startsWith("/api/operator-attachments?")) return { ok: true, blob: async () => new Blob(["ready"]) };
    return { ok: true, json: async () => ({ path: "attachments/ready.txt", bytes: 5, sha256: "two" }) };
  };
  assert.equal((await exchangeFiles({ fetchImpl }))[0].path, "ready.txt");
  const uploaded = await exchangeUpload({ path: "ready.txt" }, "main", {
    fetchImpl,
    token: "mutation",
    makeFile: (parts, name, init) => { const value = new Blob(parts, init); value.name = name; return value; },
  });
  assert.equal(uploaded.path, "attachments/ready.txt");
  assert.equal(calls[1].options.method, undefined);
  assert.equal(calls[2].options.method, "POST");
});

test("attachment replay chip reconstructs and marks a missing workspace file", async () => {
  const source = { path: "attachments/gone.txt", bytes: 12, sha256: "deadbeef" };
  const file = attachmentChipFile(source);
  assert.equal(file.callID, "attachment:deadbeef");
  const state = await probeFile("/api/files/attachments/gone.txt?session=main", async () => ({ ok: false, headers: new Map() }));
  const node = createFileChip({
    createElement() { return { className: "", textContent: "", disabled: false, children: [], append(...children) { this.children.push(...children); } }; },
  }, file, state, { downloadURL: "/ignored", openFolder() {} });
  assert.equal(node.children[1].textContent, "missing");
  assert.equal(node.children.length, 3);
  assert.equal(node.children[2].disabled, true);
});
