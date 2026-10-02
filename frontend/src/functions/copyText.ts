export async function copyText(text: string): Promise<void> {
  if (navigator.clipboard?.writeText) {
    try {
      await navigator.clipboard.writeText(text);
      return;
    } catch {
      // Older WebViews may expose the API but reject it outside a secure context.
      // Fall through to the legacy DOM copy path.
    }
  }

  const textarea = document.createElement("textarea");
  textarea.value = text;
  textarea.setAttribute("readonly", "");
  textarea.style.position = "fixed";
  textarea.style.left = "-9999px";
  textarea.style.opacity = "0";

  document.body.appendChild(textarea);
  textarea.focus();
  textarea.select();

  try {
    const ok = document.execCommand("copy");
    if (!ok) {
      throw new Error("Clipboard API is not available");
    }
  } finally {
    document.body.removeChild(textarea);
  }
}
