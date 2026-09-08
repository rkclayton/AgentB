let installed = false;

export function installUIErrorRelay(context = {}) {
	if (installed || typeof window === "undefined") return;
	installed = true;
	const original = console.error.bind(console);
	const relay = (kind, message, stack = "") => {
		try {
			const token = context.token?.() || "";
			fetch("/api/ui-errors", {
				method: "POST",
				headers: { "Content-Type": "application/json", "X-AgentB-Mutation-Token": token },
				body: JSON.stringify({ session_id: context.sessionID?.() || "", kind, message: String(message || ""), stack: String(stack || "") }),
				keepalive: true,
			}).catch(() => {});
		} catch {}
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
