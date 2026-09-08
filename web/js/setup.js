import { recommendationForBytes, recommendationTableVersion } from "./local-recommendations.js";

const root = document.getElementById("setup");
const connection = document.getElementById("connection");
let snapshot = null;
let choice = "";
let step = "choose";
let detection = null;
let busy = false;
let message = "";
let alarm = false;
let apiProfileID = "";
let localProfileID = "";

void load();
root.addEventListener("click", click);

async function load() {
  try {
    snapshot = await request("/api/state", undefined, "GET");
    render();
  } catch (error) {
    connection.textContent = error.message;
    connection.className = "alarm";
  }
}

function render() {
  if (!snapshot) return;
  root.innerHTML = step === "choose" ? chooseStep()
    : step === "api" ? connectionStep("api")
      : step === "local" ? connectionStep("local")
        : doneStep();
}

function chooseStep() {
  return `<section class="setup-section">
    <p class="setup-kicker">First connection</p>
    <h1>How will Agent_b reach its models?</h1>
    <div class="setup-cards">
      ${setupCard("api", "API only", "Use a model service you already have.")}
      ${setupCard("hybrid", "API + local assistant", "Use your API for main work and a small local Agent C for support.")}
      ${setupCard("local", "Full local", "Keep the main model on this computer.")}
    </div>
    <div class="setup-actions"><a class="setup-button quiet" href="/chat?setup=skip">Skip for now</a></div>
  </section>`;
}

function setupCard(id, title, sentence) {
  return `<button class="setup-card" type="button" data-action="choose" data-id="${id}"><strong>${title}</strong><span>${sentence}</span></button>`;
}

function connectionStep(kind) {
  const local = kind === "local";
  const id = local ? localProfileID : apiProfileID;
  const profile = snapshot.servers?.find((item) => item.id === id) || {};
  const caps = profile.capabilities || {};
  const title = local ? (choice === "hybrid" ? "Connect Agent C" : "Connect the local model") : "Connect the API";
  const role = local && choice === "hybrid" ? "Agent C uses this profile." : "Agent B uses this profile.";
  return `<section class="setup-section">
    <p class="setup-kicker">${local ? "Local detection" : "API connection"}</p>
    <h1>${title}</h1><p>${role}</p>
    ${local ? localDetection() : ""}
    <div class="setup-fields">
      <label>URL<input data-field="url" value="${attr(profile.base_url || detectedURL())}" placeholder="https://example.test/v1"></label>
      <label>Model<input data-field="model" value="${attr(profile.model || suggestedModel())}" placeholder="model name"></label>
      <label>Saved credential name<input data-field="credential" value="${attr(profile.credential || "")}" placeholder="optional"></label>
      <label>API key<input data-field="api-key" type="password" autocomplete="off" placeholder="optional"></label>
    </div>
    <p class="setup-note">If you enter a key, it is protected for your Windows account and the settings file keeps only its saved name.</p>
    <div class="setup-actions">
      <button type="button" data-action="test" data-kind="${kind}" ${busy ? "disabled" : ""}>${busy ? "Testing…" : "Save and Test"}</button>
      <button type="button" class="quiet" data-action="skip-step">Skip this step</button>
      <button type="button" class="quiet" data-action="back">Back</button>
    </div>
    ${feedback()}
    ${caps.probed_at || probeFailed(caps) ? capabilities(caps, profile) : ""}
  </section>`;
}

