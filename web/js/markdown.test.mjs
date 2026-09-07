import assert from "node:assert/strict";
import test from "node:test";

import { renderMarkdown } from "./markdown.js";

class FakeNode {
  constructor(ownerDocument, tagName = "") {
    this.ownerDocument = ownerDocument;
    this.tagName = tagName;
    this.children = [];
    this._text = "";
  }

  append(...nodes) {
    this.children.push(...nodes);
  }

  replaceChildren(...nodes) {
    this.children = [...nodes];
    this._text = "";
  }

  set textContent(value) {
    this.children = [];
    this._text = String(value);
  }

  get textContent() {
    return this._text + this.children.map((child) => child.textContent).join("");
  }
}

class FakeDocument {
  createElement(tagName) {
    return new FakeNode(this, tagName);
  }

  createTextNode(text) {
    const node = new FakeNode(this);
    node.textContent = text;
    return node;
  }
}

test("successive cumulative stream renders replace rather than append", () => {
  const document = new FakeDocument();
  const root = new FakeNode(document, "div");

  renderMarkdown(root, "Hey! Not");
  renderMarkdown(root, "Hey! Not much — just");
  renderMarkdown(root, "Hey! Not much — just here in the console.");

  assert.equal(root.children.length, 1);
  assert.equal(root.textContent, "Hey! Not much — just here in the console.");
});

test("numbered lists retain fenced blocks and copy code without markdown chrome", async () => {
  const document = new FakeDocument();
  const root = new FakeNode(document, "div");
  let copied = "";
  Object.defineProperty(globalThis, "navigator", { configurable: true, value: { clipboard: { writeText: async (value) => { copied = value; } } } });

  renderMarkdown(root, "1. First step\n   ```powershell\n   Get-Process\n   ```\n2. Second `step`");

  assert.equal(root.children[0].tagName, "ol");
  assert.equal(root.children[0].children.length, 2);
  const block = root.children[0].children[0].children[1];
  assert.equal(block.className, "code-block");
  assert.equal(block.children[0].textContent, "📎");
  assert.equal(block.children[0].ariaLabel, "Copy code");
  assert.equal(block.children[1].textContent, "Get-Process");
  await block.children[0].onclick();
  assert.equal(copied, "Get-Process");
});

test("prose-only messages have no copy control", () => {
  const document = new FakeDocument();
  const root = new FakeNode(document, "div");
  renderMarkdown(root, "Plain prose with `inline code`.");
  assert.equal(root.children.length, 1);
  assert.equal(root.children[0].tagName, "p");
});
