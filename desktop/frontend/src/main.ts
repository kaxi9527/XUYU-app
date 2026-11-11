import {renderOrganizer, type OrganizerDraft} from "./organize";
import {confirmationActions} from "./ui-actions";
import {mountWorks} from "./versions";
import {states as versionStates, roles as versionRoles} from './version-types';
import {mountAgentEnrollment} from "./agent-enrollment";
import {mountProjectMemory} from './project-memory';
import {mountProjectAnalysis} from './project-analysis';
import {mountProjectAnalysisBatch} from './project-analysis-batch';
import {mountProjectPlacement} from './project-placement';
import {mountBackgroundSettings} from './background-settings';
import {mountAutoOrganize} from './auto-organize';
import {hasStartChoice,mountWelcome,mountNewProject} from './welcome';
import {accessWasChecked,checkAccess} from './access-status';
import {aiBrandMark} from './ai-brands';
import {mountModelCenter} from './model-center';
import {agentPresentation, type AgentState} from "./agent-enrollment-types";
import "./graph-integration.css";
import "./ui/theme.css";
import "./organize-simple.css";
import "./projects.css";
import "./file-browsing.css";

const scheme = window.matchMedia("(prefers-color-scheme: dark)");
const applyScheme = () => {
    const root=document.documentElement;
    root.dataset.theme = scheme.matches ? "dark" : "light";
    root.classList.toggle('mdui-theme-dark',scheme.matches);
    root.classList.toggle('mdui-theme-light',!scheme.matches);
};
applyScheme(); scheme.addEventListener("change", applyScheme);

type Item = {
    path: string; name: string; dir: string; modTime: string; modUnix: number; size: number;
    agent: string; confidence: string; modified: boolean; workType: string; project: string;
    task: string; status: string; placement: string; note: string; projectId?: string; inPlace?: boolean;
};
type Entry = { name: string; path: string; isDir: boolean; size: number; modTime: string; status: string; agent: string };
type TaskInfo = {
    name: string; path: string; workType: string; template: string; status: string; agent: string;
    note: string; files: number; outputs: Entry[]; modTime: string;
};
type Column = { workType: string; template: string; tasks: TaskInfo[] };
type ProjectDetail = {
    folders?: {name: string; path: string; entries: Entry[]}[];
    linked?: boolean; linkTarget?: string;
    name: string; path: string; readme: string; workTypes: string[]; columns: Column[];
    materials: Entry[]; code: Entry[]; loose: Entry[]; sensitive: number;
};
type ProjectSummary = {
    name: string; path: string; workTypes: string[]; tasks: number; outputs: number; pending: number; modTime: string;
    source: string; id?: string; status?: string; category?: string; members?: number; aliases?: string[]; origin?: string;
    linked?: boolean; linkTarget?: string;
};
type Member = { path: string; kind: string; isDir: boolean; via?: string };
type PlaceDetail = {
    project: { id: string; name: string; path: string; markers: string[]; origin?: string; branch?: string; members: Member[]; aliases?: string[]; modTime: string; status: string; reasons?: string[] };
    readme: string; readmeName: string; recent: Item[];
};
type RegStatus = { exists: boolean; path: string; generatedAt: string; projects: number; hidden: number; running: boolean; error?: string; unreadable?:number; partial?:boolean; partialReasons?:string[] };
type Thumb = { kind: string; data?: string; lines?: string[]; ext: string; label: string; group: string };
type HomePreviewItem = { source: string; target: string; action: string; crossDevice?: boolean; skip?: boolean; reason?: string; whole?: boolean };
type HomeResultItem = { source: string; target: string; action: string; crossDevice?: boolean; error?: string; keptSource?: boolean; undoable?: boolean };
type HomeSummary = {
    dryRun: boolean; root: string; logId?: string; logPath?: string; undoPath?: string;
    preview?: HomePreviewItem[]; items?: HomeResultItem[];
    moved: number; copied: number; skipped: number; failed: number; crossHint?: string;
};
type TrashPreviewItem = { path: string; name: string; isDir: boolean; fileCount: number; byteSize: number; action: string; skip?: boolean; reason?: string; label: string };
type TrashResultItem = { path: string; dest?: string; action: string; undoable?: boolean; error?: string };
type InstallStatus = {
    os: string; runningPath: string; preferredPath: string; inApplications: boolean;
    needsInstall: boolean; preferredExists: boolean; message: string;
};
type MergeCandidate = { key: string; name: string; paths: string[]; sources: string[]; ids?: string[]; reasons: string[]; members: number; level?: 'recommended'|'possible'|'conflict' };
type CleanupSuggestion = {
    path: string; name: string; isDir: boolean; category: string; reason: string; evidence: string;
    confidence: string; preCheck: boolean; byteSize: number; fileCount: number; project?: string;
};
type InferredWorkType = {
    label: string; source: string; confidence: string; reason: string;
    template?: string; sensitive?: boolean; alreadyInReadme: boolean; preConfirm: boolean;
};
type ViewMode = "list" | "grid" | "status" | "timeline";

type TrashSummary = {
	 token?: string; hiddenCount?: number;
    dryRun: boolean; root: string; logId?: string; logPath?: string; undoPath?: string;
    preview?: TrashPreviewItem[]; items?: TrashResultItem[];
    trashed: number; staged: number; skipped: number; failed: number;
    totalFiles: number; totalBytes: number; methodHint?: string;
};

const view = document.getElementById("view")!;
const foot = document.getElementById("foot")!;

const useMock = import.meta.env.DEV && new URLSearchParams(location.search).has("mock");

export async function call<T>(method: string, ...args: unknown[]): Promise<T> {
    if (useMock) return (await import("./mock")).mockCall(method, args) as Promise<T>;
    const {Call} = await import("@wailsio/runtime");
    return Call.ByName("main.ShelfService." + method, ...args) as Promise<T>;
}

