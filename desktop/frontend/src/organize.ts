import "./organize.css";
import {mountSettings, type Profile} from "./organize-settings";
import {mountBrowser} from "./organize-browser";
import {organizerAction as action} from "./ui-actions";
import {mountStructure, selectedFiles, type PreviewFile} from "./organize-structure";
import {mountResultFiles, type OrganizeResult as Result} from './organize-result';

type API = <T>(method: string, ...args: unknown[]) => Promise<T>;
type Escape = (s: unknown) => string;
type Hint = {label: string; reason: string; confidence: string; preConfirm: boolean; alreadyInReadme: boolean};
type Context = {
    project: string; root: string; sources: string[]; warnings: string[];
    projects: {key: string; name: string}[]; inferred: Hint[];
    workTypes: {label: string; layout: {name: string; folders: string[]; roles?: Record<string,string>}}[];
    profile?: Profile; templates?: Profile[];
};
type Row = PreviewFile;
type Plan = {token: string; project: string; target: string; folders: string[]; rows: Row[]; warnings: string[];merge?:boolean;mergedFolders?:number};
type Undo = {restored: number; quarantined: number; failed: number; skipped: number; unchanged?: number; notes?: string[]};
let organizerGeneration = 0;
export type OrganizerDraft = {project: string; sources: string[]};

