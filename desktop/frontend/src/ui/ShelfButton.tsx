import {h} from "./html.js";
export type ButtonProps = {label: string; value: string; id?: string; size?: "sm" | "md"; tone?: "neutral" | "primary" | "danger"; disabled?: boolean};
export function ShelfButton({label, value, id, size = "sm", tone = "neutral", disabled = false}: ButtonProps): string {
    const variant = tone === "danger" ? "btn-error" : tone === "primary" ? "btn-primary" : "btn-ghost";
    return <mdui-button type="button" variant={tone === "primary" ? "filled" : "text"} id={id} className={`btn btn-${size} ${variant}`} data-x={value} disabled={disabled}>{label}</mdui-button>;
}
