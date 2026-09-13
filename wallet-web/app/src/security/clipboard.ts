// PLAN.md §7's clipboard mitigation: paste is left enabled everywhere
// (blocking it is more theater than defense), but anything this app
// itself puts on the clipboard is cleared again a short time later.
const AUTO_CLEAR_MS = 30_000;

export async function copyWithAutoClear(text: string): Promise<void> {
  await navigator.clipboard.writeText(text);
  setTimeout(() => {
    // Best-effort: only clear if nothing else has overwritten the
    // clipboard since. Reading it back requires the same permission
    // writing did, so this stays silent (not thrown) if denied - the
    // write already succeeded, which is what the caller needed.
    navigator.clipboard
      .readText()
      .then((current) => {
        if (current === text) {
          void navigator.clipboard.writeText('');
        }
      })
      .catch(() => {});
  }, AUTO_CLEAR_MS);
}
