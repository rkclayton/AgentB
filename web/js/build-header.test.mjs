import assert from "node:assert/strict";
import test from "node:test";

import { projectBuildHeader } from "./build-header.js";

test("Header projects release tag, seven-character commit, and valid signature icon", () => {
  const state = projectBuildHeader(
    { tag: "v0.7.0", commit: "0123456789abcdef", known: true, dirty: false },
    { supported: true, files: [{ status: "Valid" }, { status: "Valid" }] },
  );
  assert.equal(state.text, "v0.7.0 · 0123456");
  assert.equal(state.signatureClass, "valid");
  assert.equal(state.signatureGlyph, "✓");
});

test("Header reports dirty and invalid states honestly", () => {
  const state = projectBuildHeader(
    { tag: "v0.7.0", commit: "0123456789abcdef", known: true, dirty: true },
    { supported: true, files: [{ status: "NotSigned" }] },
  );
  assert.equal(state.text, "v0.7.0 · 0123456+dirty");
  assert.equal(state.signatureClass, "invalid");
});
