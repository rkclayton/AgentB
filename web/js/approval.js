export function shellGrantApproval(data = {}) {
  const boundary = typeof data.boundary_escape === "boolean"
    ? data.boundary_escape
    : data.name?.endsWith(".operator_override");
  return (!boundary && data.name === "shell")
    || data.name === "shell.operator_override"
    || data.name === "shell.operator_command";
}

export function approvalChoices(data = {}) {
	return [["session", "Yes, for this chat"], ["once", "Just once"], ["deny", "No"]];
}

export function approvalText(data = {}) {
	const boundary = typeof data.boundary_escape === "boolean"
		? data.boundary_escape
		: data.name?.endsWith(".operator_override");
	const value = data.args?.path ?? data.args?.command ?? data.args?.pattern ?? "";
	if (boundary) return {
		title: "Run as you",
		request: `${data.name || "This operation"} needs your Windows identity.`,
		reason: data.args?.reason || "The restricted account cannot complete it.",
		detail: value,
	};
	return {
		title: "Allow this",
		request: `${data.name || "This operation"} is restricted by your approval rules.`,
		reason: "It will run only after your decision.",
		detail: value,
	};
}

export function createApprovalCard(document, entry = {}, options = {}) {
	const event = entry.event || entry;
	const data = event.data || {};
	const wording = approvalText(data);
	const content = document.createElement("section");
	content.className = "approval-card chat-approval";
	if (data.boundary_escape) content.classList.add("alarm");
	const title = document.createElement("span");
	title.textContent = wording.title;
	const request = document.createElement("strong");
	request.textContent = wording.request;
	const reason = document.createElement("span");
	reason.textContent = wording.reason;
	content.append(title, request, reason);
	const technical = document.createElement("details");
	const summary = document.createElement("summary");
	summary.textContent = "Technical detail";
	const pre = document.createElement("pre");
	pre.textContent = wording.detail;
	technical.append(summary, pre);
	content.append(technical);
	if (entry.decision) {
		const decided = document.createElement("span");
		decided.textContent = entry.decision;
		content.append(decided);
	} else if (!options.replay && options.decide) {
		const actions = document.createElement("div");
		actions.className = "approval-actions";
		for (const [decision, label] of approvalChoices(data)) {
			const button = document.createElement("button");
			button.type = "button";
			button.textContent = label;
			if (decision === "session") {
				button.className = "default";
				button.autofocus = true;
			}
			button.onclick = () => options.decide(data.call_id, decision);
			actions.append(button);
		}
		content.append(actions);
	}
	return content;
}