// One draft across steps; the existing preview/token/confirmation boundary is retained.
export async function renderOrganizer(key: string, api: API, esc: Escape, confirm: (html: string, ok: string, dismissLabel?: string) => Promise<boolean>, draft?: OrganizerDraft) {
    const view = document.getElementById("view")!;
    view.classList.add('organize-view');
    const route = location.hash;
    const generation = ++organizerGeneration;
    const current = () => generation === organizerGeneration && location.hash === route && view.querySelector("#organizer") !== null;
    const message = (e: unknown) => e instanceof Error ? e.message : String(e);
    view.innerHTML = `<div id="organizer" class="organizer"><h1>整理文件</h1><p role="status">正在读取项目……</p></div>`;
    let ctx: Context;
    try { ctx = await api<Context>("OrganizeContext", draft ? 'root:'+draft.project : key); }
    catch (e) {
        if (current()) {
            view.innerHTML = `<div id="organizer" class="organizer"><h1>整理文件</h1><p role="alert">${esc(message(e))}</p>${action('organize-retry','重新读取',true)}</div>`;
            view.querySelector<HTMLButtonElement>("#organize-retry")!.onclick = () => renderOrganizer(key, api, esc, confirm, draft);
        }
        return;
    }
    if (!current()) return;
    if (draft) {
        ctx = {...ctx, project:draft.project, sources:[...new Set(draft.sources)]};
        key = ctx.projects.find(p=>p.key==='root:'+draft.project)?.key || ctx.projects.find(p=>p.name===draft.project)?.key || '';
    }
    view.innerHTML = `<div id="organizer" class="organizer organizer-simple">
      <ol class="organize-steps" aria-label="整理进度"><li data-step="1" aria-current="step">1 选文件</li><li data-step="2">2 确认项目</li><li data-step="3">3 看去向</li></ol>
      <header class="organize-heading"><h1 tabindex="-1" id="organize-heading">先选好要整理的文件</h1><p id="organize-intro">同一个项目的文件，可以从不同位置一起加入。</p></header>
      <div id="organize-scan-warnings">${(ctx.warnings || []).map(w => `<p class="warn">${esc(w)}</p>`).join("")}</div>
      <form id="organize-form" class="organize-form" novalidate>
        <section id="organize-step-files" aria-label="选择文件">
          <div class="organize-project-pick"><label for="organize-project-choice">从已找到的项目开始</label><div class="organize-inline"><select class="select" id="organize-project-choice"><option value="">选择项目，或自己添加文件</option>${(ctx.projects || []).map(p=>`<option value="${esc(p.key)}" ${p.key===key?'selected':''}>${esc(p.name)}</option>`).join('')}</select><mdui-button variant="text" class="btn btn-sm btn-ghost" id="organize-scan" type="button">${key?'查找散落文件':'查找项目'}</mdui-button></div></div>
          <div class="organize-source-head"><span id="organize-source-count">待整理的文件</span>${action('organize-choose','添加文件或文件夹')}</div>
          <ul id="organize-source-list" class="organize-source-list" aria-label="已选来源"></ul>
          <div class="organize-source-tools"><details id="organize-paths"><summary>粘贴路径</summary><label for="organize-sources">每行一个文件或文件夹路径</label><textarea class="textarea" id="organize-sources" rows="3" spellcheck="false" placeholder="~/Desktop/品牌改版">${esc(ctx.sources.join('\n'))}</textarea></details><div id="organize-file-browser"></div></div>
          <footer class="organize-step-actions"><span class="sub">下一步确认项目与工种，文件暂时不会移动</span>${action('organize-next','下一步',true)}</footer>
        </section>
        <section id="organize-step-worktype" aria-label="确认项目与工种" hidden>
          <div class="organize-name-field"><label for="organize-name">项目名称</label><input class="input" id="organize-name" value="${esc(ctx.project)}" placeholder="给这组文件起个名字" autocomplete="off"></div>
          <div id="organize-settings"></div>
          <footer class="organize-step-actions">${action('organize-back-files','上一步')}${action('organize-preview','看看整理后的样子',true)}</footer>
        </section>
      </form>
      <section id="organize-preview-area" aria-label="整理预览" hidden></section>
      <section id="organize-result" aria-label="整理结果" aria-live="polite" hidden></section>
      <p id="organize-status" class="organize-status" role="status" aria-live="polite"></p>
      <mdui-button variant="text" class="btn btn-sm btn-ghost" id="organize-cancel-preview" type="button" hidden>取消比较</mdui-button>
    </div>`;
    const root = view.querySelector<HTMLElement>('#organizer')!;
    const form = root.querySelector<HTMLFormElement>('#organize-form')!;
    const status = root.querySelector<HTMLElement>('#organize-status')!;
    const output = root.querySelector<HTMLElement>('#organize-preview-area')!;
    const result = root.querySelector<HTMLElement>('#organize-result')!;
    const sourcesInput = root.querySelector<HTMLTextAreaElement>('#organize-sources')!;
    const nameInput = root.querySelector<HTMLInputElement>('#organize-name')!;
    let step = 1, busy = false, sourcesChanged = !!draft, settingsEdited = false;
    let plan: Plan | null = null;
    let structure: ReturnType<typeof mountStructure> | null = null;
    let nextStructureView = '';
    const corrections: Record<string,string> = {};
    const choices = new Map<string,boolean>();
    const disabled = new Map<HTMLInputElement | HTMLButtonElement | HTMLTextAreaElement | HTMLSelectElement,boolean>();
    const sources = () => [...new Set(sourcesInput.value.split('\n').map(s=>s.trim()).filter(Boolean))];
    let previousSources = new Set(sources());
    const removedSources = new Set<string>();
    const basename = (p: string) => p.replace(/\/$/,'').split('/').pop() || p;
    function invalidate() {
        plan = null;
        const apply = output.querySelector<HTMLButtonElement>('#organize-apply');
        if (apply) { apply.disabled = true; status.textContent = '去向有变化，更新预览后再整理。'; }
        output.querySelector('#organize-repreview')?.classList.add('btn-primary');
    }
    const settingsChanged = () => { settingsEdited = true; for(const p of Object.keys(corrections))delete corrections[p]; invalidate(); };
    let settings = mountSettings(root.querySelector('#organize-settings')!,ctx,api,esc,settingsChanged,confirm);
    function showStep(next: number) {
        step = next; form.hidden = next > 2;
        root.querySelector<HTMLElement>('#organize-step-files')!.hidden = next !== 1;
        root.querySelector<HTMLElement>('#organize-step-worktype')!.hidden = next !== 2;
        output.hidden = next !== 3; result.hidden = next !== 4;
        root.querySelector<HTMLElement>('.organize-steps')!.hidden = next === 4;
        root.querySelectorAll<HTMLElement>('[data-step]').forEach(el=>{if(Number(el.dataset.step)===next)el.setAttribute('aria-current','step');else el.removeAttribute('aria-current')});
        root.querySelector('#organize-heading')!.textContent = ['','先选好要整理的文件','确认项目与工种','整理后的目录预览','文件整理好了'][next];
        root.querySelector('#organize-intro')!.textContent = ['','同一个项目的文件，可以从不同位置一起加入。','我们会按工种准备文件夹，你也可以自己决定。','左边看目录，点开文件夹检查里面的文件。确认后才会移动。','可以打开项目查看，也可以撤销刚才的整理。'][next];
        view.scrollTop = 0; root.querySelector<HTMLElement>('#organize-heading')!.focus({preventScroll:true});
    }
    function renderSources() {
        const list = sources();
        root.querySelector('#organize-source-count')!.textContent = list.length ? `已加入 ${list.length} 个来源` : '待整理的文件';
        root.querySelector('#organize-source-list')!.innerHTML = list.map((path,i)=>`<li><div><strong>${esc(basename(path))}</strong><span class="sub organize-source-path" title="${esc(path)}">${esc(path)}</span></div><mdui-button variant="text" class="btn btn-sm btn-ghost" type="button" data-remove-source="${i}" aria-label="移除 ${esc(basename(path))}">移除</mdui-button></li>`).join('') || '<li class="organize-empty">先添加文件或文件夹，也可以选择上面找到的项目。</li>';
        root.querySelectorAll<HTMLButtonElement>('[data-remove-source]').forEach(el=>el.onclick=()=>{const next=sources();next.splice(Number(el.dataset.removeSource),1);sourcesInput.value=next.join('\n');sourceChanged()});
        root.querySelector<HTMLButtonElement>('#organize-next')!.disabled = busy || !list.length;
        root.querySelector('#organize-choose')!.classList.toggle('btn-primary',!list.length);
    }
    function sourceChanged() {
        const next = new Set(sources());
        for(const path of previousSources)if(!next.has(path))removedSources.add(path);
        for(const path of next)if(!previousSources.has(path))removedSources.delete(path);
        previousSources=next;
        // Keep decisions keyed by source; only rows in the next server preview
        // can execute. Adding newly found files must not reset earlier choices.
        sourcesChanged = true; invalidate(); renderSources();
    }
    function addSources(paths: string[]) { if(!paths.length)return; sourcesInput.value=[...new Set([...sources(),...paths])].join('\n');sourceChanged(); }
    function setBusy(value: boolean) {
        busy = value;
        if(value){root.querySelectorAll<HTMLInputElement | HTMLButtonElement | HTMLTextAreaElement | HTMLSelectElement>('input,mdui-button,textarea,select').forEach(el=>{disabled.set(el,el.disabled);el.disabled=true})}
        else {for(const [el,was] of disabled)if(el.isConnected)el.disabled=was;disabled.clear();root.querySelector<HTMLButtonElement>('#organize-next')!.disabled=!sources().length;}
    }
    mountBrowser(root.querySelector('#organize-file-browser')!,api,esc,confirm,path=>{if(!busy)addSources([path])});
    sourcesInput.oninput = sourceChanged; nameInput.oninput = invalidate;
    root.querySelector<HTMLButtonElement>('#organize-choose')!.onclick = async()=>{
        if(busy)return;setBusy(true);status.textContent='';
        try{const picked=await api<string[] | null>('OrganizeChooseSources');if(current())addSources(picked||[])}catch(e){if(current())status.textContent=message(e)}finally{if(current())setBusy(false)}
    };
    root.querySelector<HTMLSelectElement>('#organize-project-choice')!.onchange = e=>{const value=(e.target as HTMLSelectElement).value;location.hash=value?`#/organize/${encodeURIComponent(value)}`:'#/organize'};
    root.querySelector<HTMLButtonElement>('#organize-scan')!.onclick = async()=>{
        if(busy)return;setBusy(true);status.textContent=key?'正在查找这个项目的散落文件……':'正在查找项目……';
        try{
            let scan=await api<{running:boolean;error?:string}>('RegistryRefresh');if(!current())return;
            // Startup may already own a background scan. Wait for its result
            // instead of claiming an old registry is the freshly found set.
            for(let attempt=0;scan.running&&attempt<60;attempt++){
                await new Promise(resolve=>setTimeout(resolve,500));if(!current())return;
                scan=await api<{running:boolean;error?:string}>('RegistryStatus');if(!current())return;
            }
            if(scan.running)throw new Error('查找仍在进行，稍后可以重试。已加入的文件保持不变。');
            if(scan.error)throw new Error(scan.error);
            const next=await api<Context>('OrganizeContext',key);if(!current())return;
            const select=root.querySelector<HTMLSelectElement>('#organize-project-choice')!;select.innerHTML=`<option value="">选择项目，或自己添加文件</option>${next.projects.map(p=>`<option value="${esc(p.key)}">${esc(p.name)}</option>`).join('')}`;select.value=key;
            root.querySelector('#organize-scan-warnings')!.innerHTML=(next.warnings||[]).map(w=>`<p class="warn">${esc(w)}</p>`).join('');
            if(key){
                const existing=new Set(sources());
                const found=[...new Set(next.sources||[])].filter(path=>!existing.has(path)&&!removedSources.has(path));
                const added=found.slice(0,Math.max(0,64-existing.size));
                if(added.length)addSources(added);
                status.textContent=added.length?`找到 ${added.length} 处相关文件，已加入本次整理。`:'没有找到新的相关文件，已选内容保持不变。';
                if(found.length>added.length)status.textContent+=` 本次最多同时整理 64 个文件或文件夹，另有 ${found.length-added.length} 处可在下一次加入。`;
                if((next.sources||[]).some(path=>removedSources.has(path)))status.textContent+=' 你移除过的来源保持不选。';
            }else status.textContent=`找到 ${next.projects.length} 个项目，可以选择一个开始整理。已加入的文件保持不变。`;
        }catch(e){if(current())status.textContent=message(e)}finally{if(current())setBusy(false)}
    };
    async function nextStep() {
        if(busy || !sources().length)return;
        setBusy(true);status.textContent='';
        try {
            if(sourcesChanged || !ctx.sources.length){
                status.textContent='正在看看这些文件适合哪种工作……';
                const suggestion=await api<{project:string;inferred:Hint[]}>('OrganizeSuggest',sources());
                if(!current())return;
                if(!nameInput.value.trim())nameInput.value=suggestion.project;
                if(!settingsEdited)settings=mountSettings(root.querySelector('#organize-settings')!,{...ctx,inferred:suggestion.inferred},api,esc,settingsChanged,confirm);
                sourcesChanged=false;
            }
            status.textContent='';showStep(2);
        }catch(e){if(current())status.textContent=message(e)}finally{if(current())setBusy(false)}
    }
    root.querySelector<HTMLButtonElement>('#organize-next')!.onclick=nextStep;
    root.querySelector<HTMLButtonElement>('#organize-back-files')!.onclick=()=>{status.textContent='';showStep(1)};
    root.querySelector<HTMLButtonElement>('#organize-preview')!.onclick=buildPreview;
    form.onsubmit=e=>{e.preventDefault();if(step===1)void nextStep();else if(step===2)void buildPreview()};
    async function buildPreview() {
        if(busy)return;const selection=settings.get();
        if(!nameInput.value.trim()){showStep(2);status.textContent='先给这个项目起个名字。';nameInput.focus();return}
        if(!selection.profile.folders.length){status.textContent='请在自定义设置中添加文件夹，或选一个模板。';showStep(2);root.querySelector<HTMLDetailsElement>('#organize-customize')!.open=true;return}
        const previewId=crypto.randomUUID(),request={project:nameInput.value.trim(),sources:sources(),...selection,folders:corrections,merge:true,previewId};
        plan=null;setBusy(true);status.textContent='正在比较文件内容和目录，生成整理预览……';
        let cancelled=false;
        const cancel=root.querySelector<HTMLButtonElement>('#organize-cancel-preview')!;cancel.hidden=false;cancel.disabled=false;
        const stop=()=>{cancelled=true;void api('OrganizeCancelPreview',previewId).catch(()=>{});if(current()){cancel.disabled=true;status.textContent='正在结束本次比较，文件不会移动……'}};
        cancel.onclick=stop;window.addEventListener('hashchange',stop,{once:true});
        try{const next=await api<Plan>('OrganizePreview',request);if(!current()||cancelled)return;plan=next;renderPreview(next);showStep(3);status.textContent=next.warnings.length?'预览已生成，部分内容仍保留原处，请查看提示。':`已检查 ${next.rows.length} 项，文件还在原处。`}
        catch(e){if(current()){status.textContent=cancelled?'已取消本次预览，来源和设置已保留。':message(e);const apply=output.querySelector<HTMLButtonElement>('#organize-apply');if(apply){apply.disabled=true;disabled.set(apply,true)}}}
        finally{window.removeEventListener('hashchange',stop);cancel.hidden=true;if(current()){setBusy(false);if(cancelled)status.textContent='已取消本次预览，来源和设置已保留。'}}
    }
    function renderPreview(p: Plan) {
        const active=nextStructureView || structure?.active() || 'overview';
        nextStructureView='';
        output.innerHTML=`${(p.warnings||[]).map(w=>`<p class="warn">${esc(w)}</p>`).join('')}<div id="organize-structure"></div>
          <footer class="organize-step-actions">${action('organize-back-settings','上一步')}<div>${action('organize-repreview','更新预览')}${action('organize-apply','确认整理',true)}</div></footer>`;
        const apply=output.querySelector<HTMLButtonElement>('#organize-apply')!;
        const ids=()=>selectedFiles(p,choices).map(row=>row.id);
        const duplicateCount=()=>{const chosen=new Set(selectedFiles(p,choices).map(r=>r.source));return p.rows.filter(r=>r.operation==='duplicate'&&(r.representative===r.target||chosen.has(r.representative||''))).length};
        const selection=()=>{const n=ids().length;apply.disabled=!plan||(!n&&!duplicateCount());apply.textContent=n?`开始整理 ${n} 项`:duplicateCount()?'完成检查，重复件留在原处':'没有需要移动的文件'};
        structure=mountStructure(output.querySelector('#organize-structure')!,p,choices,esc,selection,(rows,folder,refresh)=>{
            settingsEdited=true;
            for(const row of rows){if(folder)corrections[row.source]=folder;else delete corrections[row.source]}
            nextStructureView=folder?'folder:'+folder:'';
            invalidate();
            if(refresh)void buildPreview();
        },active,path=>{api('Reveal',path).catch(e=>status.textContent=message(e))});
        output.querySelector<HTMLButtonElement>('#organize-repreview')!.onclick=buildPreview;
        output.querySelector<HTMLButtonElement>('#organize-back-settings')!.onclick=()=>{status.textContent='';showStep(2)};
        apply.onclick=async()=>{
            if(!plan||busy)return;const chosen=ids(),token=plan.token,duplicates=duplicateCount();if(!chosen.length&&!duplicates)return;
            if(!await confirm(`<h2>按这个方案整理「${esc(p.project)}」？</h2><p>将搬迁 ${chosen.length} 项文件或完整文件夹。${p.mergedFolders?'普通文件夹按预览汇合层级，已有文件不覆盖。':'已有目录保持不变。'}完成后可以撤销。</p>${duplicates?`<p>${duplicates} 个重复文件不再搬入，来源保留原处。</p>`:''}`,"确认整理"))return;
            if(!current())return;setBusy(true);plan=null;status.textContent='正在整理，请稍候……';
            output.querySelector('.organize-step-actions > div')!.insertAdjacentHTML('afterbegin',action('organize-stop','停止后续整理'));
            const stop=output.querySelector<HTMLButtonElement>('#organize-stop')!;
            stop.onclick=async()=>{
                stop.disabled=true;
                try{const accepted=await api<boolean>('OrganizeStop',token);if(current()&&stop.isConnected){status.textContent=accepted?'已请求停止后续整理，当前文件处理完后会显示结果。':'任务尚未开始或已经结束，请稍后查看结果。';if(!accepted)stop.disabled=false}}
                catch(e){if(current()&&stop.isConnected){status.textContent='停止请求未成功，整理可能仍在继续：'+message(e);stop.disabled=false}}
            };
            try{
                const done=await api<Result>('OrganizeApply',token,chosen);if(!current())return;
                renderResult(p,done);showStep(4);status.textContent='';
                if(done.stopped){root.querySelector('#organize-heading')!.textContent='已停止后续整理';root.querySelector('#organize-intro')!.textContent='已完成的文件保留，尚未开始的文件仍在原处。可以继续整理未完成项，或撤销已完成的移动。';}
                else if(done.failed){root.querySelector('#organize-heading')!.textContent=done.moved+done.copied?'有些文件还没整理好':'这次没能完成整理';root.querySelector('#organize-intro')!.textContent='未完成的文件仍在原处，查看原因后可以重新整理。';}
                else if(!done.moved&&done.copied)root.querySelector('#organize-intro')!.textContent='原件仍在原处，可以打开项目查看复制的文件。';
                else if(!done.moved&&!done.copied&&done.duplicates){root.querySelector('#organize-heading')!.textContent='这些文件已经齐全';root.querySelector('#organize-intro')!.textContent='重复文件保留在原处，没有移动或删除任何文件。';}
            }
            catch(e){if(current()){status.textContent=message(e)+'。请更新预览后再试。';apply.disabled=true;disabled.set(apply,true)}}
            finally{stop.remove();if(current())setBusy(false)}
        };
        selection();
    }
    function renderResult(p:Plan,done:Result) {
        let undoRequested=false;
        result.innerHTML=`<div class="organize-done"><p>已移动 ${done.moved} 项${done.copied?` · 已复制 ${done.copied} 项（原件保留）`:''}${done.duplicates?` · 重复 ${done.duplicates} 项留原处`:''}${done.stopped?` · 未开始 ${done.stopped} 项（原处保留）`:''}${done.failed?` · 未完成 ${done.failed} 项`:''}</p></div>${(done.warnings||[]).map(w=>`<p role="alert">${esc(w)}</p>`).join('')}<div id="organize-result-files"></div><div class="organize-result-actions">${action('organize-open','打开项目文件夹',true)}${done.undoPath&&done.moved?action('organize-undo','撤销本次整理'):''}</div><details><summary>整理记录与未选文件</summary><p class="sub">未选中的文件和已有目录保持原处。撤销后文件回到原位，已创建的目录和整理记录保留。</p><p class="organize-path">${esc(done.undoPath||'没有产生移动记录')}</p></details><p id="organize-undo-result" role="status"></p>${action('organize-another',(done.failed||done.stopped)?'继续整理未完成项':'再整理一个项目')}`;
        const receipt=mountResultFiles(result.querySelector('#organize-result-files')!,p,done,esc,path=>{api('Reveal',path).catch(e=>status.textContent=message(e))});
        const open=result.querySelector<HTMLButtonElement>('#organize-open')!;
        if(!done.moved&&!done.copied&&!done.duplicates)open.remove();
        else open.onclick=()=>{api('Reveal',p.target).catch(e=>status.textContent=message(e))};
        result.querySelector<HTMLButtonElement>('#organize-another')!.onclick=()=>{
            if(undoRequested){sourceChanged();showStep(1);status.textContent='保留了原来的来源与设置，请重新预览当前位置。';return}
            const failed=(done.items||[]).filter(row=>row.error||row.action==='失败'||row.action==='未开始');
            if((done.failed||done.stopped)&&failed.length){
                const paths=[...new Set(failed.map(row=>row.source))];
                const retry=paths.length<=64?paths:sources().filter(source=>paths.some(path=>path===source||path.startsWith(source.replace(/\/+$/,'')+'/')));
                if(retry.length){sourcesInput.value=retry.join('\n');const failedSet=new Set(paths);p.rows.forEach(row=>choices.set(row.source,failedSet.has(row.source)));settingsEdited=true;sourceChanged();showStep(1);if(paths.length>64)status.textContent='已保留未完成文件所在的来源，已完成的文件不会再次勾选。';return}
            }
            if(location.hash==='#/organize')void renderOrganizer('',api,esc,confirm);else location.hash='#/organize';
        };
        const undo=result.querySelector<HTMLButtonElement>('#organize-undo');
        if(undo)undo.onclick=async()=>{
            if(!await confirm('<h2>撤销本次整理？</h2><p>搬迁的文件会回到原位，已有文件不会被覆盖。</p>','确认撤销'))return;
            if(!current())return;
            undo.disabled=true;
            undoRequested=true;
            result.querySelector<HTMLButtonElement>('#organize-another')!.textContent='重新检查文件';
            receipt.invalidate();
            result.querySelector<HTMLElement>('.organize-done')!.hidden=true;
            try{const r=await api<Undo>('UndoHome',done.undoPath);if(!current())return;result.querySelector('#organize-undo-result')!.textContent=`恢复 ${r.restored} 项${r.unchanged?` · 原处未动 ${r.unchanged} 项`:''} · 暂存副本 ${r.quarantined} 项 · 跳过 ${r.skipped} 项 · 失败 ${r.failed} 项。${(r.notes||[]).join('；')}`;root.querySelector('#organize-heading')!.textContent=r.failed||r.skipped?'部分文件还未恢复':'已撤销本次整理';root.querySelector('#organize-intro')!.textContent=r.failed||r.skipped?'请查看下方记录，核对未恢复的文件。':done.copied?'移动的文件已经回到原位，跨盘复制的副本仍保留。':'移动的文件已经回到原位。';}
            catch(e){if(current()){root.querySelector('#organize-heading')!.textContent='撤销暂未完成';root.querySelector('#organize-intro')!.textContent='请核对当前文件位置后重试。';result.querySelector('#organize-undo-result')!.textContent=message(e);undo.disabled=false}}
        };
    }
    renderSources();
}
