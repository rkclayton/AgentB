export function modelTurnKey(event) {
  const data = event?.data || {};
  const turn = data.turn ?? data.message?.turn;
  return event?.run_id && turn !== undefined ? `${event.run_id}:${turn}` : "";
}

export function trimTimeline(timeline, activeTurnKey, limit = 500) {
	// Completed raw streams are represented by model.response and the stored
	// assistant message. Keep only the active turn's deltas so token traffic cannot
	// evict operational history such as tool results and compactions.
	timeline = timeline.filter((event) =>
		(event.type !== "model.delta" && event.type !== "model.progress") ||
		(activeTurnKey && modelTurnKey(event) === activeTurnKey),
	);
  if (timeline.length <= limit) return timeline;
  let start = timeline.length - limit;
  if (activeTurnKey) {
    const request = timeline.findIndex(
      (event) => event.type === "model.request" && modelTurnKey(event) === activeTurnKey,
    );
    if (request >= 0 && request < start) start = request;
  }
  return start > 0 ? timeline.slice(start) : timeline;
}

export function hydrateAgentEntries(entries, messages) {
  const byContent = new Map();
  const byCall = new Map();
  for (const entry of entries) {
    if (entry.type !== "agent" || !entry.done) continue;
    if (entry.text) {
      const matches = byContent.get(entry.text) || [];
      matches.push(entry);
      byContent.set(entry.text, matches);
    }
    for (const id of entry.toolCallIDs || []) byCall.set(id, entry);
  }

  const matched = new Set();
  const used = new Set();
  for (let index = messages.length - 1; index >= 0; index--) {
    const message = messages[index];
    if (message.role !== "assistant") continue;
    let entry = null;
    for (const call of message.tool_calls || []) {
      const candidate = byCall.get(call.id);
      if (candidate && !used.has(candidate)) {
        entry = candidate;
        break;
      }
    }
    if (!entry && message.content) {
      const candidates = byContent.get(message.content) || [];
      while (candidates.length && used.has(candidates[candidates.length - 1])) candidates.pop();
      entry = candidates.pop() || null;
    }
    if (!entry) continue;
    entry.text = message.content || entry.text;
    if (message.reasoning) entry.reasoning = message.reasoning;
    used.add(entry);
    matched.add(message);
  }
  return matched;
}

export function createThinkingRenderer(options) {
  const views = new Map();
  let used = new Set();

  function begin() {
    used = new Set();
  }

  function render(entry, tokens) {
    let view = views.get(entry.key);
    if (!view) {
      view = createView(options.document, entry.key, options.expanded, options.rerender);
      views.set(entry.key, view);
    }
    used.add(entry.key);
    updateView(view, entry, tokens, options);
    return view.root;
  }

  function end() {
    for (const key of views.keys()) if (!used.has(key)) views.delete(key);
  }

  return { begin, render, end };
}

function createView(document, key, expanded, rerender) {
  const root = document.createElement("div");
  const button = document.createElement("button");
  button.type = "button";
  const caret = document.createElement("span");
  caret.className = "disclosure-caret";
  const active = document.createElement("span");
  active.textContent = "Thinking";
  const dots = document.createElement("span");
  dots.className = "thinking-dots";
  dots.textContent = "...";
  active.append(dots);
  const summary = document.createElement("em");
  button.append(caret, active, summary);
  button.onclick = () => {
    expanded.has(key) ? expanded.delete(key) : expanded.add(key);
    rerender();
  };
  const body = document.createElement("pre");
  body.className = "thinking-body";
  root.append(button, body);
  return { root, button, caret, active, dots, summary, body };
}

function updateView(view, entry, tokens, options) {
  const open = options.expanded.has(entry.key);
  view.button.className = `thinking-line ${entry.done ? "thought-line" : "thinking-active"}`;
  view.button.setAttribute("aria-expanded", String(open));
  view.caret.textContent = open ? "▾" : "▸";
  view.active.hidden = entry.done;
  view.summary.hidden = !entry.done;
  if (entry.done) {
    const duration = options.formatDuration(entry.thinkingMS);
    view.summary.textContent = `Thought ${entry.thinkingEstimated && duration ? "~" : ""}${duration || "—"} seconds (${entry.reasoningTokensEstimated || entry.thinkingEstimated ? "~" : ""}${options.format(tokens)} tokens)`;
  }
  view.body.hidden = !open;
  view.body.textContent = entry.reasoning || (entry.done
    ? "Reasoning text is unavailable in this recording."
    : "Waiting for reasoning text…");
}