function localDetection() {
  if (!detection) return `<div class="setup-detection"><span>Checking this computer…</span></div>`;
  const recommendation = recommendationForBytes(detection.recommendation_memory_bytes);
  const gpus = (detection.gpus || []).map((gpu) => `<li>${html(gpu.vendor)} ${html(gpu.name)} · ${memoryLabel(gpu.vram_bytes, gpu.unified_memory_bytes)}</li>`).join("");
  const running = (detection.servers || []).filter((item) => item.running);
  const servers = running.length
    ? running.map((item) => `<li>${html(item.name)} is running</li>`).join("")
    : (detection.servers || []).map((item) => `<li><a href="${attr(item.install_url)}" target="_blank" rel="noreferrer">Install ${html(item.name)}</a></li>`).join("");
  const interpreters = (detection.interpreters || []).map((item) => `<li><code>${html(item.name)}</code> · <span title="${attr(item.path)}">${html(item.path)}</span> · ${html(item.availability)}</li>`).join("") || "<li>None found</li>";
  return `<div class="setup-detection">
    <div><strong>Hardware</strong><ul>${gpus || "<li>No GPU reported</li>"}</ul></div>
    <div><strong>Recommendation</strong><p><code>${html(recommendation.model)}</code> · ${html(recommendation.package_size)} · ${html(recommendation.use)}</p><span class="setup-note">Table ${recommendationTableVersion}; leave room for the context and other applications.</span></div>
    <div><strong>Local servers</strong><ul>${servers}</ul></div>
    <div><strong>Interpreters</strong><ul>${interpreters}</ul><span class="setup-note">Install an interpreter for all users in Program Files to let the service run it without an operator decision.</span></div>
  </div>`;
}

function capabilities(caps, profile) {
  const rows = [
    ["Token counting", caps.tokenize], ["Streaming replies", caps.streaming], ["Tool use", caps.tool_calls],
    ["PDF and document input", caps.document_input], ["Image input", caps.image_input],
  ];
  return `<div class="setup-capabilities"><h2>What the Test found</h2>
    ${rows.map(([label, value]) => `<div><span>${label}</span><strong>${value ? "Available" : "Not available"}</strong></div>`).join("")}
    <div><span>Context size</span><strong>${format(profile.context?.n_ctx || caps.n_ctx)} tokens</strong></div>
    <div class="setup-actions"><button type="button" data-action="continue">Continue</button><button class="quiet" type="button" data-action="skip-step">Skip</button></div>
  </div>`;
}

function doneStep() {
  return `<section class="setup-section"><p class="setup-kicker">Ready</p><h1>Setup is complete</h1>
    <p>You can return here from Connections in Settings.</p>${feedback()}
    <div class="setup-actions"><button type="button" data-action="finish">Open Chat</button><a class="setup-button quiet" href="/chat?setup=skip">Skip for now</a></div>
  </section>`;
}

async function click(event) {
  const button = event.target.closest("[data-action]");
  if (!button || busy) return;
  const action = button.dataset.action;
  if (action === "choose") {
    choice = button.dataset.id;
    step = choice === "local" ? "local" : "api";
    if (step === "local") void runDetection();
    return render();
  }
  if (action === "back") { step = "choose"; message = ""; return render(); }
  if (action === "test") return testConnection(button.dataset.kind);
  if (action === "continue" || action === "skip-step") {
    step = step === "api" && choice === "hybrid" ? "local" : "done";
    message = "";
    if (step === "local") void runDetection();
    return render();
  }
  if (action === "finish") return finish();
}

