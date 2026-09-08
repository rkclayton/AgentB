export async function uploadAttachment(file, sessionID, options = {}) {
  const fetchImpl = options.fetchImpl || fetch;
  const form = new FormData();
  form.append("session_id", sessionID);
  form.append("file", file, file.name || fallbackName(file.type));
  const response = await fetchImpl("/api/attachments", {
    method: "POST",
    headers: { "X-AgentB-Mutation-Token": options.token || "" },
    body: form,
  });
  const data = await response.json().catch(() => ({}));
  if (!response.ok) throw new Error(data.error || `HTTP ${response.status}`);
  return data;
}

export async function exchangeFiles(options = {}) {
  const response = await (options.fetchImpl || fetch)("/api/operator-attachments", { cache: "no-store" });
  const data = await response.json().catch(() => ({}));
  if (!response.ok) throw new Error(data.error || `HTTP ${response.status}`);
  return data.files || [];
}

export async function exchangeUpload(item, sessionID, options = {}) {
  const fetchImpl = options.fetchImpl || fetch;
  if (options.maxBytes && item.bytes > options.maxBytes) throw new Error(`${item.path} exceeds the attachment size limit`);
  const query = new URLSearchParams({ path: item.path });
  const response = await fetchImpl(`/api/operator-attachments?${query}`, { cache: "no-store" });
  if (!response.ok) throw new Error(`Could not read ${item.path} from attachments`);
  const blob = await response.blob();
  const makeFile = options.makeFile || ((parts, name, init) => new File(parts, name, init));
  const file = makeFile([blob], item.path, { type: blob.type || "application/octet-stream" });
  return uploadAttachment(file, sessionID, { ...options, fetchImpl });
}

export function attachmentMetadata(value) {
  return { path: value.path, bytes: value.bytes, sha256: value.sha256 };
}

export function attachmentChipFile(value) {
  return { ...value, callID: `attachment:${value.sha256}`, openPath: value.path, openScope: "workspace" };
}

function fallbackName(type = "") {
  const extension = { "image/png": ".png", "image/jpeg": ".jpg", "image/gif": ".gif", "image/webp": ".webp" }[type] || "";
  return `pasted-attachment${extension}`;
}
