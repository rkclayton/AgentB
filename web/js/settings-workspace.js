let store, armed, workspaceState, operatorFileState, row, number, toggle, currentValue, html, attr;
function useSettingsContext(context) {
  ({ store, armed, workspaceState, operatorFileState, row, number, toggle, currentValue, html, attr } = context);
}

function folders() {
	const directories = workspaceState.length ? workspaceState.map((item) => {
		const policyKey=`policy:${item.dir}`; const policy=item.policy;
		return `<div class="session-row workspace-row"><span class="path" title="${attr(item.dir)}">${html(item.dir)}</span><span>${item.memory_count} memory ${item.memory_count===1?"entry":"entries"}</span><span>${html(relativeDate(item.last_used))}</span></div>
		${policy ? `<div class="session-row workspace-policy-row"><span class="path" title="${attr(policy.path)}">${html(policy.path)}</span><code title="${attr(policy.hash)}">${html((policy.hash||"").slice(0,12))}</code><span>${html(policy.approved_at||"not approved")}</span><button type="button" class="${armed.has(policyKey)?"confirm":""}" data-action="revoke-workspace-policy" data-id="${attr(item.dir)}" ${policy.approved?"":"disabled"}>${armed.has(policyKey)?"Confirm revoke":"Revoke"}</button></div>`:""}`;
	}).join("") : '<p class="settings-note">No known folders.</p>';
	return `${operatorFilesFolder()}<div class="settings-subhead">Known folders</div>${directories}`;
}

function operatorFilesFolder() {
	const bytes=Number(operatorFileState.attachment_bytes||0).toLocaleString("en-US");
	const files=Number(operatorFileState.attachment_files||0);
	const emptyKey="operator-attachments:empty";
	const dir=store.sessions[store.active]?.workspace||store.config.workspace||"";
	const found=operatorFileState.instruction_found||[];
	const adoptable=!found.includes("AGENT_B.md")&&found.some((name)=>name==="AGENTS.md"||name==="CLAUDE.md");
	const adopt=adoptable?`<div class="settings-subhead">Adopt repository instructions</div>
		<p class="settings-note">Create AGENT_B.md from ${html(found.join(" + "))}; source files remain in place.</p>
		<label class="settings-check warning"><input id="adopt-instruction-cleanup" type="checkbox"> Also remove AGENTS.md / CLAUDE.md</label>
		<p class="settings-note">Cleanup is destructive and is off by default.</p>
		<button type="button" data-action="adopt-instructions" data-id="${attr(dir)}">Adopt</button>`:"";
	return `${row("attachments",`<span class="path" title="${attr(operatorFileState.attachments_path||"")}">${files} files · ${bytes} bytes</span><button type="button" class="${armed.has(emptyKey)?"confirm":""}" data-action="empty-operator-attachments" ${files?"":"disabled"}>${armed.has(emptyKey)?"Confirm empty":"Empty"}</button>`)}
		${toggle("operator_files.allow_mailbox_approvals","Allow approvals from the mailbox",store.config.operator_files?.allow_mailbox_approvals===true)}
		<p class="settings-note">whoever can write to your synced folder can then grant the agent your identity.</p>
		${number("operator_files.log_retention_days","log retention (days)",store.config.operator_files?.log_retention_days||30)}
		${adopt}<div class="settings-subhead">Known directories</div>`;
}

function relativeDate(value) { if(!value)return "never"; const date=new Date(value); return Number.isNaN(date.valueOf())?value:date.toLocaleString(); }


export function renderWorkspacePage(context) {
  useSettingsContext(context);
  return folders();
}
