import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";

const relay = await readFile(new URL("./ui-error-relay.js", import.meta.url), "utf8");
const shell = await readFile(new URL("./shell.js", import.meta.url), "utf8");

test("UI errors and unhandled exceptions relay to the session-scoped harness tape", () => {
	assert.match(shell, /installUIErrorRelay\(\{ token: \(\) => store\.mutation_token, sessionID: \(\) => store\.active \}\)/);
	assert.match(relay, /console\.error =/);
	assert.match(relay, /addEventListener\("error"/);
	assert.match(relay, /addEventListener\("unhandledrejection"/);
	assert.match(relay, /\/api\/ui-errors/);
	assert.match(relay, /X-AgentB-Mutation-Token/);
});
