export type Profile = {name: string; folders: string[]; roles: Record<string, string>; rules: {kind: string; value: string; folder: string}[]};
type WorkType = {label: string; layout: {name: string; folders: string[]; roles?: Record<string,string>}};
type Context = {workTypes: WorkType[]; profile?: Profile; templates?: Profile[]; inferred: {label: string; reason: string; preConfirm: boolean; alreadyInReadme?: boolean}[]};
type API = <T>(method: string, ...args: unknown[]) => Promise<T>;
const purposes: Record<string,string> = {brief:"需求与计划",reference:"素材与参考",source:"源文件／完整工程",draft:"工作过程",delivery:"交付成果",subtitle:"字幕",audio:"音频"};

export function mountSettings(host: HTMLElement, ctx: Context, api: API, esc: (s: unknown)=>string, onChange: ()=>void, confirm: (html: string, ok: string, dismissLabel?: string)=>Promise<boolean>) {
    const presets = [...new Map(ctx.workTypes.map(w=>[w.layout.name,w.layout])).values()];
    const knownTypes = new Set(ctx.workTypes.map(w=>w.label));
    const inferred = ctx.inferred.find(w=>knownTypes.has(w.label)&&(w.preConfirm || w.alreadyInReadme));
    let label = inferred?.label || "";
    const extraTypes = new Set(ctx.inferred.filter(w=>knownTypes.has(w.label)&&w.label!==label&&(w.preConfirm||w.alreadyInReadme)).map(w=>w.label));
    const suggested = ctx.workTypes.find(w=>w.label===label)?.layout;
    const profileFor = (l: WorkType["layout"]): Profile => ({name:l.name,folders:[...l.folders],roles:{...l.roles},rules:[]});
    const general = (): Profile => ({name:"通用",folders:["资料","工作文件","交付"],roles:{brief:"资料",reference:"资料",source:"工作文件",draft:"工作文件",delivery:"交付"},rules:[]});
    let profile: Profile = structuredClone(ctx.profile || (suggested ? profileFor(suggested) : general()));
    let customized = !!ctx.profile;
    const changed = () => { customized = true; onChange(); refreshSummary(); };
    const saved = ctx.templates || [];
    const sourceOption = ctx.profile ? "project" : suggested ? `preset:${suggested.name}` : "general";
    let chosenTemplate = sourceOption;
    const industries = [...new Set(ctx.workTypes.map(w=>w.label.split('·')[0]))];
    host.innerHTML = `<label for="organize-worktype">这个项目主要做什么？</label><select class="select" id="organize-worktype"><option value="">暂不确定，先用通用目录</option>${industries.map(industry=>`<optgroup label="${esc(industry)}">${ctx.workTypes.filter(w=>w.label.split('·')[0]===industry).map(w=>`<option value="${esc(w.label)}">${esc(w.label)}</option>`).join('')}</optgroup>`).join('')}</select>
      <p class="sub" id="organize-suggestion">${inferred?`建议「${esc(inferred.label)}」${inferred.reason?`：${esc(inferred.reason)}`:''}。你可以更改。`:'没关系，不确定工种也可以先整理。'}</p>
      <details id="organize-extra-types"><summary>还包含其他工种 <span id="organize-extra-count"></span></summary><p class="sub">可添加多个工种，目录以主要工种或你的自定义设置为准。</p><div id="organize-extra-list" class="project-suggestion-actions"></div><label for="organize-extra-type">添加其他工种</label><select id="organize-extra-type" class="select select-sm"></select></details>
      <div class="organize-folder-summary"><p id="organize-layout-note"></p><ul id="organize-folder-summary" aria-label="整理后的文件夹"></ul></div>
      <details id="organize-customize"><summary>自定义文件夹和规则</summary>
      <label for="organize-template">换一个目录模板</label><select class="select select-sm" id="organize-template">${ctx.profile?'<option value="project">此项目上次的模板</option>':''}<option value="general">通用目录</option><option value="blank">从空白模板开始</option><optgroup label="工种模板">${presets.map(l=>`<option value="preset:${esc(l.name)}">${esc(l.name)}</option>`).join("")}</optgroup>${saved.length?`<optgroup label="我保存的模板">${saved.map((p,i)=>`<option value="saved:${i}">${esc(p.name)}</option>`).join("")}</optgroup>`:''}</select>
      <div class="organize-directory-head"><span>项目里的文件夹</span><span>默认放什么</span></div><div id="organize-directory-editor"></div><mdui-button variant="text" class="btn btn-sm btn-ghost" type="button" id="organize-add-folder">＋ 添加文件夹</mdui-button>
      <details class="organize-rules"><summary>自定义放置规则 <span id="organize-rule-count"></span></summary><p class="sub">按从上到下的顺序，采用第一条命中的规则；预览中手动选择的目录优先。工程内部保持完整。</p><div id="organize-rule-editor"></div><mdui-button variant="text" class="btn btn-sm btn-ghost" type="button" id="organize-add-rule">＋ 添加规则</mdui-button></details>
      <div class="organize-save"><label for="organize-template-name">留作以后使用</label><input class="input input-sm" id="organize-template-name" placeholder="我的项目模板" aria-label="保存的模板名称"><mdui-button variant="text" class="btn btn-sm btn-ghost" type="button" id="organize-save-template">另存为模板</mdui-button><span id="organize-template-status" role="status"></span></div></details>`;
    const template = host.querySelector<HTMLSelectElement>("#organize-template")!;
    const worktype = host.querySelector<HTMLSelectElement>("#organize-worktype")!;
    template.value=sourceOption;worktype.value=label;
    function refreshExtraTypes() {
        host.querySelector('#organize-extra-count')!.textContent=extraTypes.size?`（已选 ${extraTypes.size} 个）`:'';
        host.querySelector('#organize-extra-list')!.innerHTML=[...extraTypes].map(type=>`<mdui-button variant="text" type="button" class="btn btn-sm btn-ghost" data-remove-worktype="${esc(type)}" aria-label="移除工种 ${esc(type)}">${esc(type)} ×</mdui-button>`).join('');
        const picker=host.querySelector<HTMLSelectElement>('#organize-extra-type')!;
        picker.innerHTML='<option value="">选择一个工种</option>'+ctx.workTypes.filter(w=>w.label!==label&&!extraTypes.has(w.label)).map(w=>`<option value="${esc(w.label)}">${esc(w.label)}</option>`).join('');
        picker.onchange=()=>{if(knownTypes.has(picker.value)&&picker.value!==label){extraTypes.add(picker.value);refreshExtraTypes();onChange()}};
        host.querySelectorAll<HTMLButtonElement>('[data-remove-worktype]').forEach(button=>button.onclick=()=>{extraTypes.delete(button.dataset.removeWorktype!);refreshExtraTypes();onChange();host.querySelector<HTMLElement>('#organize-extra-type')!.focus()});
    }
    function refreshSummary() {
        host.querySelector('#organize-layout-note')!.textContent = customized ? '使用你的自定义目录' : '文件会分别放进这些文件夹';
        host.querySelector('#organize-folder-summary')!.innerHTML = profile.folders.map(f=>`<li>${esc(f)||'未命名文件夹'}</li>`).join('');
    }
    function refreshRules() {
        host.querySelector("#organize-rule-count")!.textContent=`（${profile.rules.length}）`;
        host.querySelector("#organize-rule-editor")!.innerHTML=profile.rules.map((r,i)=>`<div class="organize-rule"><span class="sub">${i+1}</span><select class="select select-sm" data-kind="${i}" aria-label="规则 ${i+1} 条件类型">${[["extension","扩展名是"],["name","文件名包含"],["path","来源路径包含"]].map(([v,t])=>`<option value="${v}" ${r.kind===v?'selected':''}>${t}</option>`).join('')}</select><input class="input input-sm" data-value="${i}" aria-label="规则 ${i+1} 匹配内容" value="${esc(r.value)}" placeholder="${r.kind==='extension'?'例如 png,jpg':'例如 客户确认'}"><span>放入</span><select class="select select-sm" data-rule-folder="${i}" aria-label="规则 ${i+1} 目标目录">${profile.folders.map(f=>`<option value="${esc(f)}" ${r.folder===f?'selected':''}>${esc(f)||'未命名目录'}</option>`).join('')}</select><mdui-button variant="text" class="btn btn-sm btn-ghost" type="button" data-up="${i}" ${i===0?'disabled':''} aria-label="上移规则 ${i+1}">↑</mdui-button><mdui-button variant="text" class="btn btn-sm btn-ghost" type="button" data-remove-rule="${i}" aria-label="删除规则 ${i+1}">移除</mdui-button></div>`).join('') || '<p class="sub">没有额外规则，按工种和文件类型自动归档。完整文件夹会保留内部结构。</p>';
        host.querySelectorAll<HTMLSelectElement>("[data-kind]").forEach(el=>el.onchange=()=>{profile.rules[Number(el.dataset.kind)].kind=el.value;changed();refreshRules()});
        host.querySelectorAll<HTMLInputElement>("[data-value]").forEach(el=>el.oninput=()=>{profile.rules[Number(el.dataset.value)].value=el.value;changed()});
        host.querySelectorAll<HTMLSelectElement>("[data-rule-folder]").forEach(el=>el.onchange=()=>{profile.rules[Number(el.dataset.ruleFolder)].folder=el.value;changed()});
        host.querySelectorAll<HTMLButtonElement>("[data-remove-rule]").forEach(el=>el.onclick=()=>{profile.rules.splice(Number(el.dataset.removeRule),1);changed();refreshRules()});
        host.querySelectorAll<HTMLButtonElement>("[data-up]").forEach(el=>el.onclick=()=>{const i=Number(el.dataset.up);[profile.rules[i-1],profile.rules[i]]=[profile.rules[i],profile.rules[i-1]];changed();refreshRules()});
    }
    function refreshFolders() {
        host.querySelector("#organize-directory-editor")!.innerHTML=profile.folders.map((f,i)=>`<div class="organize-directory"><input class="input input-sm" data-dir="${i}" aria-label="文件夹 ${i+1} 名称" value="${esc(f)}" placeholder="文件夹名称，可用 / 分层"><select class="select select-sm" data-role="${i}" aria-label="文件夹 ${i+1} 用途"><option value="">由自定义规则决定</option>${Object.entries(purposes).map(([role,text])=>`<option value="${role}" ${profile.roles[role]===f?'selected':''}>${text}</option>`).join('')}</select><mdui-button variant="text" class="btn btn-sm btn-ghost" type="button" data-remove-dir="${i}" aria-label="移除文件夹 ${i+1}">移除</mdui-button></div>`).join('') || '<p class="sub">添加自己的文件夹，或选择上面的工种模板。</p>';
        host.querySelectorAll<HTMLInputElement>("[data-dir]").forEach(el=>el.oninput=()=>{const i=Number(el.dataset.dir),old=profile.folders[i];profile.folders[i]=el.value;for(const k in profile.roles)if(profile.roles[k]===old)profile.roles[k]=el.value;for(const r of profile.rules)if(r.folder===old)r.folder=el.value;changed();refreshRules()});
        host.querySelectorAll<HTMLSelectElement>("[data-role]").forEach(el=>el.onchange=()=>{const f=profile.folders[Number(el.dataset.role)];for(const k in profile.roles)if(profile.roles[k]===f)delete profile.roles[k];if(el.value)profile.roles[el.value]=f;changed();refreshFolders()});
        host.querySelectorAll<HTMLButtonElement>("[data-remove-dir]").forEach(el=>el.onclick=()=>{const [old]=profile.folders.splice(Number(el.dataset.removeDir),1);for(const k in profile.roles)if(profile.roles[k]===old)delete profile.roles[k];profile.rules=profile.rules.filter(r=>r.folder!==old);changed();refreshFolders();refreshRules()});
    }
    const refresh=()=>{refreshFolders();refreshRules();refreshSummary()};
    template.onchange=async()=>{
        const value=template.value;
        const next:Profile=structuredClone(value==='project'?ctx.profile!:value==='general'?general():value.startsWith('saved:')?saved[Number(value.slice(6))]:value.startsWith('preset:')?profileFor(presets.find(p=>p.name===value.slice(7))!):{name:'自定义',folders:[],roles:{},rules:[]});
        template.value=chosenTemplate;
        if(customized&&JSON.stringify(next)!==JSON.stringify(profile)){
            const added=next.folders.filter(folder=>!profile.folders.includes(folder)),removed=profile.folders.filter(folder=>!next.folders.includes(folder));
            template.disabled=true;
            let accepted=false;
            try {accepted=await confirm(`<h2>用新模板重新开始？</h2><p>只替换本次整理的目录和规则，磁盘文件与已保存的模板保持不变。</p><p>新目录：${next.folders.length?next.folders.map(esc).join('、'):'空白，之后自行添加'}</p>${added.length?`<p>新增：${added.map(esc).join('、')}</p>`:''}${removed.length?`<p>移除：${removed.map(esc).join('、')}</p>`:''}<p>放置规则：现有 ${profile.rules.length} 条 → 新模板 ${next.rules.length} 条。文件夹用途与顺序也会按新模板设置。</p>`, '用新模板重新开始', '保留我的修改')}
            finally{template.disabled=false}
            if(!accepted||!template.isConnected)return;
        }
        profile=next;chosenTemplate=value;template.value=value;changed();refresh();
    };
    worktype.onchange=()=>{
        label=worktype.value;
        extraTypes.delete(label);refreshExtraTypes();
        if(!customized){const layout=ctx.workTypes.find(w=>w.label===label)?.layout;profile=layout?profileFor(layout):general();template.value=layout?`preset:${layout.name}`:'general';chosenTemplate=template.value;refresh()}
        host.querySelector('#organize-suggestion')!.textContent=customized?'已保留你编辑的目录和规则。如需换模板，可展开下方自定义设置。':'已按你的选择更新文件夹，可以继续预览。';
        onChange();
    };
    host.querySelector<HTMLButtonElement>("#organize-add-folder")!.onclick=()=>{profile.folders.push('新文件夹');changed();refresh()};
    host.querySelector<HTMLButtonElement>("#organize-add-rule")!.onclick=()=>{profile.rules.push({kind:'extension',value:'',folder:profile.folders[0]||''});changed();refreshRules()};
    host.querySelector<HTMLButtonElement>("#organize-save-template")!.onclick=async()=>{const status=host.querySelector<HTMLElement>("#organize-template-status")!,button=host.querySelector<HTMLButtonElement>("#organize-save-template")!;button.disabled=true;try{await api('OrganizeSaveTemplate',{...profile,name:host.querySelector<HTMLInputElement>("#organize-template-name")!.value.trim()});status.textContent='模板已保存，下次打开可直接选用。'}catch(e){status.textContent=e instanceof Error?e.message:String(e)}finally{button.disabled=false}};
    refresh();refreshExtraTypes();
    return {get:()=>({profile:structuredClone(profile),workTypes:[...(label?[label]:[]),...extraTypes]})};
}
