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
  const collapse = document.createElement("button");
  collapse.type = "button";
  collapse.className = "collapse-arrow";
  collapse.textContent = "↑";
  collapse.setAttribute("aria-label", "Collapse thought");
  collapse.onclick = () => { expanded.delete(key); rerender(); };
  root.append(button, collapse, body);
  return { root, button, caret, active, dots, summary, body, collapse };
}

function updateView(view, entry, tokens, options) {
  const open = options.expanded.has(entry.key);
  setAttribute(view.button, "class", `thinking-line ${entry.done ? "thought-line" : "thinking-active"}`);
  setAttribute(view.button, "aria-expanded", String(open));
  setText(view.caret, open ? "▾" : "▸");
  view.active.hidden = entry.done;
  view.summary.hidden = !entry.done;
  if (entry.done) {
    const duration = options.formatDuration(entry.thinkingMS);
    setText(view.summary, `Thought ${entry.thinkingEstimated && duration ? "~" : ""}${duration || "—"} seconds (${entry.reasoningTokensEstimated || entry.thinkingEstimated ? "~" : ""}${options.format(tokens)} tokens)`);
  }
  view.body.hidden = !open;
  view.collapse.hidden = !open;
  const body = entry.reasoning || (entry.done
    ? "Reasoning text is unavailable in this recording."
    : "Waiting for reasoning text…");
  if (view.body.textContent !== body) view.body.textContent = body;
}

function setText(node, value) {
  if (node.textContent !== value) node.textContent = value;
}

function setAttribute(node, name, value) {
  if (node.getAttribute(name) !== value) node.setAttribute(name, value);
}
