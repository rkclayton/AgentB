export function attachmentReadability(session, profiles, attachment) {
  const profile = (profiles || []).find((value) => value.id === session?.server_id);
  const capabilities = profile?.capabilities;
  if (!profile || !capabilities?.probed_at) return null;
  const handling = profile.attachment_handling || "auto";
  if (attachment?.sidecar) return null;
  if (attachment?.kind === "image" && handling === "extract")
    return "This image needs OCR before the profile can read it";
  if (attachment?.kind === "image" && handling === "auto" && !capabilities.image_input)
    return "This profile cannot read images · probe found no image input";
  if (attachment?.kind === "pdf" && handling === "extract")
    return "This PDF needs extraction before the profile can read it";
  if (attachment?.kind === "pdf" && handling === "auto" && !capabilities.document_input)
    return "This profile cannot read PDFs · probe found no document input";
  if (attachment?.kind === "binary")
    return "This profile cannot read this file type";
  return null;
}
