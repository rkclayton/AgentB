import { beginNavigation } from "./navigation-telemetry.js";

export function createNavigationGuard(runtime) {
  let claimed = false;
  runtime.onPageShow?.((event) => {
    if (event.persisted) claimed = false;
  });

  return {
    request(details, target) {
      if (!runtime.enabled) {
        runtime.begin(details);
        runtime.assign(target);
        return true;
      }
      if (claimed) {
        runtime.suppress(details);
        return false;
      }
      claimed = true;
      runtime.begin(details);
      try {
        const result = runtime.assign(runtime.decorate(target));
        if (result === false) claimed = false;
        return result !== false;
      } catch (error) {
        claimed = false;
        throw error;
      }
    },
  };
}

function browserRuntime() {
  if (typeof window === "undefined") return null;
  const enabled = new URLSearchParams(location.search).get("navigation_guard") === "1";
  return {
    enabled,
    begin: beginNavigation,
    assign: (target) => location.assign(target),
    decorate(target) {
      const url = new URL(target, location.href);
      url.searchParams.set("navigation_guard", "1");
      return `${url.pathname}${url.search}${url.hash}`;
    },
    onPageShow: (listener) => window.addEventListener("pageshow", listener),
    suppress(details) {
      void fetch("/api/navigation-suppressions", {
        method: "POST",
        headers: { "Content-Type": "application/json", "X-AgentB-Mutation-Token": details.mutationToken || "" },
        body: JSON.stringify({
          suppression_id: globalThis.crypto?.randomUUID?.() || `${Date.now()}-${Math.random().toString(16).slice(2)}`,
          navigation_kind: details.kind,
          from: details.from,
          to: details.to,
          clicked_at: performance.timeOrigin + performance.now(),
          chat_id: details.chatID || "",
        }),
        keepalive: true,
      }).catch(() => {});
    },
  };
}

const runtime = browserRuntime();
const guard = runtime ? createNavigationGuard(runtime) : null;
export const requestNavigation = (details, target) => guard?.request(details, target);
