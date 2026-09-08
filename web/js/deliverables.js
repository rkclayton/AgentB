const producingTools = new Set(["write_file", "edit_file"]);

export function filesFromResponse(items) {
  const callRuns = new Map();
  for (const item of items || []) {
    try {
      if (item?.type !== "agent") continue;
      for (const callID of item.toolCallIDs || []) callRuns.set(callID, item.run_id || "");
    } catch {}
  }
  const files = new Map();
  for (const item of items || []) {
    try {
      if (item?.type !== "tool" || !producingTools.has(item.name) || item.result?.ok !== true) continue;
      const recorded = item.result?.file;
      const path = recorded?.path || legacyRelativePath(item.args?.path);
      if (typeof path !== "string" || !path) continue;
      files.set(path.toLowerCase(), {
        path,
        bytes: Number.isFinite(recorded?.bytes) ? recorded.bytes : null,
        callID: item.callID || "",
        runID: callRuns.get(item.callID) || "",
        openScope: "workspace",
        openPath: path,
      });
    } catch {}
  }
  const delivery = (items || []).find((item) => {
    try { return item?.type === "notice" && item.event?.type === "files.delivered"; }
    catch { return false; }
  })?.event?.data;
  if (delivery?.mode === "folder") return [];
  if (delivery?.mode === "both") {
    for (const item of delivery.items || []) {
      if (!item.exchange_path || !["copied", "identical"].includes(item.status)) continue;
      const file = files.get(String(item.source_path || "").toLowerCase());
      if (!file) continue;
      file.openScope = "exchange";
      file.openPath = item.exchange_path;
    }
  }
  return [...files.values()];
}

function legacyRelativePath(value) {
  if (typeof value !== "string" || !value.trim()) return "";
  const path = value.replaceAll("\\", "/");
  if (path.startsWith("/") || path.startsWith("//") || /^[a-z]:\//i.test(path)) return "";
  const parts = path.split("/").filter((part) => part && part !== ".");
  if (!parts.length || parts.some((part) => part === "..")) return "";
  return parts.join("/");
}

export function fileURL(sessionID, path) {
  const encoded = path.split("/").map(encodeURIComponent).join("/");
  const query = new URLSearchParams();
  if (sessionID) query.set("session", sessionID);
  return `/api/files/${encoded}${query.size ? `?${query}` : ""}`;
}

export async function probeFile(url, fetcher = fetch) {
  try {
    const response = await fetcher(url, { method: "HEAD" });
    if (!response.ok) return { state: "missing", bytes: null };
    const length = Number(response.headers.get("content-length"));
    return { state: "ready", bytes: Number.isFinite(length) && length >= 0 ? length : null };
  } catch {
    return { state: "missing", bytes: null };
  }
}

export function createFileChip(document, file, status, actions) {
  const root = document.createElement("div");
  root.className = `file-chip${status?.state === "missing" ? " missing" : ""}`;
  const name = document.createElement("span");
  name.className = "file-chip-name";
  name.textContent = file.path.split("/").at(-1);
  name.title = file.path;
  const size = document.createElement("span");
  size.className = "file-chip-size";
  size.textContent = status?.state === "missing" ? "missing" : formatBytes(status?.bytes ?? file.bytes);
  root.append(name, size);
  if (status?.state !== "missing") {
    const download = document.createElement("a");
    download.textContent = "download";
    download.href = actions.downloadURL;
    download.download = name.textContent;
    root.append(download);
  }
  const open = document.createElement("button");
  open.type = "button";
  open.textContent = "open folder";
  open.disabled = status?.state === "missing";
  open.onclick = actions.openFolder;
  root.append(open);
  return root;
}

export function formatBytes(value) {
  if (!Number.isFinite(value) || value < 0) return "size unknown";
  if (value < 1024) return `${value} B`;
  if (value < 1024 * 1024) return `${(value / 1024).toFixed(value < 10 * 1024 ? 1 : 0)} KiB`;
  return `${(value / (1024 * 1024)).toFixed(value < 10 * 1024 * 1024 ? 1 : 0)} MiB`;
}
