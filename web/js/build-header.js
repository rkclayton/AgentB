export function projectBuildHeader(build = {}, signature = {}) {
  const tag = build.tag || "v?";
  const commit = build.known && build.commit ? String(build.commit).slice(0, 7) : "unknown";
  const dirty = build.dirty ? "+dirty" : "";
  const files = Array.isArray(signature.files) ? signature.files : [];
  const valid = !!signature.supported && files.length > 0 && files.every((file) => file.status === "Valid");
  const invalid = !!signature.supported && !valid;
  return {
    text: `${tag} · ${commit}${dirty}`,
    title: build.known ? `${tag} · ${build.commit}${build.dirty ? " (dirty worktree)" : " (clean commit)"}` : `${tag} · build identity unavailable`,
    signatureClass: valid ? "valid" : invalid ? "invalid" : "unknown",
    signatureGlyph: valid ? "✓" : invalid ? "!" : "◇",
    signatureLabel: valid ? `${files.length} valid code signatures` : invalid ? "Code signature check failed" : "Code signature state unavailable",
  };
}

export function renderBuildHeader(buildNode, signatureNode, build, signature) {
  const state = projectBuildHeader(build, signature);
  buildNode.textContent = state.text;
  buildNode.title = state.title;
  signatureNode.textContent = state.signatureGlyph;
  signatureNode.className = `signature-state ${state.signatureClass}`;
  signatureNode.setAttribute("aria-label", state.signatureLabel);
  signatureNode.title = state.signatureLabel;
  return state;
}
