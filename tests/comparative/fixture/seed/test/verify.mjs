import assert from "node:assert/strict";
import { attachmentKind, chatHeader, closeIdleChat, connectionSummary, retryReachability } from "../src/behavior.mjs";

const id = process.argv[2];
switch (id) {
  case "idle-close":
    assert.deepEqual(closeIdleChat(), { closed: true, confirmation: false });
    break;
  case "svg-text":
    assert.equal(attachmentKind(".svg"), "text");
    break;
  case "retry-reachable":
    assert.deepEqual(retryReachability(true), { reachableEvent: true, probeSucceeded: true });
    break;
  case "html-probe":
    assert.equal(connectionSummary(200, "text/html; charset=utf-8"), "Connection returned a web page, not model API JSON. Add the API path to base_url.");
    break;
  case "folder-header":
    assert.equal(chatHeader("b", "HomePC", "scratch-123"), "b · HomePC");
    break;
  default:
    throw new Error(`unknown verifier ${id}`);
}
