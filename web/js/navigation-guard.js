import { beginNavigation } from "./navigation-telemetry.js";

export function createNavigationGuard(runtime) {
  let claimed = false;
  runtime.onPageShow?.((event) => {
    if (event.persisted) claimed = false;
  });

  return {
    request(details, target) {
      if (claimed) {
        runtime.suppress(details);
        return false;
      }
      claimed = true;
      const navigationID = runtime.begin(details);
      const destination = runtime.decorate(target, navigationID);
      try {
        const result = runtime.assign(destination);
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
  return {
    begin: beginNavigation,
    assign: (target) => location.assign(target),
    decorate(target, navigationID) {
      const url = new URL(target, location.href);
      if (navigationID) url.searchParams.set("navigation_id", navigationID);
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
