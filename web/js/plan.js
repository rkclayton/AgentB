import { api, store, subscribe } from "./bus.js";
import { mountChat } from "./chat.js";
import { initShell } from "./shell.js";
import { claimHint, noteReports, planRows, proposalKey, proposalLabel } from "./plan-surface.js";

const shell = initShell({ page: "plan" });
mountChat(shell);
const roots = { empty: byID("plan-empty"), hint: byID("plan-hint"), name: byID("plan-name"), cubes: byID("plan-cubes"), current: byID("plan-current"), items: byID("plan-items"), proposals: byID("plan-proposals"), input: byID("chat-task") };
let loaded = null;
let loading = "";
const resolvedKey = (id) => `agentb.plan.resolved.${id}`;

subscribe(() => { void refresh(); render(); });
void refresh();

async function refresh() {
  const id = store.selection.session_id;
  if (!id || loading === id) return;
  loading = id;
  try { loaded = await api(`/api/plan?session_id=${encodeURIComponent(id)}`, undefined, "GET"); }
  catch (error) { loaded = { error: error.message, plan: "", notes: "" }; }
  finally { loading = ""; render(); }
}

function render() {
  const session = store.sessions[store.selection.session_id];
  roots.empty.hidden = !session || (session.messages || []).length > 0;
  roots.name.textContent = session?.plan_name || "Plan";
  if (!session || !loaded) return;
  if (loaded.error) { roots.current.textContent = loaded.error; roots.items.replaceChildren(); roots.proposals.replaceChildren(); return; }
  const rows = planRows(loaded.plan);
  roots.current.textContent = rows.order || "No current work order.";
  renderItems(rows.items, noteReports(loaded.notes));
  renderProposals(session);
  if (loaded.fallback) showHint("noPlanner");
}

function renderItems(items, reports) {
  roots.cubes.replaceChildren(...items.map((item) => {
    const cube = document.createElement("button");
    cube.type = "button"; cube.className = `plan-cube ${markerClass(item.marker)}`; cube.title = item.text;
    cube.onclick = async () => {
      if (item.marker !== "x" && item.marker !== " ") { document.querySelector(`[data-plan-line="${item.index}"]`)?.scrollIntoView({ block: "center" }); return; }
      await api("/api/plan/marker", { session_id: store.selection.session_id, line: item.line, marker: item.marker === "x" ? " " : "x" });
      await forceRefresh();
    };
    return cube;
  }));
  roots.items.replaceChildren(...items.map((item) => {
    const row = document.createElement("section"); row.className = `plan-item ${markerClass(item.marker)}`; row.dataset.planLine = item.index;
    const line = document.createElement("div"); line.className = "plan-item-line";
    const marker = document.createElement("span"); marker.className = "plan-marker"; marker.textContent = `[${item.marker}]`;
    const text = document.createElement("span"); text.textContent = item.text; line.append(marker, text); row.append(line);
    for (const report of reports.filter((candidate) => headingNamesItem(candidate.heading, item.text))) {
      const details = document.createElement("details"); details.className = "plan-report";
      const summary = document.createElement("summary"); summary.textContent = report.heading;
      const pre = document.createElement("pre"); pre.textContent = report.body; details.append(summary, pre); row.append(details);
    }
    return row;
  }));
}

function renderProposals(session) {
  const resolved = readSet(resolvedKey(session.id));
  const proposals = (session.messages || []).flatMap((message) => message.plan_proposals || []).filter((proposal) => !resolved.has(proposalKey(proposal)));
  if (!proposals.length) { const empty=document.createElement("p"); empty.className="plan-empty-tray"; empty.textContent="No proposed edits."; roots.proposals.replaceChildren(empty); return; }
  roots.proposals.replaceChildren(...proposals.map((proposal) => {
    const row=document.createElement("article"); row.className="plan-proposal"; row.tabIndex=0;
    const label=document.createElement("span"); label.textContent=proposalLabel(proposal);
    const path=document.createElement("code"); path.textContent=proposal.old_text;
    const actions=document.createElement("div"); actions.className="plan-proposal-actions";
    const accept=button("Accept"), dismiss=button("Dismiss"); actions.append(accept,dismiss); row.append(label,path,actions);
    row.onclick=(event)=>{ if(event.target.closest("button")) return; quote(proposal); };
    row.onkeydown=(event)=>{ if(event.key==="Enter") quote(proposal); };
    accept.onclick=async()=>{ await api("/api/plan/accept",{session_id:session.id,proposal}); remember(resolvedKey(session.id),proposalKey(proposal)); showHint("accepted"); await forceRefresh(); };
    dismiss.onclick=()=>{ remember(resolvedKey(session.id),proposalKey(proposal)); renderProposals(session); };
    return row;
  }));
}

function quote(proposal) { roots.input.value = `${roots.input.value}${roots.input.value ? "\n" : ""}> ${proposalLabel(proposal)}\n> ${proposal.new_text}`; roots.input.focus(); }
function showHint(id) { const text=claimHint(localStorage,id); if(!text)return; roots.hint.textContent=text; const clear=()=>{roots.hint.textContent=""; document.removeEventListener("click",clear,true);}; setTimeout(()=>document.addEventListener("click",clear,true),0); }
async function forceRefresh(){ loaded=null; await refresh(); }
function markerClass(marker){ return ({x:"x","~":"half","!":"blocked","-":"dropped"," ":"open"})[marker] || "open"; }
function headingNamesItem(heading,text){ const ids=[...text.matchAll(/\b(?:item\s+)?([0-9]+[a-z]*)\b/gi)].map((match)=>match[1].toLowerCase()); return ids.some((id)=>new RegExp(`(^|[^a-z0-9])${id}([^a-z0-9]|$)`,`i`).test(heading)); }
function readSet(key){ try{return new Set(JSON.parse(localStorage.getItem(key)||"[]"));}catch{return new Set();} }
function remember(key,id){const values=readSet(key);values.add(id);localStorage.setItem(key,JSON.stringify([...values]));}
function button(text){const value=document.createElement("button");value.type="button";value.textContent=text;return value;}
function byID(id){return document.getElementById(id);}
