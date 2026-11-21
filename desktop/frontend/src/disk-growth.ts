import "./disk-growth.css";

type Call = <T>(method: string, ...args: unknown[]) => Promise<T>;
type Escape = (value: unknown) => string;
type Bucket = {
  category: string;
  path: string;
  project?: string;
  app?: string;
  bytes: number;
};
type Issue = { path: string; reason: string };
type Entry = {
  started: string;
  finished: string;
  trigger: string;
  planned?: string;
  root: string;
  free: number;
  files: number;
  buckets?: Bucket[];
  issues?: Issue[];
  issueCount: number;
  error?: string;
};
type Prefs = { enabled: boolean; at: string; days: number };
type RecordInfo = { id: string; started: string; trigger: string };
type Data = {
  prefs: Prefs;
  records: RecordInfo[];
  Current: Entry | null;
  Previous: Entry | null;
  free: number;
  root: string;
  running: boolean;
  error?: string;
};
type Detail = {
  path: string;
  project: string;
  app: string;
  before: number;
  now: number;
  uncertain: boolean;
};
const triggerName: Record<string, string> = {
  manual: "手动采样",
  daily: "定时采样",
  catchup: "错过后补采",
};
export const instant = (value: string) =>
  new Date(value).toLocaleString("zh-CN", { hour12: false });
export function diskBytes(n: number) {
  const units = ["B", "KiB", "MiB", "GiB", "TiB"];
  let i = 0,
    v = Math.abs(n);
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return (
    (n < 0 ? "−" : "") +
    v.toLocaleString("zh-CN", { maximumFractionDigits: i ? 2 : 0 }) +
    " " +
    units[i]
  );
}
function cleanPath(path: string) {
  return path.replace(/^\/System\/Volumes\/Data(?=\/)/, "");
}
export function compare(current: Entry, previous: Entry | null) {
  const groups = new Map<string, Map<string, Detail>>();
  const issues = [...(current.issues || []), ...(previous?.issues || [])].map(
    (i) => i.path.replace(/\\/g, "/"),
  );
  for (const [sample, col] of [
    [previous, "before"],
    [current, "now"],
  ] as const) {
    for (const b of sample?.buckets || []) {
      let group = groups.get(b.category);
      if (!group) {
        group = new Map();
        groups.set(b.category, group);
      }
      let row = group.get(b.path);
      if (!row) {
        const p = b.path.replace(/\\/g, "/");
        const uncertain = issues.some(
          (x) =>
            p === x ||
            p.startsWith(x + "/") ||
            x.startsWith(p.endsWith("/") ? p : p + "/"),
        );
        row = {
          path: b.path,
          project: "",
          app: "",
          before: 0,
          now: 0,
          uncertain,
        };
        group.set(b.path, row);
      }
      row[col] += b.bytes;
      row.project = b.project || row.project;
      row.app = b.app || row.app;
    }
  }
  return groups;
}