async function testConnection(kind) {
  const local = kind === "local";
  const url = root.querySelector('[data-field="url"]').value.trim();
  const model = root.querySelector('[data-field="model"]').value.trim();
  const credential = root.querySelector('[data-field="credential"]').value.trim();
  const apiKey = root.querySelector('[data-field="api-key"]').value;
  if (!url || !model) { message = "URL and model are required before Test."; alarm = true; return render(); }
  busy = true; message = "Saving the connection…"; alarm = false; render();
  try {
    let id = local ? localProfileID : apiProfileID;
    if (!id) id = uniqueProfileID(local ? "setup-local" : "setup-api");
    if (local) localProfileID = id; else apiProfileID = id;
    const profile = { id, label: local && choice === "hybrid" ? "Agent C" : local ? "Local" : "API", base_url: url, model };
    if (credential) profile.credential = credential;
    if (apiKey) profile.api_key = apiKey;
    const servers = [...(snapshot.config.servers || []).filter((item) => item.id !== id), profile];
    const current = snapshot.config.agents?.[0] || {};
    const agents = [{
      name: current.name || profile.label,
      b: local && choice !== "hybrid" ? id : current.b || id,
      ...(local && choice === "hybrid" ? { c: id } : current.c ? { c: current.c } : {}),
      ...(current.d ? { d: current.d } : {}),
      toolset: current.toolset || ["read_file", "list_dir", "write_file", "edit_file", "search_text", "shell", "remember", "recall", "fetch_url", "find_files", "run_script", "call_service"],
      ...(current.prompt_addendum ? { prompt_addendum: current.prompt_addendum } : {}),
    }];
    snapshot.config = await request("/api/config", { servers, agents });
    snapshot.servers = snapshot.config.servers || [];
    message = "Running the existing connection Test…";
    render();
    await request(`/api/servers/${encodeURIComponent(id)}/probe`, {});
    await waitForProbe(id);
    message = probeFailed(snapshot.servers.find((item) => item.id === id)?.capabilities || {}) ? "Test finished with a problem." : "Test complete.";
    alarm = probeFailed(snapshot.servers.find((item) => item.id === id)?.capabilities || {});
  } catch (error) { message = error.message; alarm = true; }
  finally { busy = false; render(); }
}

async function waitForProbe(id) {
  const deadline = Date.now() + 15 * 60 * 1000;
  while (Date.now() < deadline) {
    await delay(750);
    snapshot = await request("/api/state", undefined, "GET");
    const caps = snapshot.servers?.find((item) => item.id === id)?.capabilities || {};
    if (caps.probed_at || probeFailed(caps)) return;
  }
  throw new Error("Test did not finish before the connection timeout.");
}

async function runDetection() {
  try { detection = await request("/api/local-detection", undefined, "GET"); }
  catch (error) { message = error.message; alarm = true; }
  render();
}

async function finish() {
  try {
    snapshot = await request("/api/state", undefined, "GET");
    const agent = snapshot.config.agents?.[0];
    if (!Object.keys(snapshot.sessions || {}).length && agent) {
      await request("/api/sessions", { label: "main", agent_id: agent.name.toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-|-$/g, ""), workspace: snapshot.config.workspace });
    }
    location.href = agent ? "/chat" : "/chat?setup=skip";
  } catch (error) { message = error.message; alarm = true; render(); }
}

function uniqueProfileID(base) {
  let id = base, suffix = 2;
  while (snapshot.servers?.some((profile) => profile.id === id)) id = `${base}-${suffix++}`;
  return id;
}

function detectedURL() { return detection?.servers?.find((item) => item.running)?.default_url || "http://127.0.0.1:8080"; }
function suggestedModel() { return detection ? recommendationForBytes(detection.recommendation_memory_bytes).model : ""; }
function probeFailed(caps) { return (caps.findings || []).some((item) => String(item).startsWith("probe failed:")); }
function feedback() { return message ? `<p class="setup-feedback ${alarm ? "alarm" : ""}" role="status">${html(message)}</p>` : ""; }
function memoryLabel(vram, unified) { return unified ? `${formatBytes(unified)} unified memory` : `${formatBytes(vram)} VRAM`; }
function formatBytes(bytes) { return `${(Number(bytes || 0) / (1024 ** 3)).toFixed(1)} GiB`; }
function format(value) { return Number(value || 0).toLocaleString("en-US"); }
function delay(ms) { return new Promise((resolve) => setTimeout(resolve, ms)); }
function html(value) { return String(value ?? "").replace(/[&<>"']/g, (character) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[character]); }
function attr(value) { return html(value); }

async function request(path, body, method = "POST") {
  const options = { method, headers: {} };
  if (method !== "GET" && method !== "HEAD") options.headers["X-AgentB-Mutation-Token"] = snapshot?.mutation_token || "";
  if (body !== undefined) { options.headers["Content-Type"] = "application/json"; options.body = JSON.stringify(body); }
  const response = await fetch(path, options);
  const value = await response.json().catch(() => ({}));
  if (!response.ok) throw new Error(value.error || `HTTP ${response.status}`);
  return value;
}
