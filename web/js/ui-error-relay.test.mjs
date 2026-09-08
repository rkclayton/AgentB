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
	assert.match(relay, /repeat_count/);
	assert.match(relay, /capped/);
});

test("an identical repeating error emits one event and one capped summary", async () => {
	const requests = [];
	const previousWindow = globalThis.window;
	const previousFetch = globalThis.fetch;
	const previousError = console.error;
	globalThis.window = { addEventListener() {} };
	globalThis.fetch = async (_url, options) => { requests.push(JSON.parse(options.body)); return { ok: true }; };
	console.error = () => {};
	try {
		const module = await import(`./ui-error-relay.js?cap=${Date.now()}`);
		module.installUIErrorRelay({ token: () => "token", sessionID: () => "main" });
		for (let index = 0; index < module.UI_ERROR_REPEAT_CAP + 10; index++) console.error("same failure");
		assert.equal(requests.length, 2);
		assert.deepEqual(requests.map((item) => [item.repeat_count, item.capped]), [[1, false], [module.UI_ERROR_REPEAT_CAP, true]]);
	} finally {
		console.error = previousError;
		globalThis.window = previousWindow;
		globalThis.fetch = previousFetch;
	}
});