export function mountDiskGrowth(view: HTMLElement, call: Call, esc: Escape) {
  let data: Data | undefined,
    chosen = "",
    filter = "all",
    query = "",
    active = true,
    timer = 0,
    seq = 0;
  const opened = new Set<string>();
  const expanded = new Set<string>();
  view.innerHTML = `<header class="bar"><div><h1>磁盘增长表</h1><p class="sub">按实际占用查看磁盘空间的变化</p></div><button id="dg-sample" class="btn btn-primary" type="button">立即采样</button></header>
 <section class="dg"><div id="dg-status" role="status" aria-live="polite"></div><div id="dg-summary"></div>
 <div class="dg-controls"><label>采样记录 <select id="dg-record"></select></label><label>变化 <select id="dg-filter"><option value="all">全部</option><option value="up">净增加</option><option value="down">净减少</option></select></label>
 <label>搜索 <input id="dg-search" type="search" placeholder="项目名或目录路径"></label></div>
 <div id="dg-table"></div><div id="dg-coverage"></div>
 <details class="dg-settings"><summary>每日采样与保留时间</summary><form id="dg-form"><label><input type="checkbox" id="dg-enabled"> 每日自动采样</label><label>本地时间 <input type="time" required id="dg-at"></label><label>保留天数 <select id="dg-days">${Array.from({ length: 9 }, (_, i) => `<option>${i + 7}</option>`).join("")}</select></label><button class="btn" type="submit">保存</button></form>
<p class="sub">开启后由系统在登录期间定时采样。当天错过时，序屿下次运行会补采一次。保留期按本地日期计算，运行时自动移除过期采样。</p></details></section>`;
  const el = <T extends HTMLElement>(id: string) =>
    view.querySelector<T>("#dg-" + id)!;
  const status = el("status"),
    sample = el<HTMLButtonElement>("sample"),
    record = el<HTMLSelectElement>("record"),
    enabled = el<HTMLInputElement>("enabled"),
    at = el<HTMLInputElement>("at"),
    days = el<HTMLSelectElement>("days");
  function draw() {
    if (!data) return;
    const cur = data.Current,
      prev = data.Previous,
      holder = el("table");
    el("summary").innerHTML =
      `<p class="dg-space">磁盘可用 <strong>${diskBytes(data.free)}</strong></p>` +
      (cur && !cur.error
        ? `<p>${prev ? "上次 " + esc(instant(prev.finished)) + " → 本次 " : "基线 · "}${esc(instant(cur.finished))}</p><p class="sub">${esc(triggerName[cur.trigger])} · 开始 ${esc(instant(cur.started))}${cur.planned ? " · 计划 " + esc(cur.planned) : ""} · ${cur.files.toLocaleString()} 个文件 · 当时可用 ${diskBytes(cur.free)}</p>`
        : "");
    const issues = (e: Entry) =>
      `<p>${esc(instant(e.finished))} · ${e.issueCount} 个读取问题</p><ul>${(
        e.issues || []
      )
        .slice(0, 200)
        .map((i) => `<li>${esc(i.reason)} · ${esc(cleanPath(i.path))}</li>`)
        .join(
          "",
        )}</ul>${(e.issues || []).length > 200 ? "只列前 200 项。" : ""}`;
    el("coverage").innerHTML =
      `<p class="sub">范围：${esc(data.root)} 所在卷，可读取的普通文件。按分配空间统计，共享硬链接只计一次。路径无法读取时，相应变化标为“范围不完整”。</p>` +
      [cur, prev]
        .filter((e): e is Entry => !!e && e.issueCount > 0)
        .map(
          (e) =>
            `<details><summary>查看读取限制（${e.issueCount}）</summary>${issues(e)}</details>`,
        )
        .join("");
    if (!cur || cur.error) {
      holder.innerHTML = cur
        ? `<p role="alert">${esc(cur.error)} · ${esc(instant(cur.started))} · ${esc(triggerName[cur.trigger])}</p>`
        : "<p>还没有采样记录。点击「立即采样」建立基线，第二次采样后就能看到哪些目录变大或变小。</p>";
      return;
    }
    let html = "";
    const delta = (r: Detail) => r.now - r.before;
    const groups = Array.from(compare(cur, prev), ([name, items]) => {
      const rows = Array.from(items.values()).filter((r) =>
        [r.path, r.project, r.app].join(" ").toLowerCase().includes(query),
      );
      return {
        name,
        rows,
        now: rows.reduce((a, b) => a + b.now, 0),
        before: rows.reduce((a, b) => a + b.before, 0),
        partial: rows.some((r) => r.uncertain),
      };
    })
      .filter((g) => g.rows.length > 0)
      .sort(
        (a, b) =>
          Number(a.partial) - Number(b.partial) ||
          (prev ? b.now - b.before - (a.now - a.before) : b.now - a.now) ||
          a.name.localeCompare(b.name),
      );
    const change = (n: number, partial: boolean) =>
      !prev ? "—" : partial ? "范围不完整" : (n > 0 ? "+" : "") + diskBytes(n);
    for (const g of groups) {
      const diff = g.now - g.before;
      if (
        filter !== "all" &&
        (!prev || g.partial || (filter === "down" ? diff >= 0 : diff <= 0))
      )
        continue;
      const open = opened.has(g.name) || !!query;
      html += `<tr><th scope="row"><button type="button" data-category="${esc(g.name)}" aria-expanded="${open}">${open ? "▾" : "▸"} ${esc(g.name)}</button></th><td>${prev ? diskBytes(g.before) : "—"}</td><td>${diskBytes(g.now)}</td><td>${change(diff, g.partial)}</td></tr>`;
      if (open) {
        g.rows.sort(
          (a, b) =>
            Number(a.uncertain) - Number(b.uncertain) ||
            (prev
              ? filter === "down"
                ? delta(a) - delta(b)
                : Math.abs(delta(b)) - Math.abs(delta(a))
              : b.now - a.now),
        );
        const max = expanded.has(g.name) ? g.rows.length : 25;
        for (const r of g.rows.slice(0, max))
          html += `<tr class="dg-dir"><td>${esc(cleanPath(r.path))}<small>${esc([r.project, r.app].filter(Boolean).join(" · ") || "归属未知")}</small></td><td>${prev ? diskBytes(r.before) : "—"}</td><td>${diskBytes(r.now)}</td><td>${change(delta(r), r.uncertain)}</td></tr>`;
        if (g.rows.length > max)
          html += `<tr><td colspan="4"><button type="button" data-more="${esc(g.name)}">查看全部 ${g.rows.length} 个目录</button></td></tr>`;
      }
    }
    holder.innerHTML = html
      ? `<table><thead><tr><th>类别 / 目录</th><th>上次占用</th><th>本次占用</th><th>净变化</th></tr></thead><tbody>${html}</tbody></table>`
      : "<p>没有符合条件的变化。</p>";
  }
  async function refresh(initial = false) {
    const n = ++seq;
    clearTimeout(timer);
    try {
      const next = await call<Data>("DiskGrowth", chosen);
      if (!active || n !== seq) return;
      data = next;
      if (initial) {
        enabled.checked = data.prefs.enabled;
        at.value = data.prefs.at;
        days.value = String(data.prefs.days);
      }
      record.innerHTML =
        '<option value="">最近一次</option>' +
        data.records
          .map(
            (r) =>
              `<option value="${esc(r.id)}">${esc(instant(r.started))} · ${esc(triggerName[r.trigger])}</option>`,
          )
          .join("");
      record.value = chosen;
      if (data.running || !status.dataset.saved)
        status.textContent =
          data.error ||
          (data.running
            ? "正在统计磁盘占用，可离开此页面。"
            : data.prefs.enabled
              ? `每日 ${data.prefs.at} · 保留 ${data.prefs.days} 天`
              : "每日采样尚未开启");
      sample.disabled = data.running;
      draw();
    } catch (e) {
      if (active && n === seq) {
        status.textContent = String(e);
        if (chosen) {
          chosen = "";
          void refresh();
          return;
        }
      }
    }
    if (active && n === seq)
      timer = window.setTimeout(
        () => void refresh(),
        data?.running ? 3000 : 60000,
      );
  }
  record.onchange = () => {
    chosen = record.value;
    void refresh();
  };
  el<HTMLSelectElement>("filter").onchange = (e) => {
    filter = (e.target as HTMLSelectElement).value;
    draw();
  };
  el<HTMLInputElement>("search").oninput = (e) => {
    query = (e.target as HTMLInputElement).value.trim().toLowerCase();
    draw();
  };
  el("table").onclick = (e) => {
    const btn = (e.target as HTMLElement).closest("button");
    if (!btn) return;
    const key = btn.dataset.category;
    if (key) {
      opened.has(key) ? opened.delete(key) : opened.add(key);
    } else if (btn.dataset.more) {
      expanded.add(btn.dataset.more);
    }
    draw();
  };
  sample.onclick = async () => {
    sample.disabled = true;
    delete status.dataset.saved;
    try {
      await call("SampleDiskGrowth");
      chosen = "";
      if (active) await refresh();
    } catch (e) {
      if (active) {
        status.textContent = String(e);
        sample.disabled = false;
      }
    }
  };
  el<HTMLFormElement>("form").onsubmit = async (e) => {
    e.preventDefault();
    try {
      await call("SetDiskGrowth", {
        enabled: enabled.checked,
        at: at.value,
        days: Number(days.value),
      });
      if (active) {
        status.textContent = enabled.checked
          ? `已保存 · 每日 ${at.value} · 保留 ${days.value} 天`
          : `已关闭自动采样 · 保留 ${days.value} 天`;
        status.dataset.saved = "1";
        await refresh();
      }
    } catch (err) {
      if (active) status.textContent = String(err);
    }
  };
  void refresh(true);
  return () => {
    active = false;
    clearTimeout(timer);
  };
}
