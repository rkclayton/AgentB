let installed = false;
export const UI_ERROR_DEDUPE_MS = 1000;
export const UI_ERROR_REPEAT_CAP = 25;

export function installUIErrorRelay(context = {}) {
	if (installed || typeof window === "undefined") return;
	installed = true;
	const original = console.error.bind(console);
	const errors = new Map();
	const post = (kind, message, stack, repeatCount, capped) => {
		try {
			const token = context.token?.() || "";
			fetch("/api/ui-errors", {
				method: "POST",
				headers: { "Content-Type": "application/json", "X-AgentB-Mutation-Token": token },
				body: JSON.stringify({ session_id: context.sessionID?.() || "", kind, message, stack, repeat_count: repeatCount, capped }),
				keepalive: true,
			}).catch(() => {});
		} catch {}
	};
	const relay = (kind, value, stackValue = "") => {
		const message = String(value || "");
		const stack = String(stackValue || "");
		const fingerprint = `${kind}\n${message}\n${stack}`;
		let state = errors.get(fingerprint);
		if (!state) {
			state = { total: 1, reported: 1, timer: 0, capped: false };
			errors.set(fingerprint, state);
			post(kind, message, stack, 1, false);
			return;
		}
		if (state.capped) return;
		state.total++;
		if (state.total >= UI_ERROR_REPEAT_CAP) {
			if (state.timer) clearTimeout(state.timer);
			state.timer = 0;
			state.capped = true;
			post(kind, message, stack, state.total, true);
			return;
		}
		if (state.timer) clearTimeout(state.timer);
		state.timer = setTimeout(() => {
			state.timer = 0;
			if (state.capped || state.reported === state.total) return;
			state.reported = state.total;
			post(kind, message, stack, state.total, false);
		}, UI_ERROR_DEDUPE_MS);
	};
	console.error = (...values) => {
		original(...values);
		relay("console.error", values.map(stringify).join(" "));
	};
	window.addEventListener("error", (event) => relay("unhandled exception", event.message, event.error?.stack || ""));
	window.addEventListener("unhandledrejection", (event) => relay("unhandled rejection", stringify(event.reason), event.reason?.stack || ""));
}

function stringify(value) {
	if (value instanceof Error) return value.stack || value.message;
	if (typeof value === "string") return value;
	try { return JSON.stringify(value); } catch { return String(value); }
}
