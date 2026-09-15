export function closeIdleChat() {
  return { closed: false, confirmation: true };
}

export function attachmentKind(extension) {
  return extension.toLowerCase() === ".svg" ? "image" : "text";
}

export function retryReachability(probeSucceeded) {
  return { reachableEvent: false, probeSucceeded };
}

export function connectionSummary(status, contentType) {
  if (status === 200 && contentType.includes("text/html")) return "invalid character '<' looking for beginning of value";
  return "Connection ready.";
}

export function chatHeader(role, profile, folder) {
  return `${role} · ${profile} · ${folder}`;
}
