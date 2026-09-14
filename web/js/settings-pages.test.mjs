import assert from "node:assert/strict";
import test from "node:test";

import { renderAboutPage } from "./settings-about.js";
import { renderConnectionsPage } from "./settings-connections.js";
import { renderContextPage } from "./settings-context.js";
import { renderDeliveryPage } from "./settings-delivery.js";
import { renderGeneralPage } from "./settings-general.js";
import { renderRunPage } from "./settings-run.js";
import { renderSecurityPage } from "./settings-security.js";
import { renderWorkspacePage } from "./settings-workspace.js";

const blank = () => "";

function pageContext() {
  return {
    store: {
      active: "", sessions: {}, servers: [], build: { tag: "v0.49.0", commit: "abcdef0" },
      config: { workspace: "C:\\workspace", context: { soft_pct: 0.75, summary_pct: 0.85, accounting: "auto" }, chat: {}, run: {}, approval: {}, deliver: {}, memory: {}, tools: {}, shell: { service_account: {} }, signing: {}, sandbox: {} },
      shell_credential: {}, shell_identity: {}, sandbox: {}, serving_facts: {},
    },
    expanded: new Set(), armed: new Set(), drafts: new Map(), errors: new Map(), probeMessages: new Map(),
    workspaceState: [], operatorFileState: { attachment_files: 0, attachment_bytes: 0, instruction_found: [] },
    shellCredentialMessage: "", shellCredentialAlarm: false,
    serviceAccountStatus: { loaded: false, supported: true, exists: false, administrator: false },
    serviceAccountBusy: false, serviceAccountMessage: "", serviceAccountAlarm: false,
    hardeningStatus: { loaded: false, supported: true, applied: false },
    hardeningBusy: false, hardeningMessage: "", hardeningAlarm: false,
    signingStatus: { loaded: false, supported: true, configured: false, can_manage: false, files: [] },
    signingBusy: false, signingMessage: "", signingAlarm: false,
    serverProfiles: () => [], row: blank, field: blank, text: blank, number: blank, numberControl: blank,
    textarea: blank, secret: blank, toggle: blank, choices: blank, approvalChoices: blank, copyRow: blank,
    currentValue: (_path, fallback) => fallback, issue: blank, profileReason: blank,
    html: String, attr: String, selectedHardeningServerID: blank,
    operatorStatusView: () => ({ active: false, label: "off", src: "", srcset: "" }),
  };
}

test("every Settings page renderer accepts the controller context", () => {
  const context = pageContext();
  const pages = [
    renderConnectionsPage(context), renderContextPage(null, context), renderRunPage(context),
    renderDeliveryPage(context), renderAboutPage(context), renderWorkspacePage(context),
    renderSecurityPage("shell", null, context), renderGeneralPage("sessions", null, context),
    renderGeneralPage("tools", null, context), renderGeneralPage("memory", null, context),
  ];
  assert.equal(pages.length, 10);
  for (const page of pages) assert.equal(typeof page, "string");
});

test("Security renders the LAN switch and detected confirmation list", () => {
	const context = pageContext();
	context.store.config.shell.allow_local_network = false;
	context.hardeningStatus.detected_local_subnets = ["192.168.50.0/24"];
	context.row = (label, value) => `${label}:${value}`;
	const page = renderSecurityPage("shell", null, context);
	assert.match(page, /Allow my local network/);
	assert.match(page, /192\.168\.50\.0\/24/);
	assert.match(page, /link-local, cloud metadata, and Agent_b's own listener remain refused/i);
});

test("Connections summary row never renders decoder detail verbatim", () => {
	const context = pageContext();
	const profile = { id: "fake", label: "Fake", base_url: "http://fake/", capabilities: { findings: ["probe failed: Connection returned a web page, not model API JSON. Add the API path to base_url.", "probe detail: invalid character '<' looking for beginning of value"] } };
	context.serverProfiles = () => [profile];
	context.probeMessages.set("fake", { message: "Test failed — Connection returned a web page, not model API JSON. Add the API path to base_url.", alarm: true });
	const page = renderConnectionsPage(context);
	assert.match(page, /Test failed — Connection returned a web page, not model API JSON/);
	assert.doesNotMatch(page, /invalid character/);
});
