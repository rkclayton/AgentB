export function renderMarkdown(root, text) {
  const document = root.ownerDocument || globalThis.document;
  root.replaceChildren();
  const lines = String(text).split("\n");
  let index = 0;
  while (index < lines.length) {
    if (lines[index].trimStart().startsWith("```")) {
      const parsed = fencedBlock(document, lines, index, lines[index].search(/\S/));
      root.append(parsed.node);
      index = parsed.next;
    } else if (listLine(lines[index])) {
      const ordered = /^\d+\./.test(listLine(lines[index]).marker);
      const list = document.createElement(ordered ? "ol" : "ul");
      while (index < lines.length) {
        const match = listLine(lines[index]);
        if (!match || /^\d+\./.test(match.marker) !== ordered) break;
        const item = document.createElement("li");
        inline(document, item, match.text);
        index++;
        while (index < lines.length && lines[index].trimStart().startsWith("```") && lines[index].search(/\S/) > match.indent) {
          const parsed = fencedBlock(document, lines, index, lines[index].search(/\S/));
          item.append(parsed.node);
          index = parsed.next;
        }
        list.append(item);
      }
      root.append(list);
    } else if (lines[index].trim()) {
      const paragraph = [];
      while (index < lines.length && lines[index].trim() && !lines[index].trimStart().startsWith("```") && !listLine(lines[index])) paragraph.push(lines[index++]);
      const p = document.createElement("p");
      inline(document, p, paragraph.join("\n"));
      root.append(p);
    } else index++;
  }
}

function listLine(line) {
  const match = /^(\s*)([-*]|\d+\.)\s+(.*)$/.exec(line);
  return match ? { indent: match[1].length, marker: match[2], text: match[3] } : null;
}

function fencedBlock(document, lines, start, indent) {
  const code = [];
  let index = start + 1;
  while (index < lines.length && !lines[index].trimStart().startsWith("```")) {
    const line = lines[index++];
    code.push(line.slice(Math.min(indent, line.length)));
  }
  if (index < lines.length) index++;
  const wrapper = document.createElement("div");
  wrapper.className = "code-block";
  const button = document.createElement("button");
  button.type = "button";
  button.className = "code-copy";
  button.textContent = "📎";
  button.ariaLabel = "Copy code";
  button.title = "Copy code";
  const value = code.join("\n");
  button.onclick = () => navigator.clipboard?.writeText(value);
  const pre = document.createElement("pre");
  pre.textContent = value;
  wrapper.append(button, pre);
  return { node: wrapper, next: index };
}

function inline(document, root, text) {
  const pattern = /(`[^`]+`|\*\*[^*]+\*\*)/g;
  let at = 0;
  for (const match of text.matchAll(pattern)) {
    root.append(document.createTextNode(text.slice(at, match.index)));
    const value = match[0];
    const node = document.createElement(value.startsWith("`") ? "code" : "strong");
    node.textContent = value.startsWith("`") ? value.slice(1, -1) : value.slice(2, -2);
    root.append(node);
    at = match.index + value.length;
  }
  root.append(document.createTextNode(text.slice(at)));
}
