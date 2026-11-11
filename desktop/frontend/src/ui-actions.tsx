import {h} from "./ui/html.js";
import {ShelfButton} from "./ui/ShelfButton.js";
export function confirmationActions(ok: string, onlyOk: boolean, danger: boolean): string {
    const cancel = onlyOk ? "" : <ShelfButton label="取消" value="0" />;
    const accept = <ShelfButton label={ok} value="1" tone={danger ? "danger" : "primary"} />;
    return cancel + accept;
}
export function organizerAction(id: string, label: string, primary = false): string {
    return <ShelfButton id={id} label={label} value={id} size="md" tone={primary ? "primary" : "neutral"} />;
}
// MDUI applies disabled changes asynchronously. Wait for its native button
// before restoring focus, and never steal focus after navigation/user input.
export async function restoreActionFocus(target: HTMLElement | null, allowed: () => boolean): Promise<void> {
    if (!target) return;
    await (target as HTMLElement & {updateComplete?: Promise<unknown>}).updateComplete;
    if (target.isConnected && allowed()) target.focus({preventScroll: true});
}
