// Tiny JSX-to-HTML adapter for the existing string-rendered desktop frontend.
// It does not add a framework or move event handling out of the native DOM.
export const escapeText = (value: unknown) => String(value ?? "").replace(/[&<>"']/g, c => ({"&":"&amp;","<":"&lt;",">":"&gt;",'"':"&quot;","'":"&#39;"}[c]!));
export function h(tag: string | ((props: any) => string), attrs: Record<string, unknown> | null, ...children: unknown[]): string {
    if (typeof tag === "function") return tag({...attrs, children});
    const properties = Object.entries(attrs || {}).filter(([,v]) => v !== false && v != null).map(([k,v]) => ` ${k === "className" ? "class" : k}="${escapeText(v === true ? "" : v)}"`).join("");
    return `<${tag}${properties}>${children.flat().map(escapeText).join("")}</${tag}>`;
}
declare global { namespace JSX { type Element = string; interface IntrinsicElements { button: Record<string, unknown>; 'mdui-button': Record<string, unknown> } } }
