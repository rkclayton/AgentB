import assert from "node:assert/strict";
import { EVAL_SYSTEM_PROMPT } from "./eval-system-prompt.mjs";

const guidance = "On this Windows evaluation host, invoke Go verifier commands with C:\\Go\\bin\\go.exe; go is not on PATH.";
assert.equal(EVAL_SYSTEM_PROMPT.split(guidance).length, 2, "eval prompt must contain one stable Go guidance sentence");
assert.equal(EVAL_SYSTEM_PROMPT.endsWith(guidance), true);
process.stdout.write("PASS stable eval system prompt\n");
