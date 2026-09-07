export function shellGrantApproval(data = {}) {
  const boundary = typeof data.boundary_escape === "boolean"
    ? data.boundary_escape
    : data.name?.endsWith(".operator_override");
  return (!boundary && data.name === "shell")
    || data.name === "shell.operator_override"
    || data.name === "shell.operator_command";
}

export function approvalChoices(data = {}) {
	if (shellGrantApproval(data)) {
		return [["once", "Once"], ["run", "For this run"], ["session", "For this chat"], ["operator_mode", "Operator mode"], ["deny", "Keep denied"]];
	}
	if (data.boundary_escape) return [["once", "Once"], ["run", "For this run"], ["session", "For this chat"], ["deny", "Keep denied"]];
	return [["approve", "Approve"], ["deny", "Deny"]];
}

export function approvalText(data = {}) {
	const boundary = typeof data.boundary_escape === "boolean"
		? data.boundary_escape
		: data.name?.endsWith(".operator_override");
	const value = data.args?.path ?? data.args?.command ?? data.args?.pattern ?? "";
	if (boundary) return `Privilege escalation: ${data.args?.reason || "service identity could not run operation"}. ${value}`.trim();
	if (data.name === "shell") return `Run shell command? ${value}`.trim();
	return `Policy confirmation: ${data.name || "tool"} ${value}`.trim();
}

export function createApprovalCard(document, entry = {}, options = {}) {
	const event = entry.event || entry;
	const data = event.data || {};
	const content = document.createElement("div");
	content.className = "chat-content chat-approval";
	if (data.boundary_escape) content.classList.add("alarm");
	content.append(document.createTextNode(approvalText(data)));
	if (entry.decision) {
		content.append(document.createTextNode(` ${entry.decision}`));
	} else if (!options.replay && options.decide) {
		for (const [decision, label] of approvalChoices(data)) {
			const button = document.createElement("button");
			button.type = "button";
			button.textContent = label;
			button.onclick = () => options.decide(data.call_id, decision);
			content.append(button);
		}
	}
	return content;
}
