export function attachmentReadability(session, profiles, attachment) {
  const profile = (profiles || []).find((value) => value.id === session?.server_id);
  const capabilities = profile?.capabilities;
  if (!profile || !capabilities?.probed_at) return null;
  if (attachment?.kind === "image" && !capabilities.image_input)
    return "This profile cannot read images · probe found no image input";
  if (attachment?.kind === "pdf" && !attachment.sidecar && !capabilities.document_input)
    return "This profile cannot read PDFs · probe found no document input";
  if (attachment?.kind === "binary")
    return "This profile cannot read this file type";
  return null;
}
