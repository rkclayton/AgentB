import assert from "node:assert/strict";
import test from "node:test";

import { attachmentReadability } from "./attachment-readability.js";

const session = { server_id: "text-only" };
const profiles = [{ id: "text-only", capabilities: { probed_at: "2026-09-12T00:00:00Z", image_input: false, document_input: false } }];

test("marks an image unreadable for the active probed profile", () => {
  assert.equal(attachmentReadability(session, profiles, { kind: "image" }), "This profile cannot read images · probe found no image input");
});

test("keeps readable kinds and extracted PDFs unmarked", () => {
  assert.equal(attachmentReadability(session, profiles, { kind: "text" }), null);
  assert.equal(attachmentReadability(session, profiles, { kind: "pdf", sidecar: "attachments/report.pdf.txt" }), null);
});

test("keeps OCR sidecars and native overrides readable", () => {
  assert.equal(attachmentReadability(session, profiles, { kind: "image", sidecar: "attachments/screen.png.txt" }), null);
  const native = [{ ...profiles[0], attachment_handling: "native" }];
  assert.equal(attachmentReadability(session, native, { kind: "image" }), null);
});

test("marks an extract override pending until its sidecar exists", () => {
  const extract = [{ ...profiles[0], attachment_handling: "extract", capabilities: { ...profiles[0].capabilities, image_input: true } }];
  assert.equal(attachmentReadability(session, extract, { kind: "image" }), "This image needs OCR before the profile can read it");
});

test("does not invent a capability verdict before probing", () => {
  assert.equal(attachmentReadability(session, [{ id: "text-only", capabilities: { image_input: false } }], { kind: "image" }), null);
});

test("uses the currently selected session profile", () => {
  const capable = [...profiles, { id: "vision", capabilities: { probed_at: "2026-09-12T00:00:00Z", image_input: true, document_input: true } }];
  assert.equal(attachmentReadability({ server_id: "vision" }, capable, { kind: "image" }), null);
});