export function esc(s: unknown): string {
    return String(s ?? "").replace(/[&<>"']/g, c => ({"&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;"}[c]!));
}

function errText(e: unknown): string {
    const s = e instanceof Error ? e.message : String(e);
    return s.replace(/^Error:\s*/, "");
}

function when(iso: string): string {
    if (!iso) return "";
    const d = new Date(iso);
    const now = new Date();
    const hm = d.toLocaleTimeString("zh-CN", {hour: "2-digit", minute: "2-digit", hour12: false});
    if (d.toDateString() === now.toDateString()) return "今天 " + hm;
    const y = new Date(now.getTime() - 86400000);
    if (d.toDateString() === y.toDateString()) return "昨天 " + hm;
    return (d.getMonth() + 1) + "月" + d.getDate() + "日 " + hm;
}

function dayKey(iso: string): string {
    const d = new Date(iso);
    return d.getFullYear() + "-" + (d.getMonth() + 1) + "-" + d.getDate();
}

function size(n: number): string {
    if (n < 1024) return n + " B";
    if (n < 1048576) return (n / 1024).toFixed(1) + " KB";
    if (n < 1073741824) return (n / 1048576).toFixed(1) + " MB";
    return (n / 1073741824).toFixed(2) + " GB";
}

let home = "";
let rootDir = "";
let osName = "darwin";

function short(p: string): string {
    return home && (p === home || p.startsWith(home + "/")) ? "~" + p.slice(home.length) : p;
}

const revealLabel = () => osName === "darwin" ? "在访达中显示" : osName === "windows" ? "在资源管理器中显示" : "打开所在文件夹";

// ---------- AI 品牌图标 ----------
// One versioned registry serves files, marking tools and the model center.

function agentBadge(agent: string, withName = true): string {
    const a = (agent || "").trim();
    return `<span class="agent${a?'':' unknown'}" title="${esc(a||'未知来源')}">${aiBrandMark(a,esc)}${withName ? `<span class="agent-name">${esc(a||'未知来源')}</span>` : ""}</span>`;
}

// ---------- 缩略图：懒加载 + 限并发 ----------
const thumbCache = new Map<string, Thumb>();
const thumbQueue: HTMLElement[] = [];
let thumbActive = 0;
const groupGlyph: Record<string, string> = {image: "图", pdf: "PDF", doc: "文", sheet: "表", slide: "演", archive: "包", video: "影", audio: "音", design: "设", code: "</>", text: "文", folder: "夹", lock: "🔒", file: "·"};

function thumbSlot(path: string, name: string, isDir = false): string {
    const ext = (name.match(/\.[^.]+$/) || [""])[0].slice(1).toUpperCase().slice(0, 4);
    return `<div class="thumb" data-thumb="${esc(path)}" aria-hidden="true"><span class="thumb-ext">${isDir ? "文件夹" : esc(ext || "文件")}</span></div>`;
}

function paintThumb(el: HTMLElement, t: Thumb) {
    if (t.kind === "image" && t.data) el.innerHTML = `<img alt="" src="${t.data}">`;
    else if (t.kind === "text" && t.lines) el.innerHTML = `<pre class="thumb-text">${esc(t.lines.join("\n"))}</pre>`;
    else el.innerHTML = `<span class="thumb-icon g-${esc(t.group)}"><b>${esc(groupGlyph[t.group] || "·")}</b><small>${esc(t.label)}</small></span>`;
    el.classList.add("ready");
}

function pumpThumbs() {
    while (thumbActive < 4 && thumbQueue.length) {
        const el = thumbQueue.shift()!;
        if (!el.isConnected) continue;
        const p = el.dataset.thumb!;
        const hit = thumbCache.get(p);
        if (hit) { paintThumb(el, hit); continue; }
        thumbActive++;
        call<Thumb>("Thumb", p).then(t => { thumbCache.set(p, t); paintThumb(el, t); })
            .catch(() => el.classList.add("ready"))
            .finally(() => { thumbActive--; pumpThumbs(); });
    }
}

const thumbObs = new IntersectionObserver(ents => {
    for (const e of ents) if (e.isIntersecting) { thumbObs.unobserve(e.target); thumbQueue.push(e.target as HTMLElement); }
    pumpThumbs();
}, {rootMargin: "200px"});

// 渲染完成后的通用处理：缩略图、键盘可达（WebKit 默认 Tab 不经过按钮和链接）、显示按钮。
function enhance(root: HTMLElement) {
    root.querySelectorAll<HTMLElement>("[data-thumb]").forEach(el => thumbObs.observe(el));
    root.querySelectorAll<HTMLElement>("a[href], summary").forEach(el => { if (el.tabIndex < 0 || !el.hasAttribute("tabindex")) el.tabIndex = 0; });
    root.querySelectorAll<HTMLButtonElement>("[data-reveal]").forEach(b => {
        b.addEventListener("click", ev => {
            ev.stopPropagation();
            call("Reveal", b.dataset.reveal).catch(e => confirmBox(`<h3>打不开</h3><p>${esc(errText(e))}</p>`, "好", true));
        });
    });
}

// ---------- 状态 ----------
const loading = (what = "读取中") => `<div class="state" role="status" aria-live="polite"><span class="spinner" aria-hidden="true"></span>${esc(what)}……</div>`;
const failed = (what: string, e: unknown) => `<div class="state error" role="alert"><b>${esc(what)}</b><p>${esc(errText(e))}</p><mdui-button variant="text" class="btn btn-sm btn-ghost" data-retry>再试一次</mdui-button></div>`;
const emptyState = (title: string, tip = "") => `<div class="state empty"><b>${esc(title)}</b>${tip ? `<p>${tip}</p>` : ""}</div>`;

function bindRetry(root: HTMLElement, fn: () => void) {
    root.querySelectorAll<HTMLButtonElement>("[data-retry]").forEach(b => b.onclick = () => fn());
}

const placeClass: Record<string, string> = {
    "已归位": "ok", "待批准": "warn", "放错位置": "bad", "项目内未归类": "warn", "项目外": "mute", "总控": "mute", "原处未归位": "info",
};
const placeTip: Record<string, string> = {
    "已归位": "在总文件夹里该在的位置", "待批准": "AI 交的成品，等你批准", "放错位置": "在桌面、下载、主目录顶层等不该放的地方",
    "项目内未归类": "在项目里，但不在任务/成品/资料/代码下", "项目外": "不在任何已知项目里", "原处未归位": "属于一个还在原处的项目，还没搬进总文件夹",
};
const confTip: Record<string, string> = {
    "自报": "AI 自己登记的", "入库登记": "用 workshelf put 交进来的", "侧边记录": "文件旁边的 .workshelf-origin.jsonl 里记着",
    "推断": "按任务说明推断的，不一定准", "来源不明": "没有任何记录说明是谁写的",
};

function projectLink(it: Item): string {
    if (!it.project) return `<span class="where-dir">${esc(short(it.dir))}</span>`;
    const href = it.inPlace && it.projectId ? `#/place/${encodeURIComponent(it.projectId)}` : `#/project/${encodeURIComponent(it.project)}`;
    return `<a class="proj" href="${href}" title="打开项目「${esc(it.project)}」">${esc(it.project)}</a>${it.task ? `<span class="sep">/</span><span class="task-name">${esc(it.task)}</span>` : ""}`;
}



async function refreshInstallBanner() {
    const el = document.getElementById("install-banner");
    if (!el) return;
    try {
        const st = await call<InstallStatus>("InstallStatus");
        if (!st.needsInstall) { el.hidden = true; el.innerHTML = ""; return; }
        el.hidden = false;
        el.innerHTML = `<div class="banner warn-banner" role="status">
          <div><b>建议装到应用程序文件夹</b><p class="sub" style="margin:4px 0 0">${esc(st.message)}</p>
          <p class="sub">固定位置：<code title="${esc(st.preferredPath)}">${esc(short(st.preferredPath) || st.preferredPath)}</code></p></div>
          <mdui-button variant="text" class="btn btn-sm btn-primary" id="btn-install-app" type="button">安装到应用程序文件夹</mdui-button>
        </div>`;
        enhance(el);
        el.querySelector<HTMLButtonElement>("#btn-install-app")!.onclick = async () => {
            const b = el.querySelector<HTMLButtonElement>("#btn-install-app")!;
            b.disabled = true; b.textContent = "安装中……";
            try {
                const done = await call<InstallStatus>("InstallToApplications");
                await confirmBox(`<h3>已装到应用程序文件夹</h3>
                  <p>位置：<code>${esc(done.preferredPath)}</code></p>
                  <p>请<strong>退出当前这个窗口</strong>，再从「应用程序」里的 序屿 打开。授权点一次就会记住。</p>
                  <p class="sub">这次没有替你自动打开，免得抢前台。</p>`, "好", true);
                refreshInstallBanner();
            } catch (e) {
                b.disabled = false; b.textContent = "安装到应用程序文件夹";
                await confirmBox(`<h3>没装成</h3><p>${esc(errText(e))}</p>
                  <p class="sub">也可以在终端跑：<code>scripts/install-mac.sh</code></p>`, "好", true);
            }
        };
    } catch { el.hidden = true; }
}

// ---------- 多选 / 删除 ----------
const selected = new Set<string>();
let lastVisiblePaths: string[] = [];
let fileSelecting = false;
let fileLoading = false;
let routeEpoch = 0;
let recentRequest = 0;
const trashIcon = '<svg aria-hidden="true" focusable="false" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"><path d="M4 7h16M9 7V4h6v3M6 7l1 13h10l1-13M10 10v7m4-7v7"/></svg><span class="sr-only">移到废纸篓</span>';

function syncSelBar() {
    const n = selected.size;
    const del = document.getElementById("sel-delete") as HTMLButtonElement | null;
    const label = document.getElementById("sel-count");
    const allBtn = document.getElementById("sel-all") as HTMLButtonElement | null;
    const noneBtn = document.getElementById("sel-none") as HTMLButtonElement | null;
    if (label) label.textContent = n ? `已选 ${n}` : "";
    if (del) { del.hidden = n === 0; del.textContent = `删除（${n}）`; }
    if (allBtn) allBtn.hidden = lastVisiblePaths.length === 0;
    if (noneBtn) noneBtn.hidden = n === 0;
    const mode = document.getElementById('file-select-mode') as HTMLButtonElement | null;
    if (mode) {
        view.classList.toggle('file-selecting', fileSelecting);
        mode.textContent = fileSelecting ? '完成选择' : '批量选择';
        mode.setAttribute('aria-pressed', String(fileSelecting));
        mode.disabled = fileLoading || (!lastVisiblePaths.length && !fileSelecting);
        if (allBtn) { allBtn.hidden = !fileSelecting || !lastVisiblePaths.length; allBtn.disabled = fileLoading; }
        if (noneBtn) { noneBtn.hidden = !fileSelecting || !n; noneBtn.disabled = fileLoading; }
        if (del) { del.hidden = !fileSelecting || !n; del.disabled = fileLoading; }
        if (label) label.hidden = !fileSelecting;
        view.querySelectorAll<HTMLInputElement>('input[data-sel]').forEach(cb => { cb.closest('label')!.hidden = !fileSelecting; });
    }
    document.querySelectorAll<HTMLInputElement>("input[data-sel]").forEach(cb => {
        cb.checked = selected.has(cb.dataset.sel!);
    });
}

function selToolbarHTML(): string {
    return `<div class="sel-bar" role="toolbar" aria-label="多选操作">
      <mdui-button variant="text" class="btn btn-sm btn-ghost" id="file-select-mode" type="button" aria-pressed="false">批量选择</mdui-button>
      <mdui-button variant="text" class="btn btn-sm btn-ghost" id="sel-all" type="button" hidden>全选当前列表</mdui-button>
      <mdui-button variant="text" class="btn btn-sm btn-ghost" id="sel-none" type="button" hidden>取消全选</mdui-button>
      <mdui-button variant="text" class="btn btn-sm btn-error" id="sel-delete" type="button" hidden>删除</mdui-button>
      <span id="sel-count" class="count" aria-live="polite"></span>
    </div>`;
}

function bindSelToolbar(onDeleted: () => void) {
    const mode = document.getElementById('file-select-mode');
    if (mode) mode.onclick = () => { fileSelecting = !fileSelecting; if (!fileSelecting) selected.clear(); syncSelBar(); };
    const all = document.getElementById("sel-all");
    const none = document.getElementById("sel-none");
    const del = document.getElementById("sel-delete");
    if (all) all.onclick = () => { for (const p of lastVisiblePaths) selected.add(p); syncSelBar(); };
    if (none) none.onclick = () => { selected.clear(); syncSelBar(); };
    if (del) del.onclick = () => confirmAndDelete([...selected], () => { selected.clear(); onDeleted(); });
}

function trashPreviewHTML(sum: TrashSummary): string {
    const rows = (sum.preview || []).map(it => it.skip
        ? `<li class="sub">跳过 <code>${esc(short(it.path))}</code>：${esc(it.reason || "")}</li>`
        : `<li>${esc(it.label)}</li>`).join("");
    const nGo = (sum.preview || []).filter(x => !x.skip).length;
    return `<h2>确认删除？</h2>
      <p>将处理 <b>${nGo}</b> 项（合计约 ${sum.totalFiles} 个文件，${size(sum.totalBytes)}）。文件和文件夹都会挪进废纸篓（或删除暂存），可以撤销，不会直接粉碎。</p>
      ${sum.methodHint ? `<p class="sub">${esc(sum.methodHint)}</p>` : ""}
      <ul class="home-preview">${rows || "<li class=\"sub\">没有可删的条目</li>"}</ul>`;
}

function trashResultHTML(sum: TrashSummary): string {
    return `<h2>${sum.failed ? "部分删除未完成" : "删除完成"}</h2>
      <p>进废纸篓 ${sum.trashed} · 进删除暂存 ${sum.staged} · 跳过 ${sum.skipped} · 失败 ${sum.failed}</p>
      ${sum.undoPath ? `<p class="sub">撤销清单：<code title="${esc(sum.undoPath)}">${esc(short(sum.undoPath))}</code></p>` : ""}
      ${(sum.items || []).filter(i => i.action === "失败").map(i => `<p class="err">${esc(short(i.path))}：${esc(i.error || "")}</p>`).join("")}`;
}

async function confirmAndDelete(paths: string[], after?: () => void) {
    if (!paths.length) return;
    try {
        const sum = await call<TrashSummary>("PreviewDelete", paths);
        const nGo = (sum.preview || []).filter(x => !x.skip).length;
        if (!nGo) {
            await confirmBox(trashPreviewHTML(sum) + `<p class="sub">没有可删除的条目。</p>`, "好", true);
            return;
        }
        if (!sum.token) throw new Error("删除预览已失效，请重新预览。");
        const hidden = (sum.hiddenCount || 0) > 0;
        const warning = hidden ? `<p class="organize-delete-warning"><b>包含 ${sum.hiddenCount} 项隐藏内容，包括所选文件夹内部。</b>这些内容可能是工具配置、会话记录或运行环境，删除后相关工具可能无法正常工作。</p>` : "";
        if (!await confirmBox(warning + trashPreviewHTML(sum), hidden ? "我已了解，删除隐藏文件" : "移到废纸篓", false, true)) return;
        const done = await call<TrashSummary>("ApplyDeletePlan", sum.token, hidden);
        if (done.undoPath && done.trashed + done.staged > 0) {
            const restore = await confirmBox(trashResultHTML(done), "撤销本次删除", false, false, "完成");
            if (restore) {
                try {
                    const restored = await call<{restored:number;skipped:number;failed:number;notes?:string[]}>("UndoDelete", done.undoPath);
                    await confirmBox(`<h2>${restored.failed || restored.skipped ? "部分内容还未恢复" : "已撤销本次删除"}</h2><p>已恢复 ${restored.restored} 项 · 跳过 ${restored.skipped} 项 · 失败 ${restored.failed} 项。</p>${(restored.notes || []).map(note => `<p class="sub">${esc(note)}</p>`).join("")}`, "完成", true);
                } catch (e) {
                    await confirmBox(`<h2>恢复未完成</h2><p>${esc(errText(e))}</p><p class="sub">删除记录仍保留，可以稍后按记录恢复。</p><code>${esc(done.undoPath)}</code>`, "完成", true);
                }
            }
        } else await confirmBox(trashResultHTML(done), "完成", true);
        after?.();
    } catch (e) {
        await confirmBox(`<h2>删除没做成</h2><p>${esc(errText(e))}</p>`, "好", true);
    }
}

function itemRow(it: Item): string {
    return `<div class="row">
      <label class="sel"><input class="checkbox checkbox-primary checkbox-sm" type="checkbox" data-sel="${esc(it.path)}" ${selected.has(it.path) ? "checked" : ""} aria-label="选中 ${esc(it.name)}"></label>
      ${thumbSlot(it.path, it.name)}
      <div class="row-main">
        <mdui-button variant="text" class="row-name file-name-button" type="button" data-reveal="${esc(it.path)}" title="${revealLabel()}：${esc(it.path)}" aria-label="${revealLabel()}：${esc(it.name)}">${esc(it.name)}</mdui-button>${it.modified ? ' <span class="chip bad" title="登记后内容又被改过">写后被改过</span>' : ""}
        <div class="row-where">${projectLink(it)}${it.workType ? `<span class="wt">${esc(it.workType)}</span>` : ""}</div>
        ${it.note ? `<div class="row-note" title="${esc(it.note)}">${esc(it.note)}</div>` : ""}
        ${it.project ? `<div class="row-dir" title="${esc(it.dir)}">${esc(short(it.dir))}</div>` : ""}
      </div>
      <div class="row-agent">${agentBadge(it.agent)}<span class="chip ${it.confidence === "来源不明" ? "mute" : it.confidence === "推断" ? "warn" : "ok"}" title="${esc(confTip[it.confidence] || "")}">${esc(it.confidence)}</span></div>
      <div class="row-place"><span class="chip ${placeClass[it.placement] || "mute"}" title="${esc(placeTip[it.placement] || "")}">${esc(it.placement)}</span></div>
      <div class="row-time"><time datetime="${esc(it.modTime)}">${when(it.modTime)}</time><span class="sub">${size(it.size)}</span></div>
      <div class="row-act">
        <mdui-button variant="text" class="file-trash-btn btn btn-sm btn-ghost btn-square" type="button" data-del="${esc(it.path)}" aria-label="删除 ${esc(it.name)}" title="移到废纸篓">${trashIcon}</mdui-button>
      </div>
    </div>`;
}

const filters = ["全部", "待批准", "放错位置", "原处未归位", "来源不明", "写后被改过"];
const VIEW_KEY = "workshelf.viewMode";
const viewModes: {id: ViewMode; label: string; tip: string}[] = [
    {id: "list", label: "列表", tip: "看名字、状态、谁写的（像访达列表）"},
    {id: "grid", label: "网格", tip: "大缩略图，好认文件"},
    {id: "status", label: "按状态", tip: "待批准 / 放错 / 未归位分组"},
    {id: "timeline", label: "时间线", tip: "按最近改动的日期排"},
];
function loadViewMode(): ViewMode {
    try {
        const v = localStorage.getItem(VIEW_KEY);
        if (v === "list" || v === "grid" || v === "status" || v === "timeline") return v;
    } catch {}
    return "list";
}
function saveViewMode(m: ViewMode) {
    try { localStorage.setItem(VIEW_KEY, m); } catch {}
}
const state = {days: 3, filter: "全部", query: "", view: loadViewMode() as ViewMode};

function applyFilter(items: Item[], f: string): Item[] {
    if (f === "放错位置") return items.filter(i => i.placement === "放错位置" || i.placement === "项目内未归类" || i.placement === "项目外");
    if (f === "待批准") return items.filter(i => i.placement === "待批准" || i.status === "待批准");
    if (f === "原处未归位") return items.filter(i => i.placement === "原处未归位");
    if (f === "来源不明") return items.filter(i => i.confidence === "来源不明");
    if (f === "写后被改过") return items.filter(i => i.modified);
    return items;
}


function viewSwitcherHTML(current: ViewMode): string {
    return `<div class="view-switch" role="toolbar" aria-label="视图切换">
      <span class="view-switch-label">视图</span>
      ${viewModes.map(m => `<mdui-button variant="text" type="button" class="view-btn ${m.id === current ? "on" : ""}" data-view="${m.id}" aria-pressed="${m.id === current}" title="${esc(m.tip)}">${esc(m.label)}</mdui-button>`).join("")}
    </div>`;
}

function itemCard(it: Item): string {
    return `<div class="file-card">
      <label class="sel"><input class="checkbox checkbox-primary checkbox-sm" type="checkbox" data-sel="${esc(it.path)}" ${selected.has(it.path) ? "checked" : ""} aria-label="选中 ${esc(it.name)}"></label>
      ${thumbSlot(it.path, it.name)}
      <div class="file-card-body">
        <mdui-button variant="text" class="row-name file-name-button" type="button" data-reveal="${esc(it.path)}" title="${revealLabel()}：${esc(it.path)}" aria-label="${revealLabel()}：${esc(it.name)}">${esc(it.name)}</mdui-button>
        <div class="row-where">${projectLink(it)}</div>
        <div class="file-card-meta">
          <span class="chip ${placeClass[it.placement] || "mute"}">${esc(it.placement)}</span>
          ${it.agent ? agentBadge(it.agent) : ""}
          <time datetime="${esc(it.modTime)}">${when(it.modTime)}</time>
        </div>
      </div>
      <div class="row-act">
        <mdui-button variant="text" class="file-trash-btn btn btn-sm btn-ghost btn-square" type="button" data-del="${esc(it.path)}" aria-label="删除 ${esc(it.name)}" title="移到废纸篓">${trashIcon}</mdui-button>
      </div>
    </div>`;
}

function statusGroupKey(it: Item): string {
    if (it.placement === "待批准" || it.status === "待批准") return "待批准";
    if (it.placement === "放错位置" || it.placement === "项目外" || it.placement === "项目内未归类") return "放错位置";
    if (it.placement === "原处未归位") return "原处未归位";
    if (it.confidence === "来源不明") return "来源不明";
    if (it.modified) return "写后被改过";
    if (it.placement === "已归位") return "已归位";
    return it.placement || "其他";
}

function renderItemsHTML(items: Item[], mode: ViewMode, withDayHeads: boolean): string {
    if (!items.length) return "";
    if (mode === "grid") {
        return `<div class="file-grid">${items.map(itemCard).join("")}</div>`;
    }
    if (mode === "status") {
        const order = ["待批准", "放错位置", "原处未归位", "来源不明", "写后被改过", "已归位", "其他"];
        const groups = new Map<string, Item[]>();
        for (const it of items) {
            const k = statusGroupKey(it);
            groups.set(k, [...(groups.get(k) || []), it]);
        }
        const keys = [...order.filter(k => groups.has(k)), ...[...groups.keys()].filter(k => !order.includes(k))];
        return keys.map(k => {
            const list = groups.get(k)!;
            return `<section class="status-group" aria-label="${esc(k)}">
              <h2 class="status-head"><span class="chip ${placeClass[k] || "mute"}">${esc(k)}</span> <span class="count">${list.length}</span></h2>
              <div class="list">${list.map(itemRow).join("")}</div>
            </section>`;
        }).join("");
    }
    // list + timeline：时间线强化日期头
    let html = "", last = "";
    const wrapOpen = mode === "timeline" ? `<div class="timeline">` : `<div class="list">`;
    const wrapClose = `</div>`;
    for (const it of items) {
        const k = dayKey(it.modTime);
        if (withDayHeads && k !== last && !state.query) {
            const label = when(it.modTime).split(" ")[0];
            html += mode === "timeline"
                ? `<div class="tl-day" role="heading" aria-level="2"><span class="tl-dot" aria-hidden="true"></span><span>${esc(label)}</span></div>`
                : `<div class="day">${esc(label)}</div>`;
            last = k;
        }
        html += mode === "timeline" ? `<div class="tl-item">${itemRow(it)}</div>` : itemRow(it);
    }
    return wrapOpen + html + wrapClose;
}

async function renderHome() {
    view.classList.add('file-browser');
    view.innerHTML = `
      <header class="bar">
        <h1>最近文件</h1>
        <label class="sr-only" for="q">搜索文件</label>
        <input id="q" class="search input input-sm" type="search" placeholder="搜文件名、项目、谁写的（回车搜索，Esc 清空）" value="${esc(state.query)}">
      </header>
      <p class="lead">查看最近记录的文件，点文件名在${osName === 'darwin' ? ' Finder ' : '文件管理器中'}定位。</p>
      <div class="toolbar-stack">
        <div class="filters" role="toolbar" aria-label="筛选">
          <label class="sr-only" for="days">时间范围</label>
          <select class="select select-sm" id="days">${[1, 3, 7, 14, 30].map(d => `<option value="${d}" ${d === state.days ? "selected" : ""}>最近 ${d} 天</option>`).join("")}</select>
          ${filters.map(f => `<mdui-button variant="text" class="chipbtn ${f === state.filter ? "on" : ""}" data-f="${f}" aria-pressed="${f === state.filter}">${f}<span class="n" data-n="${f}"></span></mdui-button>`).join("")}
          <mdui-button variant="text" class="btn btn-sm btn-primary" id="home-batch" hidden>一键归位所选</mdui-button>
          <span id="count" class="count" aria-live="polite"></span>
        </div>
        ${viewSwitcherHTML(state.view)}
      </div>
      ${selToolbarHTML()}
      <div id="list" class="content-host">${loading()}</div>`;
    const q = document.getElementById("q") as HTMLInputElement;
    q.addEventListener("keydown", e => {
        if (e.key === "Enter") { state.query = q.value.trim(); load(); }
        if (e.key === "Escape") { q.value = ""; state.query = ""; load(); }
    });
    (document.getElementById("days") as HTMLSelectElement).addEventListener("change", e => {
        state.days = Number((e.target as HTMLSelectElement).value); load();
    });
    view.querySelectorAll<HTMLButtonElement>("[data-f]").forEach(b => b.addEventListener("click", () => {
        state.filter = b.dataset.f!;
        view.querySelectorAll<HTMLButtonElement>("[data-f]").forEach(x => { x.classList.toggle("on", x === b); x.setAttribute("aria-pressed", String(x === b)); });
        load();
    }));
    view.querySelectorAll<HTMLButtonElement>("[data-view]").forEach(b => b.addEventListener("click", () => {
        const m = b.dataset.view as ViewMode;
        state.view = m;
        saveViewMode(m);
        view.querySelectorAll<HTMLButtonElement>("[data-view]").forEach(x => {
            const on = x.dataset.view === m;
            x.classList.toggle("on", on);
            x.setAttribute("aria-pressed", String(on));
        });
        load();
    }));
    enhance(view);
    bindSelToolbar(load);
    await load();
}

async function load() {
    const request = ++recentRequest, epoch = routeEpoch;
    const list = document.getElementById("list")!;
    const count = document.getElementById("count")!;
    const current = () => request === recentRequest && epoch === routeEpoch && list.isConnected;
    fileLoading = true;
    syncSelBar();
    const batch = document.getElementById("home-batch") as HTMLButtonElement | null;
    if (batch) batch.hidden = true;
    list.innerHTML = loading(state.query ? "搜索中" : "读取中");
    try {
        const all = (state.query ? await call<Item[]>("Search", state.query) : await call<Item[]>("Recent", state.days, 400)) || [];
        if (!current()) return;
        for (const f of filters) {
            const n = document.querySelector<HTMLElement>(`[data-n="${f}"]`);
            if (n) n.textContent = f === "全部" ? "" : String(applyFilter(all, f).length || "");
        }
        const items = applyFilter(all, state.filter);
        count.textContent = `${items.length} 个文件` + (state.query ? `（搜「${state.query}」）` : "");
        if (!items.length) {
            list.innerHTML = state.query
                ? emptyState(`没搜到「${state.query}」`, "换个词试试，或按 Esc 回到最近动态。")
                : state.filter !== "全部"
                    ? emptyState(`最近 ${state.days} 天没有「${state.filter}」的文件`, "这是好事。也可以把时间范围调大看看。")
                    : emptyState(`最近 ${state.days} 天没看到 AI 写的文件`, `把时间范围调到 7 天或 30 天试试；或者到「项目」里看看识别出的项目。`);
            lastVisiblePaths = [];
            selected.clear();
            syncSelBar();
            return;
        }
        const mode = state.view;
        list.className = "content-host" + (mode === "grid" ? " is-grid" : mode === "timeline" ? " is-timeline" : mode === "status" ? " is-status" : " is-list");
        list.innerHTML = renderItemsHTML(items, mode, true);
        enhance(list);
        lastVisiblePaths = items.map(i => i.path);
        for (const p of [...selected]) if (!lastVisiblePaths.includes(p)) selected.delete(p);
        list.querySelectorAll<HTMLInputElement>("input[data-sel]").forEach(cb => {
            cb.onchange = () => { if (cb.checked) selected.add(cb.dataset.sel!); else selected.delete(cb.dataset.sel!); syncSelBar(); };
        });
        list.querySelectorAll<HTMLButtonElement>("[data-del]").forEach(b => {
            b.onclick = () => confirmAndDelete([b.dataset.del!], load);
        });
        syncSelBar();
        const batch = document.getElementById("home-batch") as HTMLButtonElement | null;
        if (batch) {
            const can = state.filter === "放错位置" || state.filter === "原处未归位" || state.filter === "项目外";
            const paths = items.filter(i => i.project && (i.placement === "放错位置" || i.placement === "项目外" || i.placement === "原处未归位" || i.placement === "项目内未归类")).map(i => i.path);
            batch.hidden = !(can && paths.length);
            batch.textContent = `一键归位（${paths.length}）`;
            batch.onclick = () => confirmAndHome("PreviewHomeItems", "ApplyHomeItems", paths, load);
        }
    } catch (e) {
        if (!current()) return;
        selected.clear(); lastVisiblePaths = [];
        list.innerHTML = failed("没读出来", e);
        bindRetry(list, load);
    } finally {
        if (current()) { fileLoading = false; syncSelBar(); }
    }
}

// ---------- 项目 ----------
let organizerDraft: OrganizerDraft | undefined;
function startProjectGather(target: string, sources: string[]) {
    organizerDraft = {project:target,sources:[...sources]};
    location.hash = '#/organize';
}
function openOrganizer(key: string) {
    const draft = organizerDraft; organizerDraft = undefined;
    return renderOrganizer(key, call, esc, (html, ok, dismiss) => confirmBox(html, ok, false, false, dismiss), draft);
}
const IGNORED_MERGES_KEY = 'workshelf:ignored-project-suggestions:v1';
let ignoredMerges: Set<string>;
try { const stored=JSON.parse(localStorage.getItem(IGNORED_MERGES_KEY)||'[]');ignoredMerges=new Set(Array.isArray(stored)?stored.filter(x=>typeof x==='string'):[]); } catch { ignoredMerges=new Set(); }
const mergeSuggestionKey=(c:MergeCandidate)=>JSON.stringify([c.key,...[...c.paths].sort()]);
let mergeFeedback='';
function saveIgnoredMerges() {
    try {localStorage.setItem(IGNORED_MERGES_KEY,JSON.stringify([...ignoredMerges]));}
    catch {mergeFeedback='已在本次使用中更新建议；暂时无法保存，下次打开可能恢复。';}
}

async function renderProjects() {
    const epoch=routeEpoch;
    view.innerHTML = `<header class="bar"><div><h1>我的项目</h1><p class="sub">文件跟着项目走，找起来更容易。</p></div><div class="actions"><mdui-button class="btn" variant="text" href="#/organize">整理旧项目</mdui-button><mdui-button class="page-primary btn btn-primary" variant="filled" href="#/new-project">新建项目</mdui-button></div></header><div id="grid" class="project-list">${loading()}</div>`;
    const grid = document.getElementById("grid")!;
    try {
        let st = await call<RegStatus>("RegistryStatus");
        if ((!st.exists || Date.now()-Date.parse(st.generatedAt)>12*60*60*1000) && !st.running) {
            grid.innerHTML = loading("第一次打开，正在识别本机的项目（只读，不搬动任何文件）");
            st = await call<RegStatus>("RegistryRefresh");
        }
        const ps = (await call<ProjectSummary[]>("Projects")) || [];
        const suggestions = (await call<MergeCandidate[]>("MergeSuggest").catch(() => [])) || [];
        if(epoch!==routeEpoch || !grid.isConnected)return;
        const visibleSuggestions=suggestions.map((c,i)=>({c,i})).filter(({c})=>!ignoredMerges.has(mergeSuggestionKey(c)));
        const inRoot = ps.filter(p => p.source !== "原地");
        const inPlace = ps.filter(p => p.source === "原地");
        selected.clear();
        const card = (p: ProjectSummary) => {
            const href = p.source === "原地" ? `#/place/${encodeURIComponent(p.id!)}` : `#/project/${encodeURIComponent(p.name)}`;
            const body = p.source === "原地"
                ? `<div class="card-title" title="${esc(p.name)}">${esc(p.name)}</div>
                <div class="card-path" title="${esc(p.path)}">${esc(short(p.path))}</div>
                <div class="tags"><span class="chip info">原位置</span>${p.members ? `<span class="chip mute">${p.members} 个相关位置</span>` : ""}${p.aliases?.length ? `<span class="chip mute" title="${esc(p.aliases.map(short).join("\n"))}">链接 ${p.aliases.length}</span>` : ""}</div>
                <div class="card-meta">最近改动 ${when(p.modTime)}</div>`
                : `<div class="card-title" title="${esc(p.name)}">${esc(p.name)}</div>
                ${p.linked?`<div class="tags"><span class="chip mute">链接项目</span></div><div class="card-path" title="${esc(p.linkTarget)}">${esc(short(p.linkTarget || ''))}</div>`:''}
                ${(p.workTypes || []).length?`<div class="tags">${p.workTypes!.map(w => `<span class="tag">${esc(w)}</span>`).join("")}</div>`:''}
                <div class="card-meta">${[p.tasks?p.tasks+' 项任务':'',p.outputs?p.outputs+' 份成果':''].filter(Boolean).join(' · ')||'项目文件夹'}${p.pending ? ` · <span class="chip warn">待批准 ${p.pending}</span>` : ""}</div>
                <div class="card-meta sub">${when(p.modTime)}</div>`;
            return `<article class="card project-card">
              <a class="project-card-main" href="${href}"><svg class="project-folder" aria-hidden="true" viewBox="0 0 36 28"><path fill="currentColor" d="M2 4a3 3 0 0 1 3-3h9l4 5h13a3 3 0 0 1 3 3v15a3 3 0 0 1-3 3H5a3 3 0 0 1-3-3z"/><path d="M2 9h32" stroke="var(--surface)" stroke-width="1.5"/></svg>${body}</a>
              ${p.linked?'':`<label class="sel project-card-select" hidden><input class="checkbox checkbox-primary checkbox-sm" type="checkbox" data-sel="${esc(p.path)}" data-name="${esc(p.name)}" aria-label="选中项目 ${esc(p.name)}"></label>
              <mdui-button variant="text" class="btn btn-sm btn-ghost btn-square project-card-delete" type="button" data-del="${esc(p.path)}" aria-label="删除项目 ${esc(p.name)}" title="移到废纸篓">
                ${trashIcon}<span class="sr-only">删除项目 ${esc(p.name)}</span>
              </mdui-button>`}</article>`;
        };
        const sugHTML = visibleSuggestions.length || ignoredMerges.size ? `<details class="sec project-suggestions" id="project-suggestions">
            <summary>相关项目建议 <span class="count">${visibleSuggestions.length} 组</span></summary>
            <p class="sub">先核对是不是同一个项目，再查看工种和文件去向。查看方案不会移动文件。</p>
            <div class="suggest-list">${visibleSuggestions.map(({c, i}) => `
              <div class="suggest-card">
                <div class="suggest-head"><b title="${esc(c.name)}">${esc(c.name)}</b><span class="chip mute">${c.members} 个位置 · ${c.level==='conflict'?'不建议合并':c.level==='recommended'?'建议放在一起':'可能相关'}</span></div>
                <p class="sub" style="margin:0 0 6px">${esc((c.reasons || []).join("；"))}</p>
                <ul class="home-preview">${c.paths.map((path, j) => `<li><code title="${esc(path)}">${esc(short(path))}</code> <span class="chip mute">${esc(c.sources[j] || "")}</span></li>`).join("")}</ul>
                <div class="project-suggestion-actions">${c.level==='conflict'?'':`<mdui-button variant="text" class="btn btn-sm btn-ghost" type="button" data-merge-sug="${i}">${c.level==='recommended'?'查看整理方案':'是同一项目，查看方案'}</mdui-button>`}<mdui-button variant="text" class="btn btn-sm btn-ghost" type="button" data-ignore-merge="${i}" aria-label="不再推荐 ${esc(c.name)}">不再推荐这组</mdui-button></div>
              </div>`).join("")}</div>
            ${ignoredMerges.size?'<mdui-button variant="text" class="btn btn-sm btn-ghost" type="button" id="restore-merge-suggestions">恢复全部已忽略建议</mdui-button>':''}
          </details>` : "";
        grid.innerHTML = `
          ${st.unreadable||st.partial?`<p role="status">识别结果可能不完整：${st.unreadable?`${st.unreadable} 个位置或文件无法读取。请检查文件访问权限。`:''}${st.partial?esc((st.partialReasons||[]).join('；')):''} <a href="#/settings">查看设置</a></p>`:''}
          <p class="sub" id="merge-feedback" role="status">${esc(mergeFeedback)}</p>
          <div class="sel-bar" role="toolbar" aria-label="项目多选">
            <mdui-button variant="text" class="btn btn-sm btn-ghost" id="project-select-mode" type="button" aria-pressed="false">批量选择</mdui-button>
            <mdui-button variant="text" class="btn btn-sm btn-ghost" id="sel-all" type="button" hidden>全选可见项目</mdui-button>
            <mdui-button variant="text" class="btn btn-sm btn-ghost" id="sel-none" type="button" hidden>取消全选</mdui-button>
            <mdui-button variant="text" class="btn btn-sm btn-error" id="sel-delete" type="button" hidden>删除</mdui-button>
            <mdui-button variant="text" class="btn btn-sm btn-primary" id="merge-sel" type="button" hidden>合并到…</mdui-button>
            <span id="sel-count" class="count" aria-live="polite"></span>
          </div>
          <section class="sec">
            <h2>项目文件夹 <span class="count">${inRoot.length}</span></h2>
            <p class="sub">位于 ${esc(short(rootDir))} 的项目文件夹。</p>
            ${inRoot.length ? `<div class="grid">${inRoot.map(card).join("")}</div>` : emptyState("总文件夹里还没有整理好的项目", "下面「还在原处」的项目，打开后可一键归位搬进来。")}
          </section>
          <section class="sec">
            <div class="sec-head"><h2>其他位置的项目 <span class="count">${inPlace.length}</span></h2>
              <mdui-button variant="text" class="btn btn-sm btn-ghost" id="refresh">重新识别</mdui-button></div>
            <p class="sub">分散在电脑里的项目，文件仍在原位置。可直接查看，也可一起整理。</p>
            <details class="project-scan-details"><summary>查看识别范围与时间</summary><p class="sub">${st.generatedAt ? `上次识别：${when(st.generatedAt)}。` : ""}${st.hidden ? `${st.hidden} 个零散图片文件夹未单独列出。` : ""}在<a href="#/settings">设置</a>里修改扫描位置。</p></details>
            ${st.error ? `<div class="state error" role="alert"><b>上次识别出错</b><p>${esc(st.error)}</p></div>` : ""}
            ${inPlace.length ? `<div class="grid">${inPlace.map(card).join("")}</div>` : emptyState("没识别出还在原处的项目", "可以到设置里加上你放项目的文件夹，再点「重新识别」。")}
          </section>${sugHTML}`;
        enhance(grid);
        lastVisiblePaths = ps.filter(p => !p.linked).map(p => p.path);
        const mergeBtn = grid.querySelector<HTMLButtonElement>("#merge-sel")!;
        const selectModeBtn = grid.querySelector<HTMLButtonElement>("#project-select-mode")!;
        let selecting = false;
        const syncProjSel = () => {
            syncSelBar();
            grid.classList.toggle("project-selecting", selecting);
            grid.querySelectorAll<HTMLElement>(".project-card-select").forEach(label => label.hidden = !selecting);
            grid.querySelector<HTMLButtonElement>("#sel-all")!.hidden = !selecting || !lastVisiblePaths.length;
            mergeBtn.hidden = !selecting || selected.size < 2;
            selectModeBtn.textContent = selecting ? "完成选择" : "批量选择";
            selectModeBtn.setAttribute("aria-pressed", String(selecting));
        };
        selectModeBtn.onclick = () => { selecting = !selecting; if (!selecting) selected.clear(); syncProjSel(); };
        grid.querySelectorAll<HTMLInputElement>("input[data-sel]").forEach(cb => {
            cb.onchange = () => { if (cb.checked) selected.add(cb.dataset.sel!); else selected.delete(cb.dataset.sel!); syncProjSel(); };
        });
        syncProjSel();
        mergeBtn.hidden = true;
        grid.querySelector<HTMLButtonElement>("#sel-all")!.onclick = () => { for (const p of lastVisiblePaths) selected.add(p); syncProjSel(); };
        grid.querySelector<HTMLButtonElement>("#sel-none")!.onclick = () => { selected.clear(); syncProjSel(); };
        grid.querySelector<HTMLButtonElement>("#sel-delete")!.onclick = () => {
            confirmAndDelete([...selected], () => { selected.clear(); renderProjects(); });
        };
        grid.querySelectorAll<HTMLButtonElement>("[data-del]").forEach(b => {
            b.onclick = (ev) => {
                ev.preventDefault();
                ev.stopPropagation();
                confirmAndDelete([b.dataset.del!], () => { selected.clear(); renderProjects(); });
            };
        });
        mergeBtn.onclick = async () => {
            const paths = [...selected];
            if (paths.length < 2) return;
            const names = paths.map(p => {
                const cb = grid.querySelector<HTMLInputElement>(`input[data-sel="${CSS.escape(p)}"]`);
                return cb?.dataset.name || p.split("/").pop() || "项目";
            });
            const def = names.sort((a, b) => a.length - b.length)[0];
            startProjectGather(def, paths);
        };
        grid.querySelectorAll<HTMLButtonElement>("[data-merge-sug]").forEach(b => {
            b.onclick = () => {
                const c = suggestions[Number(b.dataset.mergeSug)];
                if (!c || c.level==='conflict') return;
                startProjectGather(c.name, c.paths);
            };
        });
        grid.querySelectorAll<HTMLButtonElement>('[data-ignore-merge]').forEach(b=>b.onclick=()=>{
            const c=suggestions[Number(b.dataset.ignoreMerge)];if(!c)return;
            ignoredMerges.add(mergeSuggestionKey(c));mergeFeedback=`已忽略「${c.name}」这组建议，可随时恢复。`;saveIgnoredMerges();void renderProjects();
        });
        const restore=grid.querySelector<HTMLButtonElement>('#restore-merge-suggestions');
        if(restore)restore.onclick=()=>{ignoredMerges.clear();mergeFeedback='已恢复全部忽略的建议。';saveIgnoredMerges();void renderProjects()};
        grid.querySelector<HTMLButtonElement>("#refresh")!.onclick = async () => {
            const b = grid.querySelector<HTMLButtonElement>("#refresh")!;
            b.disabled = true; b.textContent = "识别中……";
            try { await call("RegistryRefresh"); renderProjects(); } catch (e) { b.disabled = false; b.textContent = "重新识别"; confirmBox(`<h3>识别失败</h3><p>${esc(errText(e))}</p>`, "好", true); }
        };
    } catch (e) {
        grid.innerHTML = failed("项目列表没读出来", e);
        bindRetry(grid, renderProjects);
    }
}

function entryList(es: Entry[]): string {
    if (!es || !es.length) return '<div class="sub">（空）</div>';
    return es.map(x => `<div class="entry">
      <label class="sel"><input class="checkbox checkbox-primary checkbox-sm" type="checkbox" data-sel="${esc(x.path)}" ${selected.has(x.path) ? "checked" : ""} aria-label="选中 ${esc(x.name)}"></label>
      ${thumbSlot(x.path, x.name, x.isDir)}
      <mdui-button variant="text" class="entry-name file-name-button" type="button" data-reveal="${esc(x.path)}" title="${revealLabel()}：${esc(x.path)}" aria-label="${revealLabel()}：${esc(x.name)}">${esc(x.name)}${x.agent ? " " + agentBadge(x.agent, false) : ""}${x.status ? ` <span class="chip ${x.status === "待批准" ? "warn" : "ok"}">${esc(x.status)}</span>` : ""}</mdui-button>
      <span class="entry-actions">
        <mdui-button variant="text" class="file-trash-btn btn btn-sm btn-ghost btn-square" type="button" data-del="${esc(x.path)}" aria-label="删除 ${esc(x.name)}" title="移到废纸篓">${trashIcon}</mdui-button>
      </span></div>`).join("");
}

function bindListSelection(root: HTMLElement, onDeleted: () => void) {
    lastVisiblePaths = Array.from(root.querySelectorAll<HTMLInputElement>("input[data-sel]")).map(cb => cb.dataset.sel!);
    for (const p of [...selected]) if (!lastVisiblePaths.includes(p)) selected.delete(p);
    root.querySelectorAll<HTMLInputElement>("input[data-sel]").forEach(cb => {
        cb.onchange = () => { if (cb.checked) selected.add(cb.dataset.sel!); else selected.delete(cb.dataset.sel!); syncSelBar(); };
    });
    root.querySelectorAll<HTMLButtonElement>("[data-del]").forEach(b => {
        b.onclick = () => confirmAndDelete([b.dataset.del!], () => { selected.clear(); onDeleted(); });
    });
    syncSelBar();
}

async function renderProject(name: string) {
    const epoch = routeEpoch;
    view.classList.add('file-browser');
    view.innerHTML = loading();
    try {
        const d = await call<ProjectDetail>("Project", name);
        if (epoch !== routeEpoch) return;
        let inferred: InferredWorkType[] = [];
        try { inferred = (await call<InferredWorkType[]>("InferWorkTypes", name)) || []; } catch { /* 推断失败不挡详情 */ }
        if (epoch !== routeEpoch) return;
        const inferredOnly = inferred.filter(i => !i.alreadyInReadme);
        const wtTags = [
            ...(d.workTypes || []).map(w => {
                const hit = inferred.find(i => i.label === w);
                return `<span class="tag" title="${esc(hit?.reason || "写在说明里")}">${esc(w)}</span>`;
            }),
            ...inferredOnly.map(i => {
                const chip = i.confidence === "待你定" ? "warn" : "info";
                return `<span class="tag infer" title="${esc(i.reason)}">${esc(i.label)} <span class="chip ${chip}">${esc(i.source)}·${esc(i.confidence)}</span></span>`;
            }),
        ].join("") || '<span class="chip mute">还没认出工种</span>';
        const confirmable = inferred.filter(i => !i.alreadyInReadme && i.preConfirm && i.confidence !== "待你定");
        const cols = (d.columns || []).filter(c=>c.tasks?.length).map(c => `<section class="col" aria-label="${esc(c.workType)}">
            <div class="col-head"><b>${esc(c.workType)}</b>${c.template ? ` <span class="tag">${esc(c.template)}</span>` : ""}<span class="sub">${c.tasks?.length || 0} 个任务</span></div>
            ${(c.tasks || []).map(t => `<div class="task">
              <div class="task-head">
                <label class="sel"><input class="checkbox checkbox-primary checkbox-sm" type="checkbox" data-sel="${esc(t.path)}" ${selected.has(t.path) ? "checked" : ""} aria-label="选中任务文件夹 ${esc(t.name)}"></label>
                <mdui-button variant="text" class="file-name-button task-name-button" type="button" data-reveal="${esc(t.path)}" title="打开任务文件夹" aria-label="打开任务文件夹 ${esc(t.name)}"><b>${esc(t.name)}</b></mdui-button>
                <span class="chip ${t.status === "待批准" ? "warn" : t.status === "已交" ? "ok" : "mute"}">${esc(t.status)}</span>
                <mdui-button variant="text" class="file-trash-btn btn btn-sm btn-ghost btn-square" type="button" data-del="${esc(t.path)}" aria-label="删除任务文件夹 ${esc(t.name)}" title="移到废纸篓">${trashIcon}</mdui-button>
              </div>
              <div class="sub task-meta">${t.agent ? agentBadge(t.agent) + " · " : ""}${t.files} 个过程文件 · ${when(t.modTime)}</div>
              ${t.note ? `<div class="small">${esc(t.note)}</div>` : ""}
              ${t.outputs?.length ? `<div class="outs"><div class="sub">成品</div>${entryList(t.outputs)}</div>` : ""}
              <div class="task-actions">
                ${t.status === "待批准" ? ` <mdui-button variant="text" class="btn btn-sm btn-primary" data-approve="${esc(t.name)}">批准成品</mdui-button>` : ""}
              </div>
            </div>`).join("") || '<div class="sub pad">这个工种还没有任务。</div>'}
          </section>`).join("");
        selected.clear();
        view.innerHTML = `<header class="bar"><a href="#/projects" class="back">← 项目</a><h1 title="${esc(d.name)}">${esc(d.name)}</h1>
            <span class="chip ${d.linked?'mute':'ok'}">${d.linked?'链接项目':'已归位'}</span>
            <mdui-button variant="text" class="btn btn-sm btn-primary" id="allocate-btn" type="button" ${d.linked?'disabled':''}>按工种整理</mdui-button>
            <mdui-button variant="text" class="file-trash-btn btn btn-sm btn-ghost btn-square" id="del-project" type="button" aria-label="删除项目 ${esc(d.name)}" title="删除项目文件夹" ${d.linked?'disabled':''}>${trashIcon}</mdui-button>
            <mdui-button variant="text" class="btn btn-sm btn-ghost" data-reveal="${esc(d.path)}">${revealLabel()}</mdui-button></header>
          ${d.linked?`<p class="sub">此项目通过链接指向 ${esc(short(d.linkTarget || ''))}。需要整理或删除时，请在原位置操作。</p>`:''}
          <nav class="work-view-nav" aria-label="项目视图"><span aria-current="page">文件夹</span>${d.linked?'':`<a href="#/works/${encodeURIComponent('root:'+name)}">成果</a><a href="#/memory/${encodeURIComponent('root:'+name)}">文件记录</a><a href="#/graph/${encodeURIComponent('root:'+name)}">关系网</a>`}</nav>
          <section class="panel wt-panel" aria-label="工种">
            <div class="wt-row"><b>工种</b><div class="tags">${wtTags}</div>
              ${!d.linked && confirmable.length ? `<mdui-button variant="text" class="btn btn-sm btn-ghost" id="confirm-wt" type="button">把推断写入说明（${confirmable.length}）</mdui-button>` : ""}
            </div>
            <p class="sub">已标来自说明；推断按路径/文件名/扩展名猜的。拿不准标「待你定」，不会自动写入。</p>
          </section>
          ${selToolbarHTML()}
          ${d.folders?.length ? `<div class="side-panels">${d.folders.map(folder => `<section><h2>${esc(folder.name)}</h2>${entryList(folder.entries || [])}<mdui-button variant="text" class="btn btn-sm btn-ghost" data-reveal="${esc(folder.path)}">打开目录</mdui-button></section>`).join("")}</div>` : ""}
          <div class="cols">${cols || (d.folders?.length ? "" : emptyState("还没有任务", "可以先按工种整理项目文件。"))}</div>
          <div class="side-panels">
            <section><h2>资料</h2>${entryList(d.materials)}${d.sensitive ? `<div class="sub">敏感资料 ${d.sensitive} 个（不在这里列出）</div>` : ""}</section>
            <section><h2>代码</h2>${entryList(d.code)}</section>
            ${d.loose?.length ? `<section><h2>项目里没归类的文件</h2>${entryList(d.loose)}</section>` : ""}
            <section><h2>说明</h2><pre class="readme">${esc(d.readme || "（没有 00-说明.md）")}</pre></section>
          </div>`;
        enhance(view);
        if (d.linked) view.querySelectorAll<HTMLElement>('.sel-bar,.sel,[data-del],[data-approve]').forEach(el=>{el.hidden=true;});
        bindSelToolbar(() => renderProject(name));
        bindListSelection(view, () => renderProject(name));
        view.querySelector<HTMLButtonElement>("#del-project")!.onclick = () =>
            confirmAndDelete([d.path], () => { location.hash = "#/projects"; });
        view.querySelector<HTMLButtonElement>("#allocate-btn")!.onclick = () => { location.hash = "#/organize/" + encodeURIComponent("root:" + name); };
        const cbtn = view.querySelector<HTMLButtonElement>("#confirm-wt");
        if (cbtn) cbtn.onclick = async () => {
            const labels = confirmable.map(i => i.label);
            if (!await confirmBox(`<h3>写入项目说明？</h3><p>只在 <code>00-说明.md</code> 末尾追加，不改原文：</p><ul>${labels.map(l => `<li>${esc(l)}</li>`).join("")}</ul>`, "写入")) return;
            try {
                await call("ConfirmWorkTypes", name, labels);
                await confirmBox(`<h3>已写入</h3><p>${labels.map(esc).join("、")}</p>`, "好", true);
                renderProject(name);
            } catch (e) { await confirmBox(`<h3>没写成</h3><p>${esc(errText(e))}</p>`, "好", true); }
        };
        view.querySelectorAll<HTMLButtonElement>("[data-approve]").forEach(b => b.onclick = async () => {
            const task = b.dataset.approve!;
            if (!await confirmBox(`<h3>批准「${esc(task)}」的成品？</h3><p>状态改为「已交」，索引和任务说明里各追加一行记录。文件本身不动。</p>`, "批准")) return;
            try {
                const r = await call<any>("Approve", d.name, task);
                const changed = r.changed?.length ? `<p class="warn">其中 ${r.changed.length} 个入库后内容变过，已按现在的内容登记。</p>` : "";
                await confirmBox(`<h3>已批准 ${r.files?.length || 0} 个成品</h3>${changed}`, "好", true);
                renderProject(name);
            } catch (e) { await confirmBox(`<h3>没批准成</h3><p>${esc(errText(e))}</p>`, "好", true); }
        });
    } catch (e) {
        if (epoch !== routeEpoch) return;
        view.innerHTML = failed(`打不开项目「${name}」`, e);
        bindRetry(view, () => renderProject(name));
    }
}


async function renderPlace(id: string) {
    const epoch = routeEpoch;
    view.classList.add('file-browser');
    view.innerHTML = loading();
    try {
        const d = await call<PlaceDetail>("PlaceProject", id);
        if (epoch !== routeEpoch) return;
        const p = d.project;
        const members = (p.members || []).slice(1);
        const byKind = new Map<string, Member[]>();
        for (const m of members) byKind.set(m.kind, [...(byKind.get(m.kind) || []), m]);
        selected.clear();
        view.innerHTML = `<header class="bar"><a href="#/projects" class="back">← 项目</a><h1 title="${esc(p.name)}">${esc(p.name)}</h1>
            <span class="chip info">还在原处，未归位</span>
            <mdui-button variant="text" class="btn btn-sm btn-primary" id="home-project">整理项目</mdui-button>
            <mdui-button variant="text" class="file-trash-btn btn btn-sm btn-ghost btn-square" id="del-project" type="button" aria-label="删除项目 ${esc(p.name)}" title="删除项目文件夹">${trashIcon}</mdui-button>
            <mdui-button variant="text" class="btn btn-sm btn-ghost" data-reveal="${esc(p.path)}">${revealLabel()}</mdui-button></header>
          <nav class="work-view-nav" aria-label="项目视图"><span aria-current="page">文件夹</span><a href="#/works/${encodeURIComponent('place:'+id)}">成果</a><a href="#/memory/${encodeURIComponent('place:'+id)}">文件记录</a><a href="#/graph/${encodeURIComponent('place:'+id)}">关系网</a></nav>
          <p class="lead">主版本在 <code title="${esc(p.path)}">${esc(short(p.path))}</code>${p.branch ? `，分支 ${esc(p.branch)}` : ""}。选择「整理项目」可以确认工种模板、补充来源，并预览文件去向。</p>
          ${selToolbarHTML()}
          <div class="place">
            <section class="panel"><h2>最近改过的文件</h2>
              <div class="list">${d.recent?.length ? d.recent.map(itemRow).join("") : emptyState("没找到文件")}</div></section>
            <aside class="side-panels one">
              <section><h2>副本和相关文件 <span class="count">${members.length}</span></h2>
                ${members.length ? [...byKind].map(([k, ms]) => `<div class="kind">${esc(k)} · ${ms.length}</div>${entryList(ms.map(m=>({name:m.path.split('/').pop()!,path:m.path,isDir:m.isDir,size:0,modTime:'',status:'',agent:''})))}`).join("") : '<div class="sub">没有副本。</div>'}
                ${p.aliases?.length ? `<div class="kind">指向它的链接</div>${p.aliases.map(a => `<div class="entry alias"><span class="entry-name" title="${esc(a)}">${esc(short(a))}</span></div>`).join("")}` : ""}
                ${p.reasons?.length ? `<p class="sub">归组依据：${esc(p.reasons.join("；"))}</p>` : ""}
              </section>
              <section><h2>${esc(d.readmeName || "说明")}</h2><pre class="readme">${esc(d.readme || "（没有 README / AGENTS.md）")}</pre></section>
            </aside>
          </div>`;
        enhance(view);
        bindSelToolbar(() => renderPlace(id));
        bindListSelection(view, () => renderPlace(id));
        view.querySelector<HTMLButtonElement>("#home-project")!.onclick = () =>
            { location.hash = "#/organize/" + encodeURIComponent("place:" + id); };
        view.querySelector<HTMLButtonElement>("#del-project")!.onclick = () =>
            confirmAndDelete([p.path], () => { location.hash = "#/projects"; });
    } catch (e) {
        if (epoch !== routeEpoch) return;
        view.innerHTML = failed("打不开这个项目", e);
        bindRetry(view, () => renderPlace(id));
    }
}


// ---------- 一键归位 ----------
function homePreviewHTML(sum: HomeSummary): string {
    const rows = (sum.preview || []).map(it => it.skip
        ? `<li class="sub">跳过 <code>${esc(short(it.source))}</code>：${esc(it.reason || "")}</li>`
        : `<li><b>${esc(it.action)}</b> <code title="${esc(it.source)}">${esc(short(it.source))}</code>
            <div class="sub">→ ${esc(short(it.target))}${it.crossDevice ? "（跨磁盘搬迁）" : ""}${it.whole ? " · 整项目" : ""}</div></li>`).join("");
    const nGo = (sum.preview || []).filter(x => !x.skip).length;
    const nSkip = (sum.preview || []).filter(x => x.skip).length;
    return `<h3>预览归位</h3>
      <p>将处理 <b>${nGo}</b> 条${nSkip ? `，跳过 ${nSkip} 条` : ""}。文件会搬到目标位置，完成后可以撤销。</p>
      ${sum.crossHint ? `<p class="warn">${esc(sum.crossHint)}</p>` : ""}
      <ul class="home-preview">${rows || "<li class=\"sub\">没有可做的条目</li>"}</ul>`;
}

function homeResultHTML(sum: HomeSummary): string {
    return `<h3>归位完成</h3>
      <p>搬走 ${sum.moved} · 复制 ${sum.copied} · 跳过 ${sum.skipped} · 失败 ${sum.failed}</p>
      ${sum.crossHint ? `<p class="warn">${esc(sum.crossHint)}</p>` : ""}
      ${sum.undoPath ? `<p class="sub">撤销清单：<code title="${esc(sum.undoPath)}">${esc(short(sum.undoPath))}</code></p>` : ""}
      ${(sum.items || []).filter(i => i.action === "失败").map(i => `<p class="err">${esc(short(i.source))}：${esc(i.error || "")}</p>`).join("")}`;
}

async function confirmAndHome(previewMethod: string, applyMethod: string, arg: unknown, after?: () => void) {
    try {
        const sum = await call<HomeSummary>(previewMethod, arg);
        const nGo = (sum.preview || []).filter(x => !x.skip).length;
        if (!nGo) {
            await confirmBox(homePreviewHTML(sum) + `<p class="sub">没有可归位的条目。</p>`, "好", true);
            return;
        }
        if (!await confirmBox(homePreviewHTML(sum), "确认归位")) return;
        const done = await call<HomeSummary>(applyMethod, arg);
        await confirmBox(homeResultHTML(done), "好", true);
        after?.();
    } catch (e) {
        await confirmBox(`<h3>归位没做成</h3><p>${esc(errText(e))}</p>`, "好", true);
    }
}

// ---------- 设置 ----------
async function renderSettings() {
    view.innerHTML = `<header class="bar"><h1>设置</h1></header><div id="set">${loading()}</div>`;
    const box = document.getElementById("set")!;
    try {
        const s = await call<any>("Settings");
        box.innerHTML = `
          <section class="panel form"><h2>模型与账号</h2><p class="sub">在模型接入中心配置服务商、协议、API Key 和订阅账号。</p><mdui-button class="btn" href="#/models">打开模型接入中心</mdui-button></section>
          <section class="panel form"><h2>开始方式</h2><p class="sub">整理现有文件，或用工种模板建立新项目。</p><mdui-button class="btn" href="#/welcome">重新选择</mdui-button></section>
          <section class="panel form" id="background-settings"><h2>后台管理</h2><p>读取设置中…</p></section>
          <section class="panel form" id="auto-organize-settings"><h2>自动整理项目</h2><p>读取设置中…</p></section>
          <section class="panel form"><h2>文件访问权限</h2><p class="sub">检查桌面、文稿、下载、Projects 和主目录。系统可能询问权限，macOS 通常会记住授权，但权限可能改变。其他扫描目录和 AI 会话目录需分别配置。</p><mdui-button id="check-access" class="btn" variant="text">检查并授权</mdui-button><p id="check-access-result" role="status" aria-live="polite">尚未检查</p></section>
          ${osName === 'darwin' ? `<section class="panel form" id="install-panel">
            <h2>安装位置（保持授权）</h2>
            <p class="sub">使用固定安装位置和签名有助于 macOS 记住授权。请从 <code>~/Applications/WorkShelf.app</code> 打开；系统权限可能需要重新确认。</p>
            <p id="install-status" class="sub">读取中……</p>
            <div class="actions"><mdui-button variant="text" class="btn btn-sm btn-primary" id="install-app" type="button">安装到应用程序文件夹</mdui-button></div>
          </section>` : ''}
          <section class="panel form">
            <h2>总文件夹</h2>
            <p class="sub">所有整理好的项目都放在这里。改路径不会搬动已有文件，只换「以后往哪收」。文件夹必须已经存在，且不能和现在的位置互相套在一起。</p>
            <label for="root">总文件夹路径</label>
            <div class="row-input"><input class="input input-sm" id="root" type="text" spellcheck="false" value="${esc(short(s.root))}" aria-describedby="root-hint"><mdui-button variant="text" class="btn btn-sm btn-primary" id="save-root">保存路径</mdui-button></div>
            <p id="root-hint" class="sub">可用 ~ 开头，例如 ~/Work 或 ~/Documents/WorkShelf。保存后项目列表和原地登记会按新位置读。</p>
            <p id="root-msg" role="status" aria-live="polite"></p>
            <dl class="facts"><dt>当前</dt><dd title="${esc(s.root)}">${esc(short(s.root))}</dd>
              <dt>原地登记表</dt><dd title="${esc(s.registryDir)}">${esc(short(s.registryDir))}</dd>
              <dt>配置文件</dt><dd title="${esc(s.configPath)}">${esc(short(s.configPath))}${s.configExists ? "" : "（保存时新建）"}</dd></dl>
          </section>
          <section class="panel form">
            <h2>识别项目时看哪些地方</h2>
            <p class="sub">每行一个文件夹，可以用 ~ 开头。只读扫描，不会搬动或修改任何文件。${s.configExists ? "" : "现在用的是默认范围（还没有配置文件）。"}</p>
            <label for="paths">扫描位置</label>
            <textarea class="textarea textarea-sm" id="paths" rows="7" spellcheck="false">${esc((s.scanPaths || []).map(short).join("\n"))}</textarea>
            <label for="depth">主目录本身往下看几层</label>
            <select class="select select-sm" id="depth">${[0, 1, 2, 3].map(n => `<option value="${n}" ${n === s.homeDepth ? "selected" : ""}>${n} 层</option>`).join("")}</select>
            <div class="actions"><mdui-button variant="text" class="btn btn-sm btn-primary" id="save">保存并重新识别</mdui-button><mdui-button variant="text" class="btn btn-sm btn-ghost" id="reset">恢复默认</mdui-button><span id="msg" role="status" aria-live="polite"></span></div>
          </section>`;
        enhance(box);
        const refreshAccessStatus=async()=>{
            const button=box.querySelector<HTMLButtonElement>('#check-access')!,result=box.querySelector<HTMLElement>('#check-access-result')!;
            button.disabled=true;result.textContent='正在检查，系统可能会询问访问权限……';
            try{const folders=await checkAccess(call);if(result.isConnected)result.textContent=folders.map(f=>`${f.name}：${f.status}`).join('　')+(folders.some(f=>f.status==='无法访问')?(osName==='darwin'?'。请检查 macOS「系统设置 → 隐私与安全性 → 文件与文件夹」中的授权。':'。请检查系统中的文件夹权限。'):'');}
            catch(e){if(result.isConnected)result.textContent=errText(e);}
            finally{if(button.isConnected)button.disabled=false;}
        };
        box.querySelector<HTMLButtonElement>('#check-access')!.onclick=refreshAccessStatus;
        if(accessWasChecked()) void refreshAccessStatus();
        mountBackgroundSettings(box.querySelector<HTMLElement>('#background-settings')!,call,esc);
        mountAutoOrganize(box.querySelector<HTMLElement>('#auto-organize-settings')!,call,esc,(html,ok)=>confirmBox(html,ok));
        if (osName === 'darwin') void (async () => {
            const stEl = box.querySelector<HTMLElement>("#install-status")!;
            const btn = box.querySelector<HTMLButtonElement>("#install-app")!;
            try {
                const st = await call<InstallStatus>("InstallStatus");
                stEl.innerHTML = st.inApplications
                    ? `正在从固定位置运行：<code title="${esc(st.runningPath)}">${esc(short(st.runningPath) || st.runningPath)}</code>`
                    : `当前：<code title="${esc(st.runningPath)}">${esc(short(st.runningPath) || st.runningPath || "（开发/临时包）")}</code><br>目标：<code>${esc(short(st.preferredPath) || st.preferredPath)}</code>`;
                btn.onclick = async () => {
                    btn.disabled = true; btn.textContent = "安装中……";
                    try {
                        const done = await call<InstallStatus>("InstallToApplications");
                        stEl.textContent = "已装到 " + done.preferredPath + "。请退出后从应用程序里打开。";
                        await refreshInstallBanner();
                        await confirmBox(`<h3>已安装</h3><p><code>${esc(done.preferredPath)}</code></p><p>请退出当前窗口，从「应用程序」里的 序屿 再开。系统如再次询问权限，请按需要允许。</p>`, "好", true);
                    } catch (e) {
                        btn.disabled = false; btn.textContent = "安装到应用程序文件夹";
                        stEl.className = "err"; stEl.textContent = errText(e);
                    }
                };
            } catch (e) { stEl.className = "err"; stEl.textContent = errText(e); }
        })();
        const rootMsg = box.querySelector<HTMLElement>("#root-msg")!;
        box.querySelector<HTMLButtonElement>("#save-root")!.onclick = async () => {
            rootMsg.className = ""; rootMsg.textContent = "保存中……";
            try {
                const path = box.querySelector<HTMLInputElement>("#root")!.value.trim();
                const st = await call<any>("SetRoot", path);
                rootDir = st.root;
                home = st.root.replace(/[\/\\][^\/\\]+$/, "");
                rootMsg.textContent = `已改成 ${short(st.root)}。正在按新位置重新识别……`;
                const reg = await call<RegStatus>("RegistryRefresh");
                rootMsg.textContent = `已改成 ${short(st.root)}。识别出 ${reg.projects} 个还在原处的项目。`;
                // 刷新展示的登记路径
                renderSettings();
            } catch (e) { rootMsg.className = "err"; rootMsg.textContent = errText(e); }
        };
        const ta = box.querySelector<HTMLTextAreaElement>("#paths")!;
        const msg = box.querySelector<HTMLElement>("#msg")!;
        box.querySelector<HTMLButtonElement>("#reset")!.onclick = () => { ta.value = (s.defaults || []).map(short).join("\n"); msg.textContent = "已填入默认范围，点「保存」才生效。"; };
        box.querySelector<HTMLButtonElement>("#save")!.onclick = async () => {
            msg.className = ""; msg.textContent = "保存中……";
            try {
                const depth = Number(box.querySelector<HTMLSelectElement>("#depth")!.value);
                await call("SaveSettings", ta.value.split("\n").map(x => x.trim()).filter(Boolean), depth);
                msg.textContent = "已保存，正在重新识别……";
                const st = await call<RegStatus>("RegistryRefresh");
                msg.textContent = `已保存。识别出 ${st.projects} 个还在原处的项目。`;
            } catch (e) { msg.className = "err"; msg.textContent = errText(e); }
        };
    } catch (e) {
        box.innerHTML = failed("设置没读出来", e);
        bindRetry(box, renderSettings);
    }
}

// ---------- 确认框（页面内；Esc 取消，焦点留在框里） ----------
function confirmBox(html: string, ok: string, onlyOk = false, danger = false, dismissLabel = "取消"): Promise<boolean> {
    return new Promise(res => {
        const prev = document.activeElement as HTMLElement | null;
        const dialog = document.createElement("mdui-dialog") as HTMLElement & {open:boolean;closeOnEsc:boolean};
        dialog.className = "shelf-dialog";
        dialog.closeOnEsc = true;
        dialog.innerHTML = `<div class="modal-box">${html}</div><div class="modal-btns" slot="action">${confirmationActions(ok,onlyOk,danger)}</div>`;
        const title=dialog.querySelector<HTMLElement>('h1,h2,h3');
        if(title){title.slot='headline';dialog.prepend(title)}
        else dialog.setAttribute('headline',ok);
        const dismiss=dialog.querySelector<HTMLElement>('[data-x="0"]');
        if(dismiss)dismiss.textContent=dismissLabel;
        const background=Array.from(document.body.children).filter((n):n is HTMLElement=>n instanceof HTMLElement);
        const alreadyInert=new Set(background.filter(n=>n.hasAttribute('inert')));
        background.forEach(n=>n.setAttribute('inert',''));
        let answer=false,settled=false;
        const finish=()=>{
            if(settled)return;settled=true;
            dialog.remove();
            background.forEach(n=>{if(!alreadyInert.has(n))n.removeAttribute('inert')});
            if(prev?.isConnected)prev.focus();
            res(answer);
        };
        dialog.addEventListener('closed',e=>{if(e.target===dialog)finish()});
        dialog.querySelectorAll<HTMLElement>('[data-x]').forEach(b=>b.onclick=()=>{answer=b.dataset.x==='1';dialog.open=false});
        dialog.addEventListener('opened',()=>dialog.querySelector<HTMLElement>(onlyOk?'[data-x="1"]':'[data-x="0"]')?.focus(),{once:true});
        document.body.appendChild(dialog);
        dialog.open=true;
    });
}

async function renderCleanup() {
    view.innerHTML = `
      <header class="bar"><h1>推荐清理</h1></header>
      <p class="lead">根据登记表和桌面/下载上的名字，猜哪些可能是副本、空文件夹或旧压缩包。<b>不会自动删</b>；「推荐」可预勾，「待你定」请你自己勾。删除仍走废纸篓预览确认。</p>
      <div class="sel-bar" role="toolbar" aria-label="清理操作">
        <mdui-button variant="text" class="btn btn-sm btn-ghost" id="cu-refresh" type="button">重新扫描</mdui-button>
        <mdui-button variant="text" class="btn btn-sm btn-ghost" id="cu-all-rec" type="button">勾选全部推荐</mdui-button>
        <mdui-button variant="text" class="btn btn-sm btn-ghost" id="cu-none" type="button" hidden>取消勾选</mdui-button>
        <mdui-button variant="text" class="btn btn-sm btn-error" id="cu-delete" type="button" hidden>删除所选</mdui-button>
        <span id="cu-count" class="count" aria-live="polite"></span>
      </div>
      <div id="cu-list">${loading("正在找可清理的内容")}</div>
      <details class="probe-box">
        <summary>对某个项目做轻量扫描（不宣称死代码分析）</summary>
        <p class="sub">只按目录名/临时文件名猜「可能用不到」的东西，结果一律进「待你定」、不预勾。</p>
        <div class="probe-row">
          <input id="cu-probe-path" class="search input input-sm" type="text" placeholder="项目绝对路径，例如 ~/Work/WorkShelf/代码/WorkShelf" style="max-width:none;flex:1">
          <mdui-button variant="text" class="btn btn-sm btn-ghost" id="cu-probe" type="button">扫描</mdui-button>
        </div>
        <div id="cu-probe-out" class="sub"></div>
      </details>`;
    const list = document.getElementById("cu-list")!;
    const picked = new Set<string>();
    let suggestions: CleanupSuggestion[] = [];

    const sync = () => {
        const n = picked.size;
        const del = document.getElementById("cu-delete") as HTMLButtonElement;
        const none = document.getElementById("cu-none") as HTMLButtonElement;
        const count = document.getElementById("cu-count")!;
        del.hidden = n === 0;
        none.hidden = n === 0;
        del.textContent = `删除所选（${n}）`;
        count.textContent = n ? `已选 ${n} / ${suggestions.length}` : `${suggestions.length} 条建议`;
        list.querySelectorAll<HTMLInputElement>("input[data-cu]").forEach(cb => { cb.checked = picked.has(cb.dataset.cu!); });
    };

    const paint = (items: CleanupSuggestion[]) => {
        suggestions = items;
        picked.clear();
        for (const s of items) if (s.preCheck && s.confidence === "推荐") picked.add(s.path);
        if (!items.length) {
            list.innerHTML = emptyState("暂时没找到明显可清理的东西", "这是好事。也可以到「项目」里打开某个项目，用下面的轻量扫描再看看。");
            sync();
            return;
        }
        const order = ["重复与副本", "多余压缩包", "空文件夹", "旧会话与 worktree", "依赖与缓存", "可能用不到的文件", "待你定"];
        const groups = new Map<string, CleanupSuggestion[]>();
        for (const s of items) {
            const k = s.category || "待你定";
            groups.set(k, [...(groups.get(k) || []), s]);
        }
        const keys = [...order.filter(k => groups.has(k)), ...[...groups.keys()].filter(k => !order.includes(k))];
        list.innerHTML = keys.map(k => {
            const arr = groups.get(k)!;
            return `<section class="cu-sec">
              <h2>${esc(k)} <span class="count">${arr.length}</span></h2>
              <div class="cu-list">${arr.map(s => `
                <div class="cu-row">
                  <label class="sel"><input class="checkbox checkbox-primary checkbox-sm" type="checkbox" data-cu="${esc(s.path)}" ${picked.has(s.path) ? "checked" : ""} aria-label="选中 ${esc(s.name)}"></label>
                  <div class="cu-main">
                    <div class="row-name" title="${esc(s.path)}">${esc(s.name)} ${s.isDir ? '<span class="chip mute">文件夹</span>' : ""}</div>
                    <div class="sub">${esc(s.reason)}</div>
                    <div class="sub" title="${esc(s.evidence)}">证据：${esc(s.evidence)}${s.project ? ` · 项目 ${esc(s.project)}` : ""} · ${s.fileCount || 0} 个文件 · ${size(s.byteSize || 0)}</div>
                  </div>
                  <div class="cu-side">
                    <span class="chip ${s.confidence === "推荐" ? "warn" : "mute"}">${esc(s.confidence)}</span>
                    <mdui-button variant="text" class="btn btn-sm btn-ghost" data-reveal="${esc(s.path)}" type="button">${revealLabel()}</mdui-button>
                  </div>
                </div>`).join("")}</div>
            </section>`;
        }).join("");
        enhance(list);
        list.querySelectorAll<HTMLInputElement>("input[data-cu]").forEach(cb => {
            cb.onchange = () => { if (cb.checked) picked.add(cb.dataset.cu!); else picked.delete(cb.dataset.cu!); sync(); };
        });
        sync();
    };

    const reload = async () => {
        list.innerHTML = loading("正在找可清理的内容");
        try {
            paint((await call<CleanupSuggestion[]>("CleanupSuggest")) || []);
        } catch (e) {
            list.innerHTML = failed("推荐清理没读出来", e);
            bindRetry(list, reload);
        }
    };

    document.getElementById("cu-refresh")!.onclick = () => reload();
    document.getElementById("cu-all-rec")!.onclick = () => {
        for (const s of suggestions) if (s.confidence === "推荐") picked.add(s.path);
        sync();
    };
    document.getElementById("cu-none")!.onclick = () => { picked.clear(); sync(); };
    document.getElementById("cu-delete")!.onclick = () => {
        confirmAndDelete([...picked], () => { reload(); });
    };
    document.getElementById("cu-probe")!.onclick = async () => {
        const raw = (document.getElementById("cu-probe-path") as HTMLInputElement).value.trim();
        const out = document.getElementById("cu-probe-out")!;
        if (!raw) { out.textContent = "先填一个项目路径。"; return; }
        let path = raw;
        if (path.startsWith("~/")) path = (home || "") + path.slice(1);
        out.textContent = "扫描中…";
        try {
            const extra = (await call<CleanupSuggestion[]>("CleanupProbeProject", path)) || [];
            if (!extra.length) { out.textContent = "这棵目录下没扫到临时文件或常见缓存目录名。"; return; }
            // merge into list as 待你定
            const map = new Map(suggestions.map(s => [s.path, s]));
            for (const s of extra) map.set(s.path, s);
            paint([...map.values()]);
            out.textContent = `又找到 ${extra.length} 条（均为待你定，未预勾）。`;
        } catch (e) {
            out.textContent = errText(e);
        }
    };
    enhance(view);
    await reload();
}


const GRAPH_TAB_KEY = "workshelf.graphTab";
type GraphTab = "projects" | "files" | "works" | "memory";
type GNode = {id: string; kind: string; name: string; path?: string; source?: string; workTypes?: string[]; memberCount?: number; attachCount?: number; projectId?: string; project?: string; size?: number; extra?: string; agent?:string; workId?:string; versionId?:string; versionCount?:number; state?:string; usable?:boolean; recordedUsable?:boolean; reference?:boolean};
type GEdge = {id: string; type: string; from: string; to: string; reason: string; weight: number; confidence: string; directed?:boolean};
type WorkGraph = {project:string;projectKey:string;workId?:string;workTitle?:string;revision:number;nodes:GNode[];edges:GEdge[];total:number;offset:number;limit:number;note:string;skipped?:string};
const graphVersionState=(n:GNode)=>n.reference?'关联版本 · 内容未核对':n.usable?'当前可用版':(n.recordedUsable?'曾选为可用 · ':'')+(versionStates[n.state||'']||'内容未核对');
type ProjectGraph = {nodes: GNode[]; edges: GEdge[]; skipped?: string};
type FileGraph = {project: string; projectKey?:string; projectId?: string; nodes: GNode[]; edges: GEdge[]; skipped?: string; note?: string; attachCount?: number; attachShown?: number; attachHidden?: number};

// 边类型：名称、说明、理想长度（越像「同一份东西」越短）
const edgeOrder = ["exported_from", "work_version", "source_record", "dup", "copyname", "same_dir", "activity", "parent", "member", "git", "copykey", "attach"];
const edgeLabels: Record<string, string> = {
    memory_member: "项目记忆",
    memory_event: "记录了变化",
    exported_from: "导出为 →",
    work_version: "作品包含",
    source_record: "来源记录",
    member: "登记成员",
    copykey: "同名合并建议",
    git: "同一 git 仓库",
    attach: "挂靠",
    same_dir: "同任务/成品目录",
    dup: "内容相同",
    activity: "活动关联",
    copyname: "副本命名",
    parent: "目录包含",
};
const edgeTips: Record<string, string> = {
    memory_member: "本项目保存了该文件的历史记录，不证明当前位置可访问",
    memory_event: "同一文件身份的观察记录，不推断操作者或因果关系",
    exported_from: "已登记由起点版本导出为终点版本；不代表已验证导出过程",
    work_version: "登记在同一作品下，不表示版本先后或演变",
    source_record: "索引、活动或侧边标记记录过这个工具；尚未核验当前内容作者",
    member: "登记在同一项目组：主版本和副本/worktree 相连，副本之间也互相连",
    copykey: "名字去掉后缀后一样，可能该合并",
    git: "指向同一个 git 仓库",
    attach: "文件归到这个项目，但还放在项目外",
    same_dir: "放在同一个任务/成品目录里，两两互相连（不是一个中心）",
    dup: "内容一模一样（sha256 相同）",
    activity: "同一任务、智能体或工种留下的记录",
    copyname: "去掉「副本 / v2 / 终版」等后缀后同名",
    parent: "目录和其中的文件",
};
const edgeLen: Record<string, number> = {dup: 120, copyname: 140, same_dir: 170, activity: 200, parent: 150, member: 170, git: 170, copykey: 200, attach: 180};
const edgeSort = (t: string) => { const i = edgeOrder.indexOf(t); return i < 0 ? 99 : i; };

function loadGraphTab(): GraphTab {
    try {
        const v = localStorage.getItem(GRAPH_TAB_KEY);
        if (v === "files" || v === "projects" || v === "works" || v === "memory") return v;
    } catch {}
    return "projects";
}
function saveGraphTab(t: GraphTab) {
    try { localStorage.setItem(GRAPH_TAB_KEY, t); } catch {}
}

const GRAPH_EXPAND_KEY = "workshelf.graphExpandAttach";
function loadGraphExpandAttach(): boolean {
    try {
        const v = localStorage.getItem(GRAPH_EXPAND_KEY);
        if (v === "0" || v === "false") return false;
        if (v === "1" || v === "true") return true;
    } catch {}
    return true; // 默认展开：桌面/下载等项目外相关文件也进图
}
function saveGraphExpandAttach(on: boolean) {
    try { localStorage.setItem(GRAPH_EXPAND_KEY, on ? "1" : "0"); } catch {}
}

/** 下拉稳定键：总文件夹用名；原地用 place:<id> */
function projectSelectKey(p: {name: string; source: string; id?: string; path?: string}): string {
    if (p.source === "原地" && p.id) return "place:" + p.id;
    return "name:" + p.name;
}
function parseProjectSelectKey(key: string): {name: string; id: string} {
    if (key.startsWith("place:")) return {name: "", id: key.slice(6)};
    if (key.startsWith("name:")) return {name: key.slice(5), id: ""};
    return {name: key, id: ""}; // 兼容旧值
}

const LABEL_PX = 12, LINE_H = 15, LABEL_MAX_W = 172;
function textW(s: string): number {
    let w = 0;
    for (const ch of s) {
        const c = ch.codePointAt(0) || 0;
        w += c >= 0x2e80 ? LABEL_PX * 1.02 : c === 32 ? LABEL_PX * 0.32 : LABEL_PX * 0.62;
    }
    return w;
}
// 名字完整显示：按宽度换行，不截断、不加省略号；优先在分隔符后断开
function wrapLabel(s: string): string[] {
    const lines: string[] = [];
    let cur = "";
    for (const ch of s) {
        if (cur && textW(cur + ch) > LABEL_MAX_W) {
            const cut = Math.max(cur.lastIndexOf("-"), cur.lastIndexOf("_"), cur.lastIndexOf(" "), cur.lastIndexOf("."));
            if (cut > cur.length * 0.5 && cut < cur.length - 1) {
                lines.push(cur.slice(0, cut + 1));
                cur = cur.slice(cut + 1) + ch;
            } else {
                lines.push(cur);
                cur = ch;
            }
        } else cur += ch;
    }
    if (cur) lines.push(cur);
    return lines;
}

type SimNode = GNode & {x: number; y: number; vx: number; vy: number; r: number; lines: string[]; lw: number; lh: number; deg: number};
function nodeBox(n: SimNode) {
    const half = Math.max(n.r, n.lh / 2) + 5;
    return {l: n.x - n.r - 6, r: n.x + n.r + 8 + n.lw + 6, t: n.y - half, b: n.y + half};
}
function collide(sim: SimNode[], passes: number) {
    for (let p = 0; p < passes; p++) {
        let moved = false;
        for (let i = 0; i < sim.length; i++) {
            const A = nodeBox(sim[i]);
            for (let j = i + 1; j < sim.length; j++) {
                const B = nodeBox(sim[j]);
                const ox = Math.min(A.r, B.r) - Math.max(A.l, B.l);
                const oy = Math.min(A.b, B.b) - Math.max(A.t, B.t);
                if (ox <= 0 || oy <= 0) continue;
                moved = true;
                const ca = (A.l + A.r) / 2, cb = (B.l + B.r) / 2;
                if (oy < ox) {
                    const s = (sim[i].y <= sim[j].y ? -1 : 1) * (oy / 2 + 0.5);
                    sim[i].y += s; sim[j].y -= s;
                } else {
                    const s = (ca <= cb ? -1 : 1) * (ox / 2 + 0.5);
                    sim[i].x += s; sim[j].x -= s;
                }
                A.l = sim[i].x - sim[i].r - 6; A.r = sim[i].x + sim[i].r + 8 + sim[i].lw + 6;
                A.t = sim[i].y - Math.max(sim[i].r, sim[i].lh / 2) - 5; A.b = sim[i].y + Math.max(sim[i].r, sim[i].lh / 2) + 5;
            }
        }
        if (!moved) break;
    }
}

// 网状力导向：节点（含标签占位）碰撞、按关系类型分理想边长、弱中心引力；
// 初始按连通块分区摆放，同一团自然聚在一起，不做任何中心辐射摆放。
function layoutMesh(nodes: GNode[], edges: GEdge[], prev: Map<string, {x: number; y: number}>): SimNode[] {
    const deg = new Map<string, number>();
    for (const e of edges) { deg.set(e.from, (deg.get(e.from) || 0) + 1); deg.set(e.to, (deg.get(e.to) || 0) + 1); }
    // 连通块
    const parent = new Map<string, string>(nodes.map(n => [n.id, n.id]));
    const find = (x: string): string => { let r = x; while (parent.get(r) !== r) r = parent.get(r)!; parent.set(x, r); return r; };
    for (const e of edges) { if (parent.has(e.from) && parent.has(e.to)) parent.set(find(e.from), find(e.to)); }
    const comps = new Map<string, GNode[]>();
    for (const n of nodes) { const r = find(n.id); if (!comps.has(r)) comps.set(r, []); comps.get(r)!.push(n); }
    const compList = [...comps.values()].sort((a, b) => b.length - a.length);
    const sim: SimNode[] = [];
    const cols = Math.ceil(Math.sqrt(compList.length));
    compList.forEach((comp, ci) => {
        const cx = (ci % cols) * 420, cy = Math.floor(ci / cols) * 300;
        comp.forEach((n, i) => {
            const lines = wrapLabel(n.name);
            if(n.kind==='work')lines.push(`${n.versionCount||0} 个版本${n.usable?' · 有可用版':''}`);
            if(n.kind==='version')lines.push(n.usable?'当前可用版':n.reference?'关联版本 · 未核对':n.state==='available'?(versionRoles[n.source||'']||'内容可读取'):(versionStates[n.state||'']||'内容尚未核对'));
            const d = deg.get(n.id) || 0;
            const base = n.kind === "project" ? 13 : n.kind === "folder" ? 10 : 8;
            const p = prev.get(n.id);
            const a = (i / Math.max(comp.length, 1)) * Math.PI * 2 + ci;
            const rr = 40 + comp.length * 12;
            sim.push({...n, lines, lw: Math.max(...lines.map(textW)), lh: lines.length * LINE_H, deg: d,
                r: base + Math.min(d, 8) * 0.9,
                x: p ? p.x : cx + Math.cos(a) * rr, y: p ? p.y : cy + Math.sin(a) * rr, vx: 0, vy: 0});
        });
    });
    const n = sim.length;
    if (!n) return sim;
    const idx = new Map(sim.map((s, i) => [s.id, i]));
    // 同一对节点的多条关系只算一次弹簧，免得被拉成一坨
    const pairMul = new Map<string, number>();
    const pk = (a: string, b: string) => a < b ? a + "|" + b : b + "|" + a;
    for (const e of edges) pairMul.set(pk(e.from, e.to), (pairMul.get(pk(e.from, e.to)) || 0) + 1);
    const iters = prev.size ? 160 : Math.min(420, 220 + n * 2);
    for (let it = 0; it < iters; it++) {
        const alpha = 1 - it / iters;
        for (let i = 0; i < n; i++) {
            for (let j = i + 1; j < n; j++) {
                let dx = sim[i].x - sim[j].x, dy = sim[i].y - sim[j].y;
                let d2 = dx * dx + dy * dy;
                if (d2 > 640000) continue;
                if (d2 < 1) { dx = Math.random() - 0.5; dy = Math.random() - 0.5; d2 = 1; }
                const d = Math.sqrt(d2);
                const f = 5200 / d2;
                sim[i].vx += (dx / d) * f; sim[i].vy += (dy / d) * f;
                sim[j].vx -= (dx / d) * f; sim[j].vy -= (dy / d) * f;
            }
        }
        for (const e of edges) {
            const a = idx.get(e.from), b = idx.get(e.to);
            if (a == null || b == null) continue;
            const A = sim[a], B = sim[b];
            const dx = B.x - A.x, dy = B.y - A.y;
            const d = Math.hypot(dx, dy) || 0.01;
            const want = (edgeLen[e.type] || 170) + (A.lw + B.lw) * 0.22;
            const mul = pairMul.get(pk(e.from, e.to)) || 1;
            const f = ((d - want) * 0.045 * (0.6 + 0.4 * Math.min(e.weight || 1, 1))) / mul;
            A.vx += (dx / d) * f; A.vy += (dy / d) * f;
            B.vx -= (dx / d) * f; B.vy -= (dy / d) * f;
        }
        for (const s of sim) {
            s.vx -= s.x * 0.004; s.vy -= s.y * 0.006; // 弱中心引力（横向更宽，适合宽屏）
            const sp = Math.hypot(s.vx, s.vy);
            const max = 30 * alpha + 1;
            const m = sp > max ? max / sp : 1;
            s.x += s.vx * m; s.y += s.vy * m;
            s.vx *= 0.55; s.vy *= 0.55;
        }
        if (it > iters * 0.4 && it % 2 === 0) collide(sim, 2);
    }
    collide(sim, 80);
    return sim;
}

async function renderGraph(routeKey = "") {
    const [projectKey, initialWorkID=''] = routeKey.split('/');
    const epoch = routeEpoch;
    let request = 0;
    const live = () => epoch === routeEpoch && wrap.isConnected;
    let tab:GraphTab = initialWorkID==='events'?'memory':initialWorkID ? 'works' : projectKey ? 'files' : loadGraphTab();
    let graphWorkID=initialWorkID==='events'?'':initialWorkID, graphWorkOffset=0;
    view.innerHTML = `
      <header class="bar"><h1>关系网</h1></header>
      <p class="lead">查看项目、文件来源和版本之间的关系。点节点看依据，或在下方清单查找。</p>
      <div id="g-context"></div>
      <div class="graph-head">
        <div class="view-switch graph-tabs" role="tablist" aria-label="关系网种类">
          <mdui-button variant="text" type="button" class="view-btn btn btn-sm" data-gtab="projects" role="tab" aria-selected="false">项目关系</mdui-button>
          <mdui-button variant="text" type="button" class="view-btn btn btn-sm" data-gtab="files" role="tab" aria-selected="false">文件关系</mdui-button>
          <mdui-button variant="text" type="button" class="view-btn btn btn-sm" data-gtab="works" role="tab" aria-selected="false">成果与版本</mdui-button>
          <mdui-button variant="text" type="button" class="view-btn btn btn-sm" data-gtab="memory" role="tab" aria-selected="false">文件变化</mdui-button>
        </div>
        <div class="graph-toolbar" id="g-tools"></div>
      </div>
      <div class="graph-legend" id="g-legend" role="group" aria-label="按关系类型筛选（可多选）"></div>
      <div class="graph-note" id="g-note" aria-live="polite"></div>
      <div class="graph-shell">
        <div class="graph-stage">
          <div class="graph-wrap" id="g-wrap"><div class="graph-empty">加载中…</div></div>
          <div class="graph-zoom" role="group" aria-label="缩放">
            <mdui-button variant="text" type="button" class="zbtn btn btn-sm" data-zoom="in" aria-label="放大" title="放大">＋</mdui-button>
            <mdui-button variant="text" type="button" class="zbtn btn btn-sm" data-zoom="out" aria-label="缩小" title="缩小">－</mdui-button>
            <mdui-button variant="text" type="button" class="zbtn wide btn btn-sm" data-zoom="fit" title="整张图放进窗口">适应窗口</mdui-button>
          </div>
          <p class="graph-hint">滚轮缩放 · 拖空白处平移 · 拖节点挪位置 · 点节点看它连着谁 · Esc 取消</p>
        </div>
        <aside class="graph-detail" id="g-detail" tabindex="-1" aria-live="polite" aria-label="关系详情"></aside>
      </div>
      <section class="graph-list" aria-labelledby="g-list-h">
        <div class="graph-list-head">
          <h2 id="g-list-h">关系清单 <span class="count" id="g-list-n"></span></h2>
          <span id="g-list-focus"></span>
          <label class="sr-only" for="g-list-q">按名字筛选关系</label>
          <input id="g-list-q" class="search input input-sm" type="search" placeholder="按名字筛选关系" aria-label="按名字筛选关系">
        </div>
        <div class="rel-cols" aria-hidden="true"><span>甲</span><span>关系</span><span>乙</span><span>理由</span><span>把握</span></div>
        <ul class="rel-list" id="g-list"></ul>
      </section>`;

    const tools = document.getElementById("g-tools")!;
    const legend = document.getElementById("g-legend")!;
    const wrap = document.getElementById("g-wrap")!;
    const detail = document.getElementById("g-detail")!;
    const noteEl = document.getElementById("g-note")!;
    const listEl = document.getElementById("g-list")!;
    const listN = document.getElementById("g-list-n")!;
    const listFocus = document.getElementById("g-list-focus")!;
    const listQ = document.getElementById("g-list-q") as HTMLInputElement;
    let projectList: {name: string; source: string; id?: string; path?: string}[] = [];
    let fileProjectKey = projectKey.startsWith('root:') ? 'name:'+projectKey.slice(5) : projectKey.startsWith('place:') ? projectKey : '';
    let activeProjectKey = '';
    let agentState:AgentState|undefined;
    let agentStateError = false;
    let projectSearchTimer = 0;
    let expandAttach = loadGraphExpandAttach();
    let lastFileMeta: {project: string; note: string; skipped: string; attachCount: number; attachShown: number; attachHidden: number; found: boolean} = {
        project: "", note: "", skipped: "", attachCount: 0, attachShown: 0, attachHidden: 0, found: true,
    };

    let allNodes: GNode[] = [];
    let allEdges: GEdge[] = [];
    let hidden = new Set<string>();
    let sel: {kind: "node" | "edge"; id: string} | null = null;
    let sim: SimNode[] = [];
    let byId = new Map<string, SimNode>();
    let shown: GEdge[] = [];
    let positions = new Map<string, {x: number; y: number}>();
    let vb = {x: 0, y: 0, w: 800, h: 500};
    let fitVb = {...vb};
    let svg: SVGSVGElement | null = null;
    const nameOf = (id: string) => allNodes.find(n => n.id === id)?.name || id;

    const syncTabs = () => {
        view.querySelectorAll<HTMLButtonElement>("[data-gtab]").forEach(b => {
            const on = b.dataset.gtab === tab;
            b.classList.toggle("on", on);
            b.setAttribute("aria-selected", on ? "true" : "false");
        });
    };

    const paintLegend = () => {
        const counts = new Map<string, number>();
        for (const e of allEdges) counts.set(e.type, (counts.get(e.type) || 0) + 1);
        const types = [...counts.keys()].sort((a, b) => edgeSort(a) - edgeSort(b));
        legend.innerHTML = types.length ? `<span class="legend-cap">关系类型</span>` + types.map(t => {
            const on = !hidden.has(t);
            return `<mdui-button variant="text" type="button" class="legend-btn${on ? " on" : ""}" data-etype="${esc(t)}" aria-pressed="${on}" title="${esc(edgeTips[t] || "")}（点一下显示/隐藏）">
              <i class="sw et-${esc(t)}" aria-hidden="true"></i>${esc(edgeLabels[t] || t)}<span class="n">${counts.get(t)}</span></mdui-button>`;
        }).join("") + (hidden.size ? `<mdui-button variant="text" type="button" class="legend-reset btn btn-sm" data-etype="*">全部显示</mdui-button>` : "")
            : `<span class="sub">还没有关系</span>`;
        legend.querySelectorAll<HTMLButtonElement>("[data-etype]").forEach(b => {
            b.onclick = () => {
                const t = b.dataset.etype!;
                if (t === "*") hidden.clear();
                else if (hidden.has(t)) hidden.delete(t); else hidden.add(t);
                if (sel?.kind === "edge" && !allEdges.some(e => e.id === sel!.id && !hidden.has(e.type))) sel = null;
                paintLegend();
                draw(true);
            };
        });
    };

    // 同一对节点之间的多条关系：用弧线错开
    const offsets = new Map<string, number>();
    const computeOffsets = () => {
        offsets.clear();
        const groups = new Map<string, GEdge[]>();
        for (const e of shown) {
            const k = e.from < e.to ? e.from + "|" + e.to : e.to + "|" + e.from;
            if (!groups.has(k)) groups.set(k, []);
            groups.get(k)!.push(e);
        }
        for (const g of groups.values()) {
            g.sort((a, b) => edgeSort(a.type) - edgeSort(b.type));
            g.forEach((e, i) => offsets.set(e.id, (i - (g.length - 1) / 2) * 26));
        }
    };
    const pathFor = (e: GEdge) => {
        const a = byId.get(e.from), b = byId.get(e.to);
        if (!a || !b) return "";
        // 统一方向，保证同一对的弧线朝两侧分开
        const [p, target] = e.directed ? [a,b] : a.id < b.id ? [a, b] : [b, a];
        const distance=Math.hypot(target.x-p.x,target.y-p.y)||1;
        const q=e.directed?{...target,x:target.x-(target.x-p.x)/distance*(target.r+7),y:target.y-(target.y-p.y)/distance*(target.r+7)}:target;
        const off = offsets.get(e.id) || 0;
        const mx = (p.x + q.x) / 2, my = (p.y + q.y) / 2;
        if (!off) return `M${p.x.toFixed(1)} ${p.y.toFixed(1)} L${q.x.toFixed(1)} ${q.y.toFixed(1)}`;
        const dx = q.x - p.x, dy = q.y - p.y, d = Math.hypot(dx, dy) || 1;
        const cx = mx - (dy / d) * off * 2, cy = my + (dx / d) * off * 2;
        return `M${p.x.toFixed(1)} ${p.y.toFixed(1)} Q${cx.toFixed(1)} ${cy.toFixed(1)} ${q.x.toFixed(1)} ${q.y.toFixed(1)}`;
    };

    const setVb = () => { svg?.setAttribute("viewBox", `${vb.x.toFixed(1)} ${vb.y.toFixed(1)} ${vb.w.toFixed(1)} ${vb.h.toFixed(1)}`); };
    const computeFit = () => {
        const cw = Math.max(wrap.clientWidth || 720, 320), ch = Math.max(wrap.clientHeight || 460, 320);
        let l = Infinity, r = -Infinity, t = Infinity, b = -Infinity;
        for (const s of sim) { const B = nodeBox(s); l = Math.min(l, B.l); r = Math.max(r, B.r); t = Math.min(t, B.t); b = Math.max(b, B.b); }
        if (!isFinite(l)) { l = 0; r = cw; t = 0; b = ch; }
        const pad = 36;
        let w = r - l + pad * 2, h = b - t + pad * 2;
        // 小图不放大过头（最多 1.1 倍），也就不会挤成一团或字特别大
        w = Math.max(w, cw / 1.1); h = Math.max(h, ch / 1.1);
        const ar = cw / ch;
        if (w / h < ar) w = h * ar; else h = w / ar;
        fitVb = {x: (l + r) / 2 - w / 2, y: (t + b) / 2 - h / 2, w, h};
        vb = {...fitVb};
    };
    const toSvg = (cx: number, cy: number) => {
        const m = svg!.getScreenCTM();
        if (!m) return {x: cx, y: cy};
        const p = new DOMPoint(cx, cy).matrixTransform(m.inverse());
        return {x: p.x, y: p.y};
    };
    const zoomAt = (factor: number, cx?: number, cy?: number) => {
        const p = cx == null ? {x: vb.x + vb.w / 2, y: vb.y + vb.h / 2} : toSvg(cx, cy!);
        const nw = Math.min(Math.max(vb.w * factor, fitVb.w * 0.15), fitVb.w * 4);
        const f = nw / vb.w;
        vb = {x: p.x - (p.x - vb.x) * f, y: p.y - (p.y - vb.y) * f, w: vb.w * f, h: vb.h * f};
        setVb();
    };

    const neighborsOf = (id: string) => {
        const ns = new Set<string>([id]);
        const es = new Set<string>();
        for (const e of shown) if (e.from === id || e.to === id) { ns.add(e.from); ns.add(e.to); es.add(e.id); }
        return {ns, es};
    };
    const applyHighlight = (target: {kind: "node" | "edge"; id: string} | null) => {
        if (!svg) return;
        svg.querySelectorAll(".hl,.on").forEach(x => x.classList.remove("hl", "on"));
        if (!target) { svg.classList.remove("dim"); return; }
        let ns = new Set<string>(), es = new Set<string>();
        if (target.kind === "node") ({ns, es} = neighborsOf(target.id));
        else { const e = shown.find(x => x.id === target.id); if (e) { ns = new Set([e.from, e.to]); es = new Set([e.id]); } }
        svg.classList.add("dim");
        svg.querySelectorAll<SVGElement>(".graph-node").forEach(g => { if (ns.has(g.dataset.nid!)) g.classList.add("hl"); if (target.kind === "node" && g.dataset.nid === target.id) g.classList.add("on"); });
        svg.querySelectorAll<SVGElement>(".graph-edge").forEach(p => { if (es.has(p.dataset.eid!)) { p.classList.add("hl"); if (target.kind === "edge") p.classList.add("on"); } });
        // 被选中的边/节点放到最上层，弧线不被别的线压住
        svg.querySelectorAll<SVGElement>(".graph-edge.hl").forEach(p => p.parentNode!.appendChild(p));
    };

    const select = (s: {kind: "node" | "edge"; id: string} | null) => {
        const previous = sel;
        sel = s;
        applyHighlight(sel);
        paintDetail();
        paintList();
        if(window.matchMedia('(max-width: 1080px)').matches) {
            if(s){detail.focus({preventScroll:true});detail.scrollIntoView({block:'nearest'});}
            else if(previous?.kind==='node'){svg?.querySelector<SVGElement>(`.graph-node[data-nid="${CSS.escape(previous.id)}"]`)?.focus();}
        }
    };

    const nodeMeta = (n: GNode) => {
        const bits: string[] = [];
        bits.push(n.kind === "memory_source" ? "来源记录" : n.kind === "memory_event" ? "变化记录" : n.kind === "memory_project" ? "项目记忆" : n.kind === "memory_file" ? "文件身份" : n.kind === "project" ? "项目" : n.kind === "work" ? "作品" : n.kind === "version" ? "版本" : n.kind === "agent" ? "AI 来源" : n.kind === "folder" ? "文件夹" : "文件");
        if (n.source) bits.push(n.kind==='version'?(versionRoles[n.source]||n.source):n.source);
        if(n.kind==='work'||n.kind==='version')bits.push(graphVersionState(n));
        if (n.workTypes?.length) bits.push("工种 " + n.workTypes.join("、"));
        if (n.memberCount) bits.push(`成员 ${n.memberCount}`);
        if (n.attachCount) bits.push(`挂靠 ${n.attachCount}`);
        if (n.extra&&!n.kind.startsWith('memory_')) bits.push(n.extra);
        if (n.size) bits.push(size(n.size));
        return bits.join(" · ");
    };
    const paintDetail = () => {
        if (!sel) {
            if(tab==='memory'){detail.innerHTML='<h2 class="dt-title">文件变化的依据</h2><p>项目记忆连接文件身份，文件身份连接这页的观察记录。</p><p>点节点查看位置、观察时间和依据。来源未确认的记录不会标成某个 AI 的操作。</p><p class="sub">只读已有账本，不触发扫描或修改文件。</p>';return}
            if(tab==='works'){
                detail.innerHTML=`<h2 class="dt-title">${graphWorkID?'这份作品的版本':'先选一份作品'}</h2><p>${graphWorkID?'箭头从源版本指向导出件。虚线表示作品归属，不代表内容演变。':'默认收起各个版本，选中作品后可以展开，也可以直接去成果详情。'}</p><p class="sub">只显示账本已有关系；同名、相同内容和登记时间不证明导出过程。</p>`;
                return;
            }
            const nodeWord = tab === "projects" ? "个项目" : "个文件与来源节点";
            detail.innerHTML = `<h2 class="dt-title">怎么看这张图</h2>
              <ul class="dt-tips">
                <li>一条线 = 一种关系；同一对之间有几种关系，就有几条弧线错开画。</li>
                <li>同目录、同内容、同任务的${tab === "projects" ? "项目" : "文件"}是<b>两两互连</b>的小团。</li>
                <li>点上面的关系类型可以只看某几种。</li>
              </ul>
              <p class="sub">${allNodes.length} ${nodeWord} · ${shown.length} 条关系显示中</p>`;
            return;
        }
        if (sel.kind === "edge") {
            const e = allEdges.find(x => x.id === sel!.id);
            if (!e) { detail.innerHTML = ""; return; }
            const a = allNodes.find(n => n.id === e.from), b = allNodes.find(n => n.id === e.to);
            detail.innerHTML = `<p class="dt-kicker"><i class="sw et-${esc(e.type)}" aria-hidden="true"></i>${esc(edgeLabels[e.type] || e.type)}</p>
              <div class="dt-pair">
                <mdui-button variant="text" type="button" class="dt-node btn btn-sm" data-pick="${esc(e.from)}" title="${esc(a?.path || a?.name || "")}">${esc(a?.name || e.from)}</mdui-button>
                <span class="dt-link" aria-hidden="true">${e.directed?'→':'↔'}</span>
                <mdui-button variant="text" type="button" class="dt-node btn btn-sm" data-pick="${esc(e.to)}" title="${esc(b?.path || b?.name || "")}">${esc(b?.name || e.to)}</mdui-button>
              </div>
              <p class="dt-reason">${esc(e.reason)}</p>
              <p><span class="chip ${e.confidence === "较有把握" ? "ok" : e.confidence === "待你定" ? "warn" : "info"}">${esc(e.confidence)}</span></p>
              <div class="actions"><mdui-button variant="text" type="button" class="btn btn-sm btn-ghost" data-clear>取消选择</mdui-button></div>`;
        } else {
            const n = allNodes.find(x => x.id === sel!.id);
            if (!n) { detail.innerHTML = ""; return; }
            const {es} = neighborsOf(n.id);
            const rel = shown.filter(e => es.has(e.id)).sort((a, b) => edgeSort(a.type) - edgeSort(b.type));
            const groups = new Map<string, GEdge[]>();
            for (const e of rel) { if (!groups.has(e.type)) groups.set(e.type, []); groups.get(e.type)!.push(e); }
            const sourceCandidate=agentState?.receipt.records?.find(c=>c.id===n.agent);
            const sourceStatus=sourceCandidate&&agentState?agentPresentation(sourceCandidate,agentState.receipt):undefined;
            const sourceNote=n.kind==='agent'?`<div class="graph-source-status"><p>${esc(sourceStatus?sourceStatus.label:agentStateError?'接入状态暂时无法读取':'未在本机 Agent 检查中登记')}</p><p class="sub">${esc(sourceStatus?.ignored?'自动接入已暂停。已写规则保留。':sourceStatus?.detail||'图中保留历史来源标记，不据此推断工具已安装或文件作者已核验。')}</p></div>`:'';
            const nodeKey=n.source==='原地'&&n.projectId?'place:'+n.projectId:'root:'+(n.project||n.name);
            const openBtn = n.kind.startsWith('memory_')&&activeProjectKey ? `<a class="btn-link" href="#/memory/${encodeURIComponent(activeProjectKey)}">查看项目文件记录</a>` : n.workId&&activeProjectKey ? `<a class="btn-link" href="#/works/${encodeURIComponent(activeProjectKey)}/${encodeURIComponent(n.workId)}">查看这份作品</a>${n.kind==='work'||n.reference?`<mdui-button variant="text" type="button" class="btn btn-sm" data-expand-work="${esc(n.workId)}">展开作品版本</mdui-button>`:''}${n.versionId&&!n.reference&&n.state==='available'?`<mdui-button variant="text" type="button" class="btn btn-sm" data-open-graph-version="${esc(n.versionId)}">在文件夹中显示</mdui-button>`:''}` : n.kind === "agent" ? `<a class="btn-link" href="#/agents/${encodeURIComponent(n.agent||n.name)}">查看 AI 接入</a>` : n.kind === "project"
                ? (n.source === "原地" && n.projectId
                    ? `<a class="btn-link" href="#/place/${encodeURIComponent(n.projectId)}">打开原地项目</a>`
                    : `<a class="btn-link" href="#/project/${encodeURIComponent(n.project || n.name)}">打开项目</a>`)+`<a class="btn-link" href="#/graph/${encodeURIComponent(nodeKey)}">查看文件关系</a>`
                : activeProjectKey?`<a class="btn-link" href="#/works/${encodeURIComponent(activeProjectKey)}">查看项目成果与版本</a>`:"";
            detail.innerHTML = `<p class="dt-kicker">${esc(nodeMeta(n))}</p>
              <h2 class="dt-title" title="${esc(n.name)}">${esc(n.name)}</h2>
              ${n.path ? `<div class="path" title="${esc(n.path)}">${esc(n.path)}</div>` : ""}
              ${sourceNote}${n.kind.startsWith('memory_')?`<p class="memory-path">${esc(n.extra||'')}</p>`:''}
              ${n.recordedUsable&&!n.usable?'<p class="works-warning">旧确认保留，但本次无法确认原内容可用。</p>':''}
              <p class="sub">连着 ${new Set(rel.map(e => e.from === n.id ? e.to : e.from)).size} 个，共 ${rel.length} 条关系</p>
              ${[...groups.entries()].map(([t, es2]) => `<div class="dt-group"><p class="dt-gh"><i class="sw et-${esc(t)}" aria-hidden="true"></i>${esc(edgeLabels[t] || t)}</p>
                <ul>${es2.map(e => { const o = e.from === n.id ? e.to : e.from; return `<li><mdui-button variant="text" type="button" class="dt-node btn btn-sm" data-pick="${esc(o)}" title="${esc(e.reason)}">${esc(nameOf(o))}</mdui-button></li>`; }).join("")}</ul></div>`).join("")
                || `<p class="sub">在当前筛选下没有关系。</p>`}
              <div class="actions">${openBtn}
                ${n.path ? `<mdui-button variant="text" class="btn btn-sm btn-ghost" data-reveal="${esc(n.path)}" type="button">${revealLabel()}</mdui-button>` : ""}
                <mdui-button variant="text" type="button" class="btn btn-sm btn-ghost" data-clear>取消选择</mdui-button>
              </div><p id="g-version-action" role="status"></p>`;
            enhance(detail);
        }
        detail.querySelectorAll<HTMLButtonElement>("[data-pick]").forEach(b => b.onclick = () => select({kind: "node", id: b.dataset.pick!}));
        detail.querySelectorAll<HTMLButtonElement>("[data-clear]").forEach(b => b.onclick = () => select(null));
        detail.querySelectorAll<HTMLButtonElement>('[data-expand-work]').forEach(b=>b.onclick=()=>void loadWorks(b.dataset.expandWork!,0));
        detail.querySelectorAll<HTMLButtonElement>('[data-open-graph-version]').forEach(b=>b.onclick=async()=>{
            const ticket=request,key=activeProjectKey,id=b.dataset.openGraphVersion!,message=detail.querySelector<HTMLElement>('#g-version-action')!;
            b.disabled=true;message.textContent='正在重新核对版本内容……';
            try{const path=await call<string>('VersionFile',key,id);if(!live()||ticket!==request||!b.isConnected)return;await call('Reveal',path);if(message.isConnected)message.textContent='已打开文件位置。'}catch(e){if(live()&&ticket===request&&message.isConnected)message.textContent=errText(e)}finally{if(b.isConnected)b.disabled=false}
        });
    };

    const paintList = () => {
        const q = listQ.value.trim().toLowerCase();
        const overview=tab==='works'&&!graphWorkID;
        view.querySelector<HTMLElement>('.rel-cols')!.hidden=overview;
        document.getElementById('g-list-h')!.firstChild!.textContent=overview?'作品清单 ':'关系清单 ';
        if(overview){
            const nodes=allNodes.filter(n=>!q||n.name.toLowerCase().includes(q));
            listN.textContent=`${nodes.length} 份`;listFocus.innerHTML='';
            listEl.innerHTML=nodes.map(n=>`<li><mdui-button variant="text" type="button" class="graph-work-row" data-work-pick="${esc(n.id)}"><strong>${esc(n.name)}</strong><span>${n.versionCount||0} 个版本</span><span>${esc(graphVersionState(n))}</span></mdui-button></li>`).join('')||'<li class="rel-empty">当前页没有符合筛选的作品</li>';
            listEl.querySelectorAll<HTMLButtonElement>('[data-work-pick]').forEach(b=>b.onclick=()=>select({kind:'node',id:b.dataset.workPick!}));
            return;
        }
        let rows = shown.slice();
        if (sel?.kind === "node") rows = rows.filter(e => e.from === sel!.id || e.to === sel!.id);
        if (q) rows = rows.filter(e => (nameOf(e.from) + " " + nameOf(e.to) + " " + e.reason).toLowerCase().includes(q));
        rows.sort((a, b) => edgeSort(a.type) - edgeSort(b.type) || nameOf(a.from).localeCompare(nameOf(b.from), "zh") || nameOf(a.to).localeCompare(nameOf(b.to), "zh"));
        listN.textContent = `${rows.length} 条`;
        listFocus.innerHTML = sel?.kind === "node"
            ? `<mdui-button variant="text" type="button" class="focus-chip btn btn-sm" id="g-list-clear" title="取消，只看全部">只看「${esc(nameOf(sel.id))}」的关系 ✕</mdui-button>` : "";
        document.getElementById("g-list-clear")?.addEventListener("click", () => select(null));
        const cap = 300;
        listEl.innerHTML = rows.slice(0, cap).map(e => {
            const a = allNodes.find(n => n.id === e.from), b = allNodes.find(n => n.id === e.to);
            const on = sel?.kind === "edge" && sel.id === e.id;
            return `<li><mdui-button variant="text" type="button" class="rel-row${on ? " on" : ""}" data-eid="${esc(e.id)}" aria-pressed="${on}">
              <span class="rel-a" title="${esc(a?.path || a?.name || "")}">${esc(a?.name || e.from)}</span>
              <span class="rel-t"><i class="sw et-${esc(e.type)}" aria-hidden="true"></i>${esc(edgeLabels[e.type] || e.type)}</span>
              <span class="rel-b" title="${esc(b?.path || b?.name || "")}">${esc(b?.name || e.to)}</span>
              <span class="rel-r">${esc(e.reason)}</span>
              <span class="rel-c">${esc(e.confidence)}</span>
            </mdui-button></li>`;
        }).join("") + (rows.length > cap ? `<li class="sub rel-more">还有 ${rows.length - cap} 条，先筛选一下</li>` : "")
            || `<li class="rel-empty">${shown.length ? "没有符合筛选的关系" : "当前没有关系可列"}</li>`;
        listEl.querySelectorAll<HTMLButtonElement>(".rel-row").forEach(b => {
            const id = b.dataset.eid!;
            b.onclick = () => select(sel?.kind === "edge" && sel.id === id ? null : {kind: "edge", id});
            b.onmouseenter = () => applyHighlight({kind: "edge", id});
            b.onmouseleave = () => applyHighlight(sel);
            b.onfocus = () => applyHighlight({kind: "edge", id});
            b.onblur = () => applyHighlight(sel);
        });
    };

    const nodeSvg = (n: SimNode) => {
        const badge = n.attachCount && n.attachCount > 0
            ? `<g class="graph-badge-g"><circle class="graph-badge-bg" cx="${(n.r * 0.75).toFixed(1)}" cy="${(-n.r * 0.75).toFixed(1)}" r="7.5"/><text class="graph-badge" text-anchor="middle" dominant-baseline="central" x="${(n.r * 0.75).toFixed(1)}" y="${(-n.r * 0.75).toFixed(1)}">${n.attachCount > 9 ? "9+" : n.attachCount}</text></g>` : "";
        const y0 = -((n.lines.length - 1) * LINE_H) / 2;
        const label = n.lines.map((l, i) => `<tspan x="${(n.r + 8).toFixed(1)}" y="${(y0 + i * LINE_H).toFixed(1)}">${esc(l)}</tspan>`).join("");
        const box=nodeBox(n);
        return `<g class="graph-node k-${esc(n.kind)}" data-nid="${esc(n.id)}" transform="translate(${n.x.toFixed(1)},${n.y.toFixed(1)})" tabindex="0" role="button" aria-label="${esc(n.name)}，${n.deg} 条关系">
          <rect class="graph-hit-area" x="${(box.l-n.x).toFixed(1)}" y="${(box.t-n.y).toFixed(1)}" width="${(box.r-box.l).toFixed(1)}" height="${(box.b-box.t).toFixed(1)}" fill="transparent" stroke="none" pointer-events="all"/>
          <title>${esc(n.name + (n.path ? "\n" + n.path : ""))}</title>
          <circle class="halo" r="${(n.r + 5).toFixed(1)}"/><circle class="dot" r="${n.r.toFixed(1)}"/>${badge}
          <text class="lbl" dominant-baseline="central">${label}</text>
        </g>`;
    };

    const draw = (keepView = false) => {
        shown = allEdges.filter(e => !hidden.has(e.type));
        if (!allNodes.length) {
            let title = "这里还没什么关系可画";
            let tip = "先在「项目」里登记或归位一些项目，再回来看它们怎么连。";
            if(tab==='works'){title='这个项目还没有登记成果';tip=activeProjectKey?`在<a href="#/works/${encodeURIComponent(activeProjectKey)}">成果页</a>添加文件和版本，登记后这里会显示它们的关系。`:'先选择一个项目，再查看它的作品与版本。'}
            if (tab === "files") {
                if (!fileProjectKey && !projectList.length) {
                    title = "还没有可选的项目";
                    tip = "文件关系是按项目看的。先在「项目」里出现项目（总文件夹或原地登记），再回来选一个。";
                } else if (!lastFileMeta.found) {
                    title = "找不到这个项目";
                    tip = "总文件夹里没有同名目录，登记表里也没有。换一个，或到「项目」里重新识别。";
                } else if (lastFileMeta.attachHidden > 0 && !expandAttach) {
                    title = "项目目录里暂时没什么可画的";
                    tip = `不过登记里还有 <b>${lastFileMeta.attachHidden}</b> 个在桌面/下载等其他文件夹里的相关文件。打开上面的「含其他文件夹里的相关文件」就能看见。`;
                } else if (lastFileMeta.attachCount > 0 && lastFileMeta.attachShown === 0 && expandAttach) {
                    title = "项目目录里暂时没什么可画的";
                    tip = "挂靠文件因节点上限或其他原因没画进来，看看上方说明。";
                } else {
                    title = "这个项目里还没有可画的文件";
                    tip = "文件关系只画「已进项目列表或挂靠进该项目」的文件。往任务/成品里放几个，或确认桌面/下载里的相关文件已被识别挂靠。";
                }
            }
            wrap.innerHTML = emptyState(title, tip);
            svg = null; paintDetail(); paintList();
            return;
        }
        const oldVb = {...vb};
        sim = layoutMesh(allNodes, shown, positions);
        byId = new Map(sim.map(s => [s.id, s]));
        positions = new Map(sim.map(s => [s.id, {x: s.x, y: s.y}]));
        computeOffsets();
        const edgesSvg = shown.map(e => {
            const d = pathFor(e);
            if (!d) return "";
            return `<path class="graph-edge et-${esc(e.type)}" data-eid="${esc(e.id)}" d="${d}" ${e.directed?'marker-end="url(#graph-export-arrow)"':''}><title>${esc(`${nameOf(e.from)} — ${edgeLabels[e.type] || e.type} — ${nameOf(e.to)}\n${e.reason}`)}</title></path>`;
        }).join("");
        const hits = shown.map(e => `<path class="graph-hit" data-hit="${esc(e.id)}" d="${pathFor(e)}"/>`).join("");
        const nodesSvg = sim.slice().sort((a, b) => a.deg - b.deg).map(nodeSvg).join("");
        wrap.innerHTML = `<svg class="graph-svg" role="group" aria-label="关系网图：${allNodes.length} 个点，${shown.length} 条关系">
          <defs><marker id="graph-export-arrow" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="7" markerHeight="7" orient="auto-start-reverse"><path d="M 0 0 L 10 5 L 0 10 z" fill="var(--accent)"/></marker></defs>
          <g class="g-hits">${hits}</g><g class="g-edges">${edgesSvg}</g><g class="g-nodes">${nodesSvg}</g></svg>`;
        svg = wrap.querySelector("svg")!;
        computeFit();
        if (keepView && oldVb.w > 0 && svg) vb = oldVb;
        setVb();
        bindSvg();
        if (sel && !(sel.kind === "node" ? byId.has(sel.id) : shown.some(e => e.id === sel!.id))) sel = null;
        applyHighlight(sel);
        paintDetail();
        paintList();
    };

    const redrawNode = (id: string) => {
        const s = byId.get(id);
        if (!s || !svg) return;
        svg.querySelector<SVGGElement>(`.graph-node[data-nid="${CSS.escape(id)}"]`)?.setAttribute("transform", `translate(${s.x.toFixed(1)},${s.y.toFixed(1)})`);
        for (const e of shown) {
            if (e.from !== id && e.to !== id) continue;
            const d = pathFor(e);
            svg.querySelector(`.graph-edge[data-eid="${CSS.escape(e.id)}"]`)?.setAttribute("d", d);
            svg.querySelector(`.graph-hit[data-hit="${CSS.escape(e.id)}"]`)?.setAttribute("d", d);
        }
    };

    const bindSvg = () => {
        if (!svg) return;
        const s = svg;
        let drag: {mode: "pan" | "node"; id?: string; sx: number; sy: number; p0: {x: number; y: number}; vb0: typeof vb; n0?: {x: number; y: number}; moved: boolean; target: Element | null} | null = null;
        s.addEventListener("wheel", ev => { ev.preventDefault(); zoomAt(Math.exp(ev.deltaY * 0.0015), ev.clientX, ev.clientY); }, {passive: false});
        s.addEventListener("pointerdown", ev => {
            if (ev.button !== 0) return;
            const t = ev.target as Element;
            const g = t.closest<SVGGElement>(".graph-node");
            const p0 = toSvg(ev.clientX, ev.clientY);
            if (g) {
                const n = byId.get(g.dataset.nid!)!;
                drag = {mode: "node", id: n.id, sx: ev.clientX, sy: ev.clientY, p0, vb0: {...vb}, n0: {x: n.x, y: n.y}, moved: false, target: t};
            } else {
                drag = {mode: "pan", sx: ev.clientX, sy: ev.clientY, p0, vb0: {...vb}, moved: false, target: t};
            }
            s.setPointerCapture(ev.pointerId);
        });
        s.addEventListener("pointermove", ev => {
            if (!drag) {
                const g = (ev.target as Element).closest<SVGGElement>(".graph-node");
                const h = (ev.target as Element).closest<SVGPathElement>(".graph-hit");
                if (g) applyHighlight({kind: "node", id: g.dataset.nid!});
                else if (h) applyHighlight({kind: "edge", id: h.dataset.hit!});
                else applyHighlight(sel);
                return;
            }
            if (Math.hypot(ev.clientX - drag.sx, ev.clientY - drag.sy) > 4) drag.moved = true;
            if (!drag.moved) return;
            s.classList.add("dragging");
            if (drag.mode === "pan") {
                const k = drag.vb0.w / (s.clientWidth || 1);
                const kk = Math.max(k, drag.vb0.h / (s.clientHeight || 1));
                vb = {...drag.vb0, x: drag.vb0.x - (ev.clientX - drag.sx) * kk, y: drag.vb0.y - (ev.clientY - drag.sy) * kk};
                setVb();
            } else if (drag.id && drag.n0) {
                const p = toSvg(ev.clientX, ev.clientY);
                const n = byId.get(drag.id)!;
                n.x = drag.n0.x + (p.x - drag.p0.x); n.y = drag.n0.y + (p.y - drag.p0.y);
                positions.set(n.id, {x: n.x, y: n.y});
                redrawNode(n.id);
            }
        });
        const end = (ev: PointerEvent) => {
            if (!drag) return;
            const d = drag; drag = null;
            s.classList.remove("dragging");
            try { s.releasePointerCapture(ev.pointerId); } catch {}
            if (d.moved) return;
            const t = d.target;
            const g = t?.closest<SVGGElement>(".graph-node");
            const h = t?.closest<SVGPathElement>(".graph-hit");
            if (g) select(sel?.kind === "node" && sel.id === g.dataset.nid ? null : {kind: "node", id: g.dataset.nid!});
            else if (h) select({kind: "edge", id: h.dataset.hit!});
            else select(null);
        };
        s.addEventListener("pointerup", end);
        s.addEventListener("pointercancel", end);
        s.addEventListener("pointerleave", () => { if (!drag) applyHighlight(sel); });
        s.querySelectorAll<SVGGElement>(".graph-node").forEach(g => {
            g.addEventListener("keydown", ev => {
                if (ev.key === "Enter" || ev.key === " ") { ev.preventDefault(); select({kind: "node", id: g.dataset.nid!}); }
            });
            g.addEventListener("focus", () => applyHighlight({kind: "node", id: g.dataset.nid!}));
            g.addEventListener("blur", () => applyHighlight(sel));
        });
    };

    view.querySelectorAll<HTMLButtonElement>("[data-zoom]").forEach(b => {
        b.onclick = () => {
            if (!svg) return;
            const z = b.dataset.zoom;
            if (z === "in") zoomAt(1 / 1.25);
            else if (z === "out") zoomAt(1.25);
            else { computeFit(); setVb(); }
        };
    });
    listQ.oninput = () => paintList();
    const onKey = (ev: KeyboardEvent) => {
        if (!document.body.contains(wrap)) { window.removeEventListener("keydown", onKey); return; }
        if (ev.key === "Escape" && sel) select(null);
    };
    window.addEventListener("keydown", onKey);

    const setData = (nodes: GNode[], edges: GEdge[], note: string) => {
        allNodes = nodes; allEdges = edges; sel = null; positions = new Map(); hidden = new Set();
        noteEl.innerHTML = note ? `<p>${esc(note)}</p>` : "";
        paintLegend();
        draw(false);
    };

    const loadProjects = async () => {
        const ticket=++request;
        document.getElementById('g-context')!.innerHTML='';
        tools.innerHTML = `<span class="count" id="g-count" aria-live="polite"></span>
          <mdui-button variant="text" class="btn btn-sm btn-ghost" id="g-refresh" type="button">重新加载</mdui-button>`;
        document.getElementById("g-refresh")!.onclick = () => loadProjects();
        wrap.innerHTML = `<div class="graph-empty">${loading("正在整理项目关系")}</div>`;
        try {
            const g = await call<ProjectGraph>("ProjectGraph");
            if (!live()||ticket!==request||tab!=='projects') return;
            document.getElementById("g-count")!.textContent = `${(g.nodes || []).length} 个项目 · ${(g.edges || []).length} 条关系`;
            setData(g.nodes || [], g.edges || [], g.skipped || "");
        } catch (e) {
            if (!live()||ticket!==request||tab!=='projects') return;
            wrap.innerHTML = failed("项目关系网没读出来", e);
            bindRetry(wrap, () => loadProjects());
        }
    };

    const loadFiles = async () => {
        const setup=++request;
        if (!projectList.length) {
            try {
                const ps = await call<any[]>("Projects");
                projectList = (ps || []).map(p => ({name: p.name, source: p.source, id: p.id, path: p.path}));
            } catch {}
        }
        if(!live()||setup!==request||tab!=='files')return;
        if (!fileProjectKey && projectList.length) {
            const pref = projectList.find(p => p.source === "总文件夹") || projectList[0];
            fileProjectKey = projectSelectKey(pref);
        }
        const filtered = (q: string) => {
            const s = q.trim().toLowerCase();
            if (!s) return projectList;
            return projectList.filter(p =>
                p.name.toLowerCase().includes(s) ||
                (p.path || "").toLowerCase().includes(s) ||
                (p.id || "").toLowerCase().includes(s));
        };
        const paintTools = (q = "") => {
            window.clearTimeout(projectSearchTimer);
            const list = filtered(q);
            const opts = list.map(p => {
                const key = projectSelectKey(p);
                const label = p.name + (p.source === "原地" ? "（原地）" : "");
                const title = p.path || "";
                return `<option value="${esc(key)}" title="${esc(title)}" ${key === fileProjectKey ? "selected" : ""}>${esc(label)}</option>`;
            }).join("") || `<option value="">${projectList.length ? "没有匹配的项目" : "没有项目"}</option>`;
            tools.innerHTML = `
              <div class="graph-field"><label class="tool-label" for="g-proj-q">找项目</label>
              <input id="g-proj-q" class="graph-filter input input-sm" type="search" placeholder="搜索项目名/路径" value="${esc(q)}" aria-label="搜索项目"></div>
              <div class="graph-field"><label class="tool-label" for="g-proj">项目</label>
              <select id="g-proj" class="graph-select select select-sm" aria-label="选择项目">${opts}</select></div>
              <label class="graph-toggle" title="把登记挂靠到本项目、但还在桌面/下载/文稿等处的文件也画进图">
                <input class="checkbox checkbox-primary checkbox-sm" type="checkbox" id="g-expand" ${expandAttach ? "checked" : ""}>
                <span>含其他文件夹里的相关文件</span>
              </label>
              <span class="count" id="g-count" aria-live="polite"></span>
              <mdui-button variant="text" class="btn btn-sm btn-ghost" id="g-refresh" type="button">重新加载</mdui-button>`;
            const qEl = document.getElementById("g-proj-q") as HTMLInputElement;
            const selEl = document.getElementById("g-proj") as HTMLSelectElement;
            const expEl = document.getElementById("g-expand") as HTMLInputElement;
            qEl.oninput = () => {
                window.clearTimeout(projectSearchTimer);
                const ticket=request;
                projectSearchTimer = window.setTimeout(() => {
                    if(!live()||tab!=='files'||ticket!==request)return;
                    const focused=document.activeElement===qEl;
                    paintTools(qEl.value);
                    if(focused)document.getElementById('g-proj-q')?.focus();
                }, 120);
            };
            selEl.onchange = () => { fileProjectKey = selEl.value; graphWorkID='';graphWorkOffset=0;void fetchFiles(); };
            expEl.onchange = () => {
                expandAttach = expEl.checked;
                saveGraphExpandAttach(expandAttach);
                void fetchFiles();
            };
            document.getElementById("g-refresh")!.onclick = () => void fetchFiles();
        };
        const paintBanner = (g: FileGraph) => {
            const bits: string[] = [];
            if (g.note) bits.push(esc(g.note));
            if (g.skipped) bits.push(esc(g.skipped));
            const needExpand = !expandAttach && (g.attachHidden || 0) > 0;
            const notFound = !!(g.skipped && (g.skipped.includes("找不到") || g.note?.includes("找不到")));
            let banner = "";
            if (needExpand) {
                banner = `<div class="graph-banner" role="status">
                  <div class="graph-banner-body">
                    <b>还有 ${g.attachHidden} 个文件在其他文件夹里</b>
                    <p>它们已经挂靠到这个项目，但还在桌面、下载、文稿等处。打开「含其他文件夹里的相关文件」就能画进关系网。</p>
                  </div>
                  <mdui-button variant="text" type="button" class="btn btn-sm btn-primary" id="g-banner-on">打开并重载</mdui-button>
                </div>`;
            } else if (notFound) {
                banner = `<div class="graph-banner warn" role="status">
                  <div class="graph-banner-body"><b>找不到这个项目</b><p>${esc(g.skipped || g.note || "")}</p></div>
                </div>`;
            }
            const noteHtml = bits.length ? `<p>${bits.join("；")}</p>` : "";
            noteEl.innerHTML = banner + noteHtml;
            const btn = document.getElementById("g-banner-on");
            if (btn) btn.onclick = () => {
                expandAttach = true;
                saveGraphExpandAttach(true);
                void fetchFiles();
            };
        };
        const fetchFiles = async () => {
            const ticket=++request;
            paintTools((document.getElementById("g-proj-q") as HTMLInputElement | null)?.value || "");
            wrap.innerHTML = `<div class="graph-empty">${loading("正在整理文件关系")}</div>`;
            noteEl.innerHTML = "";
            const parsed = parseProjectSelectKey(fileProjectKey);
            let name = parsed.name;
            let id = parsed.id;
            if (id && !name) {
                const hit = projectList.find(p => p.id === id);
                if (hit) name = hit.name;
            }
            if (!id && name) {
                const hit = projectList.find(p => p.name === name && p.source === "原地");
                // 仅当总文件夹没有同名、且原地唯一时附带 id（后端仍可按名）
                const rootHit = projectList.find(p => p.name === name && p.source === "总文件夹");
                if (!rootHit && hit?.id) id = hit.id;
            }
            try {
                const [graphResult,sourceResult] = await Promise.allSettled([call<FileGraph>("FileGraph", name || "", id || "", expandAttach),call<AgentState>('AgentEnrollmentStatus')]);
                if (!live()||ticket!==request||tab!=='files') return;
                if(graphResult.status==='rejected')throw graphResult.reason;
                const g=graphResult.value;
                agentState=sourceResult.status==='fulfilled'?sourceResult.value:undefined;
                agentStateError=sourceResult.status==='rejected';
                activeProjectKey=g.projectKey||'';
                const projectHref=activeProjectKey.startsWith('root:')?'#/project/'+encodeURIComponent(activeProjectKey.slice(5)):activeProjectKey.startsWith('place:')?'#/place/'+encodeURIComponent(activeProjectKey.slice(6)):'';
                document.getElementById('g-context')!.innerHTML=projectHref?`<nav class="work-view-nav" aria-label="项目视图"><a href="${projectHref}">文件夹</a><a href="#/works/${encodeURIComponent(activeProjectKey)}">成果</a><span aria-current="page">关系网</span></nav>`:'';
                const nodes = g.nodes || [], edges = g.edges || [];
                lastFileMeta = {
                    project: g.project || name,
                    note: g.note || "",
                    skipped: g.skipped || "",
                    attachCount: g.attachCount || 0,
                    attachShown: g.attachShown || 0,
                    attachHidden: g.attachHidden || 0,
                    found: !(g.skipped || "").includes("找不到") && !(g.note || "").includes("找不到"),
                };
                const countEl = document.getElementById("g-count");
                if (countEl) {
                    const attachBit = expandAttach && (g.attachShown || 0) > 0 ? ` · 含 ${g.attachShown} 个其他文件夹` : "";
                    countEl.textContent = `${nodes.filter(n=>n.kind!=='agent').length} 个文件 · ${nodes.filter(n=>n.kind==='agent').length} 个来源工具 · ${edges.length} 条关系${attachBit}`;
                }
                allNodes = nodes; allEdges = edges; sel = null; positions = new Map(); hidden = new Set();
                paintBanner(g);
                if(agentStateError)noteEl.insertAdjacentHTML('beforeend','<p>AI 接入状态暂时无法读取，文件关系仍按来源记录显示。</p>');
                paintLegend();
                draw(false);
            } catch (e) {
                if(!live()||ticket!==request||tab!=='files')return;
                wrap.innerHTML = failed("文件关系网没读出来", e);
                bindRetry(wrap, () => fetchFiles());
            }
        };
        paintTools();
        await fetchFiles();
    };

    const loadWorks = async (workID=graphWorkID,offset=graphWorkOffset) => {
        const ticket=++request;
        graphWorkID=workID;graphWorkOffset=offset;allNodes=[];allEdges=[];sel=null;svg=null;
        detail.innerHTML='';listEl.innerHTML='';legend.innerHTML='';noteEl.textContent='';
        wrap.innerHTML='<div class="graph-empty" role="status">正在读取作品并核对版本内容……</div>';
        const fresh=()=>live()&&ticket===request&&tab==='works';
        try{
            if(!projectList.length){const ps=await call<any[]>('Projects');if(!fresh())return;projectList=(ps||[]).map(p=>({name:p.name,source:p.source,id:p.id,path:p.path}))}
            if(!fileProjectKey&&projectList.length)fileProjectKey=projectSelectKey(projectList.find(p=>p.source==='总文件夹')||projectList[0]);
            const parsed=parseProjectSelectKey(fileProjectKey);
            activeProjectKey=parsed.id?'place:'+parsed.id:parsed.name?'root:'+parsed.name:'';
            const currentOption=activeProjectKey&&!projectList.some(p=>projectSelectKey(p)===fileProjectKey)?`<option value="${esc(fileProjectKey)}" selected>${esc(parsed.name||'当前原地项目')}</option>`:'';
            const options=currentOption+projectList.map(p=>`<option value="${esc(projectSelectKey(p))}" ${projectSelectKey(p)===fileProjectKey?'selected':''}>${esc(p.name)}${p.source==='原地'?'（原地）':''}</option>`).join('');
            tools.innerHTML=`<div class="graph-field"><label for="g-work-project">项目</label><select id="g-work-project" class="graph-select select select-sm"><option value="" disabled ${activeProjectKey?'':'selected'}>选择项目</option>${options}</select></div><mdui-button variant="text" type="button" class="btn btn-sm" id="g-work-refresh">重新读取</mdui-button><div id="g-work-page" class="graph-work-page"></div>`;
            document.getElementById('g-work-project')!.onchange=e=>{fileProjectKey=(e.target as HTMLSelectElement).value;void loadWorks('',0)};
            document.getElementById('g-work-refresh')!.onclick=()=>void loadWorks();
            if(!activeProjectKey){draw();return}
            const g=await call<WorkGraph>('WorkGraph',activeProjectKey,workID,offset);if(!fresh())return;
            graphWorkID=g.workId||'';graphWorkOffset=g.offset;
            const projectHref=g.projectKey.startsWith('root:')?'#/project/'+encodeURIComponent(g.projectKey.slice(5)):'#/place/'+encodeURIComponent(g.projectKey.slice(6));
            document.getElementById('g-context')!.innerHTML=`<nav class="work-view-nav" aria-label="项目视图"><a href="${projectHref}">文件夹</a><a href="#/works/${encodeURIComponent(g.projectKey)}">成果</a><span aria-current="page">关系网</span></nav>`;
            const page=document.getElementById('g-work-page')!;
            page.innerHTML=`${g.workId?'<mdui-button variant="text" type="button" class="btn btn-sm" id="g-work-back">← 全部作品</mdui-button>':''}<span>${esc(g.workTitle||g.project)} · ${g.total?g.offset+1:0}–${Math.min(g.total,g.offset+g.limit)} / ${g.total} ${g.workId?'个版本':'份作品'}</span>${g.total>g.limit?`<mdui-button variant="text" type="button" class="btn btn-sm" id="g-work-prev" ${g.offset===0?'disabled':''}>上一页</mdui-button><mdui-button variant="text" type="button" class="btn btn-sm" id="g-work-next" ${g.offset+g.limit>=g.total?'disabled':''}>下一页</mdui-button>`:''}`;
            document.getElementById('g-work-back')?.addEventListener('click',()=>void loadWorks('',0));
            document.getElementById('g-work-prev')?.addEventListener('click',()=>void loadWorks(graphWorkID,g.offset-g.limit));
            document.getElementById('g-work-next')?.addEventListener('click',()=>void loadWorks(graphWorkID,g.offset+g.limit));
            setData(g.nodes||[],g.edges||[],[g.note,g.skipped].filter(Boolean).join(' '));
        }catch(e){if(!fresh())return;wrap.innerHTML=failed('作品关系没读出来',e);bindRetry(wrap,()=>loadWorks(workID,offset));noteEl.innerHTML='<mdui-button variant="text" type="button" class="btn btn-sm" id="g-work-error-back">返回作品总览</mdui-button>';document.getElementById('g-work-error-back')!.onclick=()=>void loadWorks('',0)}
    };

    let memoryCursor='',memoryRevision=0;
    const loadMemory = async (cursor='') => {
        const ticket=++request;const fresh=()=>live()&&request===ticket&&tab==='memory';
        allNodes=[];allEdges=[];sel=null;svg=null;detail.innerHTML='';listEl.innerHTML='';legend.innerHTML='';noteEl.innerHTML='';
        wrap.innerHTML='<div class="graph-empty" role="status">正在读取文件变化记录…</div>';
        try{
            if(!projectList.length){const ps=await call<any[]>('Projects');if(!fresh())return;projectList=(ps||[]).map(p=>({name:p.name,source:p.source,id:p.id,path:p.path}))}
            if(!fileProjectKey&&projectList.length)fileProjectKey=projectSelectKey(projectList[0]);
            const parsed=parseProjectSelectKey(fileProjectKey);activeProjectKey=parsed.id?'place:'+parsed.id:parsed.name?'root:'+parsed.name:'';
            const options=projectList.map(p=>`<option value="${esc(projectSelectKey(p))}" ${projectSelectKey(p)===fileProjectKey?'selected':''}>${esc(p.name)}</option>`).join('');
            tools.innerHTML=`<div class="graph-field"><label for="g-memory-project">项目</label><select id="g-memory-project" class="graph-select select select-sm">${options}</select></div><mdui-button variant="text" class="btn btn-sm" type="button" id="g-memory-refresh">最新记录</mdui-button><div id="g-memory-page" class="graph-work-page"></div>`;
            document.getElementById('g-memory-project')!.onchange=e=>{fileProjectKey=(e.target as HTMLSelectElement).value;void loadMemory()};
            document.getElementById('g-memory-refresh')!.onclick=()=>void loadMemory();
            if(!activeProjectKey){setData([],[],'先选择项目，再查看已保存的文件变化。');return}
            if(!cursor){const state=await call<{revision:number}>('ProjectMemory',activeProjectKey);if(!fresh())return;memoryRevision=state.revision}
            const g=await call<{project:string;revision:number;next:string;nodes:GNode[];edges:GEdge[];note:string}>('ProjectMemoryGraph',activeProjectKey,memoryRevision,cursor);if(!fresh())return;
            memoryCursor=g.next;
            document.getElementById('g-context')!.innerHTML=`<nav class="work-view-nav" aria-label="项目视图"><a href="#/memory/${encodeURIComponent(activeProjectKey)}">← ${esc(g.project)}的文件记录</a><span aria-current="page">关系网</span></nav>`;
            document.getElementById('g-memory-page')!.innerHTML=`<span>${cursor?'较早的':'最近的'}文件变化 · 记录版本 ${g.revision}</span>${memoryCursor?'<mdui-button variant="text" type="button" class="btn btn-sm" id="g-memory-more">更早的记录</mdui-button>':''}`;
            document.getElementById('g-memory-more')?.addEventListener('click',()=>void loadMemory(memoryCursor));
            setData(g.nodes||[],g.edges||[],g.note);
            if(!g.nodes.length)wrap.innerHTML=`<div class="graph-empty"><h2>这一页还没有文件变化</h2><p>到<a href="#/memory/${encodeURIComponent(activeProjectKey)}">文件记录</a>检查一次，或查看更早的记录。</p></div>`;
        }catch(e){if(!fresh())return;wrap.innerHTML=failed('文件变化没读出来',e);bindRetry(wrap,()=>loadMemory());}
    };

    const reload = () => {
        if(!live())return;
        svg=null;allNodes=[];allEdges=[];sel=null;
        detail.innerHTML='';legend.innerHTML='';listEl.innerHTML='';noteEl.innerHTML='';
        document.getElementById('g-context')!.innerHTML='';
        syncTabs();
        saveGraphTab(tab);
        listQ.value = "";
        if (tab === "projects") return loadProjects();
        if (tab === "works") return loadWorks();
        if (tab === "memory") return loadMemory();
        return loadFiles();
    };

    view.querySelectorAll<HTMLButtonElement>("[data-gtab]").forEach(b => {
        b.onclick = () => { tab = b.dataset.gtab as GraphTab; reload(); };
    });
    enhance(view);
    // Focusing an inactive window happens between pointerdown and pointerup.
    // Replacing the SVG there loses the user's click and its pointer capture.
    let pointerActive=false;
    const onPointerDown=()=>{pointerActive=true};
    const onPointerEnd=()=>{pointerActive=false};
    const onFocus=()=>{if(live()&&!pointerActive)void reload()};
    window.addEventListener('pointerdown',onPointerDown,true);
    window.addEventListener('pointerup',onPointerEnd);
    window.addEventListener('pointercancel',onPointerEnd);
    window.addEventListener('focus',onFocus);
    detail.onkeydown=e=>{if(e.key==='Escape'){e.preventDefault();select(null)}};
    disposeGraph=()=>{request++;window.clearTimeout(projectSearchTimer);window.removeEventListener('focus',onFocus);window.removeEventListener('keydown',onKey);window.removeEventListener('pointerdown',onPointerDown,true);window.removeEventListener('pointerup',onPointerEnd);window.removeEventListener('pointercancel',onPointerEnd)};
    await reload();
}

let disposeMemory: (()=>void)|undefined;
let disposeAnalysis: (()=>void)|undefined;
let disposeWorks: (()=>void)|undefined;
let disposeAgents: (()=>void)|undefined;
let disposeGraph: (()=>void)|undefined;
let disposeNewProject: (()=>void)|undefined;
let disposeModels: (()=>void)|undefined;
export const routes: Record<string, (arg: string) => void | Promise<void>> = {
    "": () => hasStartChoice()?renderProjects():mountWelcome(view,call,esc),
    "welcome":()=>mountWelcome(view,call,esc),
    "new-project":()=>{disposeNewProject=mountNewProject(view,call,esc,(html,ok)=>confirmBox(html,ok));},
    "models":()=>{disposeModels=mountModelCenter(view,call,esc,(html,ok)=>confirmBox(html,ok));},
    "organize": (key) => openOrganizer(key),
    "recent": () => renderHome(),
    "projects": () => renderProjects(),
    "project": (n) => renderProject(n),
    "place": (id) => renderPlace(id),
    "memory": (key) => {disposeMemory=mountProjectMemory(view,key,call,esc,(html,ok)=>confirmBox(html,ok));},
    "analysis": (key) => {disposeAnalysis=mountProjectAnalysis(view,key,call,esc,(html,ok)=>confirmBox(html,ok));},
    "analysis-batch": (arg) => {disposeAnalysis=mountProjectAnalysisBatch(view,arg,call,esc,(html,ok)=>confirmBox(html,ok));},
    "placement": (arg) => {disposeAnalysis=mountProjectPlacement(view,arg,call,esc);},
    "works": (arg) => {const [key,workID='']=arg.split('/');disposeWorks = mountWorks(view, key, call, esc, (html, ok) => confirmBox(html, ok),workID); },
    "cleanup": () => renderCleanup(),
    "graph": (key) => renderGraph(key),
    "agents": (agent) => { disposeAgents = mountAgentEnrollment(view, call, esc, (html, ok, onlyOK) => confirmBox(html, ok, onlyOK), agent); },
    "settings": () => renderSettings(),
};

function route() {
    disposeAnalysis?.();disposeAnalysis=undefined;
    disposeModels?.();disposeModels=undefined;
    disposeNewProject?.();disposeNewProject=undefined;
    disposeMemory?.(); disposeMemory=undefined;
    disposeWorks?.(); disposeWorks = undefined;
    disposeAgents?.(); disposeAgents = undefined;
    disposeGraph?.(); disposeGraph = undefined;
    routeEpoch++;
    selected.clear(); lastVisiblePaths = []; fileSelecting = false; fileLoading = false;
    view.classList.remove("organize-view", 'file-browser', 'file-selecting');
    const h = location.hash.replace(/^#\/?/, "");
    const [k, ...rest] = h.split("/");
    const arg = decodeURIComponent(rest.join("/"));
    document.querySelectorAll<HTMLAnchorElement>("[data-nav]").forEach(a => {
        const n = a.dataset.nav;
        const on = n === k || (n === "projects" && (k === "" && hasStartChoice() || ["project","place","works","memory","analysis","analysis-batch","placement","new-project"].includes(k))) || (n === 'settings' && k === 'models');
        a.classList.toggle("on", on);
        if (on) a.setAttribute("aria-current", "page"); else a.removeAttribute("aria-current");
        if (on && a.closest("details")) a.closest("details")!.open = true;
    });
    view.scrollTop = 0;
    (routes[k] || routes[""])(arg);
}

async function boot() {
    enhance(document.querySelector<HTMLElement>(".side")!);
    try {
        const info = await call<any>("Info");
        rootDir = info.root;
        home = typeof info.home === 'string' ? info.home : '';
        osName = info.os;
        foot.textContent = `v${info.version} · ${info.os}/${info.arch}`;
    } catch (e) {
        foot.textContent = "后端没连上";
    }
    await refreshInstallBanner();
    const accessBanner = document.getElementById('access-banner')!;
    window.addEventListener('workshelf:access-status', (event:Event) => {
        const folders=(event as CustomEvent<{name:string;status:string}[]>).detail;
        const blocked=folders.filter(f=>f.status==='无法访问');
        accessBanner.hidden=!blocked.length;
        accessBanner.innerHTML=blocked.length?`无法访问 ${blocked.map(f=>esc(f.name)).join('、')}，项目识别可能不完整。 <a href="#/settings">查看文件访问权限</a>`:'';
    });
    const refreshAccess=()=>{if(accessWasChecked()) void checkAccess(call).catch(()=>{accessBanner.hidden=false;accessBanner.textContent='无法检查文件访问权限，请到设置中重新检查。';});};
    window.addEventListener('focus',refreshAccess);
    refreshAccess();
    window.addEventListener("hashchange", route);
    route();
}
boot();
