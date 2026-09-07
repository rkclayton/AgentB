import assert from "node:assert/strict";
import test from "node:test";

class FakeElement {
  constructor(tag = "div") { this.tagName = tag; this.children = []; this.attributes = new Map(); this.className = ""; this.textContent = ""; this.hidden = false; this.scrollTop = 0; this.scrollHeight = 0; this.clientHeight = 0; this.style = {}; }
  append(...values) { this.children.push(...values); }
  replaceChildren(...values) { this.children = [...values]; }
  setAttribute(name, value) { this.attributes.set(name, String(value)); }
  getAttribute(name) { return this.attributes.get(name); }
  set innerHTML(value) { const count = [...String(value).matchAll(/<span/g)].length; this.children = Array.from({ length: count }, () => new FakeElement("span")); }
  get classList() { return {
    add: (...names) => { this.className += ` ${names.join(" ")}`; },
    contains: (name) => this.className.split(/\s+/).includes(name),
    toggle: (name, force) => {
      const names = new Set(this.className.split(/\s+/).filter(Boolean));
      if (force ?? !names.has(name)) names.add(name); else names.delete(name);
      this.className = [...names].join(" ");
    },
  }; }
}
const elements = new Map();
globalThis.document = { hidden: false, addEventListener: () => {}, getElementById: (id) => { if (!elements.has(id)) elements.set(id, new FakeElement()); return elements.get(id); }, createElement: (tag) => new FakeElement(tag) };
globalThis.window = { addEventListener: () => {} };
globalThis.EventSource = class { addEventListener() {} };
globalThis.requestAnimationFrame = (fn) => fn();
const { reduce } = await import("./bus.js");
const { renderFlow } = await import("./flow.js");
const { renderRack } = await import("./rack.js");
const { renderRail } = await import("./rail.js");
const { renderTimeline } = await import("./timeline.js");
const { renderState } = await import("./state.js");

reduce({ type: "snapshot", data: { replay: false, servers: [], config: {}, flow: { stages: ["assemble", "call_model"], edges: [] }, sessions: { main: {
  schema_version: 1, cursor: { generation: "main.jsonl", offset: 100 }, complete: true, id: "main", label: "main", server_id: "homepc",
  run: { status: "idle", turn: 0, max_turns: 40, last_stop_reason: "" }, activity: { stage: "wait_user", completed_stages: ["assemble"] },
  tools: [{ name: "read_file", calls: 2, enabled: true }], messages: [], timeline: [], chat: [],
  budget: { n_ctx: 32768, ceiling: 24576, reserve: 8192, used_est: 1511, categories: { system: 290, tools: 1221 }, estimated: false },
  model_turns: 3, compaction_count: 1, compaction_token_delta: -500, compaction_model_calls: 1, compaction_prompt_tokens: 400, compaction_completion_tokens: 100,
} } } });

test("Activity renders only the canned snapshot projection", () => { renderFlow(); assert.equal(elements.get("flow").children.length, 2); assert.equal(elements.get("flow-count").textContent, "idle"); });
test("Tools renders authoritative projected counts", () => { renderRack(); assert.equal(elements.get("tool-count").textContent, "2 calls"); assert.equal(elements.get("rack").children[0].children[1].textContent, "read_file"); });
test("Context renders projected accounting", () => { renderRail(); assert.match(elements.get("rail").children[0].getAttribute("aria-label"), /system prompt 290/); });
test("History renders projected durable history", () => { renderTimeline(); assert.match(elements.get("timeline-count").textContent, /3 turns.*1 compact/); assert.equal(elements.get("timeline-list").children[0].textContent, "—"); });
test("State renders projected messages", () => { renderState(); assert.equal(elements.get("state-count").textContent, "0"); assert.equal(elements.get("state-list").children[0].textContent, "—"); });
