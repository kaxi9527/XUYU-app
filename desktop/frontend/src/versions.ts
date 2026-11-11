import {organizerAction} from './ui-actions';
import {VersionJournal} from './version-journal';
import {basename,roles,states,type API,type Work,type WorkList,type History,type Version,type Preview,type Listing,type Action,type Receipt} from './version-types';
import './versions.css';

type Confirm=(html:string,ok:string)=>Promise<boolean>;
type Project={name:string;path:string;roots:{name:string;path:string;file?:boolean}[]};
const errorText=(e:unknown)=>e instanceof Error?e.message:String(e);
const date=(s:string)=>s?new Date(s).toLocaleString('zh-CN',{year:'numeric',month:'numeric',day:'numeric',hour:'2-digit',minute:'2-digit'}):'未记录';

/** Private project screen; native controls and the existing ShelfButton contract. */
export function mountWorks(view:HTMLElement,key:string,api:API,esc:(s:unknown)=>string,confirm:Confirm,initialWorkID=''):()=>void {
    const shell=document.createElement('section');shell.className='works-screen';view.replaceChildren(shell);
    let disposed=false,sequence=0,previewRequest='',previewToken='',busy=false;
    let project:Project,journal:VersionJournal,current:Work|undefined,query='',listOffset=0,historyOffset=0;
    let content:HTMLElement,status:HTMLElement,recovery:HTMLElement;
    const live=()=>!disposed&&shell.isConnected;
    const q=<T extends HTMLElement>(selector:string)=>shell.querySelector<T>(selector)!;
    const say=(message:string)=>{if(live())status.textContent=message};
    const projectHref=key.startsWith('root:')?'#/project/'+encodeURIComponent(key.slice(5)):'#/place/'+encodeURIComponent(key.slice(6));
    const button=(id:string,label:string,primary=false)=>organizerAction(id,label,primary);
    const run=(fn:()=>Promise<unknown>)=>{void fn().catch(e=>{say(errorText(e));showRecovery()})};
    const focusHeading=()=>content.querySelector<HTMLElement>('h2')?.focus();
    const hasPending=()=>{try{return !!journal?.pending()}catch(e){say(errorText(e));return true}};
    function showRecovery() {
        if(!live()||!recovery||!journal)return;
        const pending=hasPending();
        recovery.hidden=!pending;
        recovery.innerHTML=pending?`<p>上次操作还没有明确结果。先核对，再继续编辑。</p><div class="works-actions">${button('work-recover','核对结果')}${button('work-retry-operation','用同一请求重试')}</div>`:'';
        if(pending){q<HTMLButtonElement>('#work-recover').onclick=()=>run(()=>recover(false));q<HTMLButtonElement>('#work-retry-operation').onclick=()=>run(()=>recover(true))}
    }
    async function recover(retry:boolean) {
        if(busy)return;busy=true;recovery.setAttribute('inert','');say('正在核对上次操作……');
        try {const receipt=await journal.recover(retry);say(receipt?'上次操作已保存。':'尚未查到回执。可以用同一请求重试。');if(receipt){if(current)await detail(current.id);else await list()}}
        finally{busy=false;recovery.removeAttribute('inert');showRecovery()}
    }
    async function mutate(action:Omit<Action,'operationID'|'projectKey'>):Promise<Receipt|undefined> {
        if(busy||hasPending()){say('请先核对上次操作。');showRecovery();return}
        busy=true;content.setAttribute('inert','');say('正在保存……');
        try {const receipt=await journal.start(action);say('已保存。');return receipt}
        finally{busy=false;content.removeAttribute('inert');showRecovery()}
    }
    async function openVersion(id:string) {
        say('正在核对文件内容……');
        const path=await api<string>('VersionFile',key,id);
        if(!live())return;
        await api('Reveal',path);say('已在文件管理器中显示。');
    }
    function pager(offset:number,limit:number,total:number,load:(next:number)=>Promise<void>) {
        const el=content.querySelector('.works-pagination');if(!el)return;
        el.innerHTML=`<span>${total?`${offset+1}–${Math.min(offset+limit,total)} / ${total}`:'0 项'}</span>${button('works-prev','上一页')}${button('works-next','下一页')}`;
        const prev=q<HTMLButtonElement>('#works-prev'),next=q<HTMLButtonElement>('#works-next');
        prev.disabled=offset===0;next.disabled=offset+limit>=total;
        prev.onclick=()=>run(()=>load(Math.max(0,offset-limit)));next.onclick=()=>run(()=>load(offset+limit));
    }
    async function list(offset=listOffset) {
        const seq=++sequence;current=undefined;listOffset=offset;
        const graphLink=shell.querySelector<HTMLAnchorElement>('.work-view-nav a[href^="#/graph/"]');
        if(graphLink)graphLink.href=`#/graph/${encodeURIComponent(key)}`;
        const search=query;
        content.innerHTML=`<div class="works-toolbar"><h2 tabindex="-1">成果</h2>${button('work-new','登记作品',true)}</div><form class="works-search"><label for="work-search">搜索作品、文件或来源</label><div><input class="input" id="work-search" type="search" value="${esc(query)}" placeholder="名称、文件夹、工具或任务"><mdui-button variant="text" class="btn btn-sm" type="submit">搜索</mdui-button>${query?'<mdui-button variant="text" class="btn btn-sm btn-ghost" id="work-clear" type="button">清除</mdui-button>':''}</div></form><div class="works-list" aria-busy="true"><p role="status">正在读取作品……</p></div><nav class="works-pagination" aria-label="作品分页"></nav>`;
        q<HTMLButtonElement>('#work-new').onclick=()=>run(()=>registration());
        q<HTMLFormElement>('.works-search').onsubmit=e=>{e.preventDefault();query=q<HTMLInputElement>('#work-search').value.trim();run(()=>list(0))};
        const clear=q<HTMLButtonElement>('#work-clear');if(clear)clear.onclick=()=>{query='';run(()=>list(0))};
        try {
            const data=await api<WorkList>('ProjectWorks',key,search,offset);
            if(!live()||seq!==sequence)return;
            listOffset=data.offset;
            q('.works-list').removeAttribute('aria-busy');
            q('.works-list').innerHTML=data.items.length?data.items.map(w=>`<article class="work-row"><div><mdui-button variant="text" class="work-title" type="button" data-work="${esc(w.id)}">${esc(w.title)}</mdui-button><p class="sub">${w.versions} 个版本 · ${w.usable?'已选可用版':esc(states[w.usableState]||'状态待核对')}</p></div>${w.usable&&w.currentUsableVersionID?`<mdui-button variant="text" class="btn btn-sm btn-ghost" type="button" data-open-version="${esc(w.currentUsableVersionID)}">打开可用版</mdui-button>`:''}</article>`).join(''):`<div class="works-empty"><h3>${search?'没有匹配的作品':'把重要成果放在这里'}</h3><p>${search?'试试文件名、文件夹或来源工具，也可以清除搜索。':'选择一个文件登记为作品，后续草稿和导出件就能收在同一处。'}</p>${search?'':`<p class="sub">磁盘里的文件保持原位。需要整理文件夹，可返回项目。</p>`}</div>`;
            content.querySelectorAll<HTMLButtonElement>('[data-work]').forEach(b=>b.onclick=()=>run(()=>detail(b.dataset.work!)));
            content.querySelectorAll<HTMLButtonElement>('[data-open-version]').forEach(b=>b.onclick=()=>run(()=>openVersion(b.dataset.openVersion!)));
            pager(data.offset,data.limit,data.total,list);
        } catch(e){if(live()&&seq===sequence){q('.works-list').removeAttribute('aria-busy');q('.works-list').innerHTML=`<p>${esc(errorText(e))}</p>${button('work-list-retry','重新读取')}`;q<HTMLButtonElement>('#work-list-retry').onclick=()=>run(()=>list(offset))}}
    }
    const versionName=(v:Version)=>basename(v.locations[0]?.location.path||'位置未记录');
    function versionHTML(v:Version,featured=false):string {
        const id=esc(v.version.id);
        return `<article class="work-version ${featured?'work-featured':''}" data-version="${id}"><div class="works-toolbar"><div><h3>${esc(versionName(v))}</h3><p class="sub">${esc(roles[v.version.role]||v.version.role)} · ${esc(date(v.version.registeredAt))}</p></div><span class="work-state">${v.usable?'可用版':esc(states[v.state]||v.state)}</span></div>${v.recordedUsable&&!v.usable?'<p class="works-warning">之前确认过这一版，但原内容现在不可用。旧确认保留，尚未保存旧文件副本。</p>':''}<div class="works-actions">${v.state==='available'?`<mdui-button variant="text" class="btn btn-sm ${featured?'btn-primary':'btn-ghost'}" type="button" data-open-version="${id}">在文件夹中显示</mdui-button>${!v.usable?`<mdui-button variant="text" class="btn btn-sm btn-ghost" type="button" data-usable="${id}">这版可用</mdui-button>`:''}`:''}${v.recordedUsable?`<mdui-button variant="text" class="btn btn-sm btn-ghost" type="button" data-unset="${id}">取消可用标记</mdui-button>`:''}</div><details class="work-source"><summary>位置与来源${v.provenance.length?` · ${v.provenance.length} 条记录`:''}</summary>${v.locations.map(l=>`<p class="work-path">${esc(l.location.path)}<br><span class="sub">${esc(states[l.state]||l.state)}${l.reason?' · '+esc(l.reason):''}</span></p>`).join('')}${v.provenance.length?v.provenance.map(p=>`<section class="work-source-record"><b>${esc(({tool_declaration:'工具声明',index_record:'索引登记',user_note:'用户补充'} as Record<string,string>)[p.kind]||p.kind)}</b><p class="sub">${esc(({content_match:'与本版内容一致',path_only:'仅按路径关联，未验证本版来源',user_supplied:'由用户填写，未经工具验证'} as Record<string,string>)[p.binding||'']||'未验证来源')}</p><dl>${[['工具',p.tool],['智能体',p.agent],['登记者',p.recorder],['任务',p.task],['来源',p.source],['参数',p.parameters],['声明时间',p.claimedAt?date(p.claimedAt):''],['录入时间',date(p.recordedAt)]].filter(([,v])=>v).map(([k,v])=>`<dt>${esc(k)}</dt><dd>${esc(v)}</dd>`).join('')}</dl></section>`).join(''):'<p class="sub">没有来源记录。可以自行补充；不会根据文件名推测。</p>'}${v.confirmations.length?`<p class="sub">最近确认：${esc(date(v.confirmations[v.confirmations.length-1].confirmedAt))}</p>`:''}<mdui-button variant="text" class="btn btn-sm btn-ghost" type="button" data-source="${id}">补充来源</mdui-button></details></article>`;
    }
    async function detail(id:string,offset=0) {
        const seq=++sequence;historyOffset=offset;
        content.innerHTML='<p role="status">正在核对版本内容……</p>';
        try {
            const data=await api<History>('WorkVersions',key,id,offset);
            let featured=data.items.find(v=>v.version.id===data.work.currentUsableVersionID);
            if(data.work.currentUsableVersionID&&!featured){const extra=await api<{revision:number;item:Version}>('VersionDetail',key,data.work.currentUsableVersionID);if(extra.revision!==data.revision)throw new Error('作品记录刚有变化，请重新读取。');featured=extra.item}
            if(!live()||seq!==sequence)return;
            current=data.work;
            const graphLink=shell.querySelector<HTMLAnchorElement>('.work-view-nav a[href^="#/graph/"]');
            if(graphLink)graphLink.href=`#/graph/${encodeURIComponent(key)}/${encodeURIComponent(current.id)}`;
            content.innerHTML=`<div class="works-toolbar"><mdui-button variant="text" class="btn btn-sm btn-ghost" type="button" id="work-back">← 全部成果</mdui-button>${button('work-add-version','添加版本',true)}</div><h2 tabindex="-1">${esc(current.title)}</h2>${featured?`<section aria-label="当前可用版"><h3 class="work-section-title">${featured.usable?'当前可用版':'之前选定的版本'}</h3>${versionHTML(featured,true)}</section>`:'<div class="works-empty compact"><h3>还未选择可用版</h3><p>展开下面的版本，选择你现在要使用的那一版。新草稿不会替换它。</p></div>'}<details class="work-history" ${featured?'':'open'}><summary>全部版本 · ${data.total}</summary><div>${data.items.map(v=>versionHTML(v)).join('')}</div><nav class="works-pagination" aria-label="版本分页"></nav></details>`;
            q<HTMLButtonElement>('#work-back').onclick=()=>run(()=>list());q<HTMLButtonElement>('#work-add-version').onclick=()=>run(()=>registration(data.work));
            content.querySelectorAll<HTMLButtonElement>('[data-open-version]').forEach(b=>b.onclick=()=>run(()=>openVersion(b.dataset.openVersion!)));
            content.querySelectorAll<HTMLButtonElement>('[data-usable],[data-unset]').forEach(b=>b.onclick=()=>run(async()=>{
                const unset=!!b.dataset.unset,vid=b.dataset.usable||'';
                if(!await confirm(`<h2>${unset?'取消可用标记？':'将这版设为可用？'}</h2><p>${unset?'历史确认会保留，之后可以重新选择。':'确认的是当前文件内容。原文件改写后需要重新确认；这里尚未保存旧文件副本。'}</p>`,unset?'取消标记':'这版可用')||!live())return;
                if(await mutate({kind:'usable',workID:id,versionID:vid,revision:data.revision}))await detail(id,historyOffset);
            }));
            content.querySelectorAll<HTMLButtonElement>('[data-source]').forEach(b=>b.onclick=()=>sourceForm(b.dataset.source!,data));
            pager(data.offset,data.limit,data.total,async next=>{await detail(id,next);q<HTMLDetailsElement>('.work-history').open=true});
            focusHeading();
        }catch(e){if(live()&&seq===sequence){content.innerHTML=`<p>${esc(errorText(e))}</p>${button('work-detail-retry','重新读取')}${button('work-detail-back','返回成果')}`;q<HTMLButtonElement>('#work-detail-retry').onclick=()=>run(()=>detail(id,offset));q<HTMLButtonElement>('#work-detail-back').onclick=()=>run(()=>list())}}
    }
    function sourceForm(id:string,data:History) {
        ++sequence;
        content.innerHTML=`<h2 tabindex="-1">补充来源</h2><p class="sub">只填写你知道的信息，留空表示未知。这些内容会标为“用户补充”。</p><form class="work-source-form">${[['tool','工具'],['agent','智能体'],['task','任务'],['source','来源说明'],['parameters','参数（可选）']].map(([name,label])=>`<label for="source-${name}">${label}<input class="input" id="source-${name}" name="${name}" maxlength="2000"></label>`).join('')}<div class="works-actions"><mdui-button variant="text" class="btn btn-primary" type="submit">保存来源</mdui-button>${button('source-cancel','返回版本')}</div></form>`;
        q<HTMLButtonElement>('#source-cancel').onclick=()=>run(()=>detail(data.work.id,historyOffset));
        q<HTMLFormElement>('.work-source-form').onsubmit=e=>{e.preventDefault();run(async()=>{const note=Object.fromEntries(new FormData(e.target as HTMLFormElement).entries()) as Record<string,string>;if(await mutate({kind:'source',versionID:id,note,revision:data.revision}))await detail(data.work.id,historyOffset)})};
        focusHeading();
    }
    async function registration(work?:Work) {
        if(hasPending()){showRecovery();say('请先核对上次操作。');return}
        const seq=++sequence;let browseSeq=0,root=project.roots[0],path=root.path,selection=new Map<string,string>();
        current=work;
        content.innerHTML=`<div class="works-toolbar"><h2 tabindex="-1">${work?'添加版本':'登记作品'}</h2>${button('register-cancel','返回成果')}</div><p class="sub">${work?`选中的文件将加入「${esc(work.title)}」。`:'选择属于同一个作品的文件。不同草稿、尺寸和导出件可以一起登记。'}</p><form id="version-register"><div class="work-register-fields">${work?'':`<label for="register-title">作品名称<input class="input" id="register-title" maxlength="300" required></label>`}<label for="register-role">文件用途<select class="select" id="register-role">${Object.entries(roles).map(([k,v])=>`<option value="${k}" ${k==='draft'?'selected':''}>${v}</option>`).join('')}</select></label></div><div class="works-toolbar">${project.roots.length>1?`<label>项目位置<select class="select" id="register-root">${project.roots.map((r,i)=>`<option value="${i}">${esc(r.name)}</option>`).join('')}</select></label>`:'<b>选择项目文件</b>'}<label class="work-hidden"><input class="toggle toggle-sm" id="register-hidden" type="checkbox">显示隐藏文件</label></div><div class="works-toolbar"><mdui-button variant="text" class="btn btn-sm btn-ghost" id="register-up" type="button">上一级</mdui-button><span class="work-breadcrumb"></span></div><div class="work-picker" aria-label="项目文件"></div><div class="work-selected" role="status"></div><div class="works-actions"><mdui-button variant="text" class="btn btn-primary" type="submit" id="register-preview">预览登记</mdui-button><mdui-button variant="text" class="btn btn-ghost" type="button" id="register-stop" hidden>取消检查</mdui-button></div></form>`;
        const picker=q('.work-picker'),hidden=q<HTMLInputElement>('#register-hidden'),form=q<HTMLFormElement>('#version-register');
        const valid=()=>live()&&seq===sequence;
        function updateSelection(){if(!valid())return;q('.work-selected').textContent=`已选 ${selection.size} 个文件${selection.size?' · 将登记到同一个作品':''}`;q<HTMLButtonElement>('#register-preview').disabled=!selection.size}
        const leave=()=>{++browseSeq;run(async()=>{if(work)await detail(work.id);else await list()})};
        q<HTMLButtonElement>('#register-cancel').onclick=leave;
        async function browse(next:string) {
            const n=++browseSeq;picker.innerHTML='<p role="status">正在读取文件夹……</p>';
            try {
                const data:Listing=root.file?{path:root.path,parent:'',entries:[{path:root.path,name:basename(root.path),hidden:basename(root.path).startsWith('.'),link:false,directory:false}],hiddenCount:0,truncated:false}:await api<Listing>('OrganizeBrowse',next,hidden.checked);
                if(!valid()||n!==browseSeq)return;
                path=data.path;q('.work-breadcrumb').textContent=root.file?basename(path):path===root.path?root.name:path.slice(root.path.length+1);
                const parent=data.parent;const up=q<HTMLButtonElement>('#register-up');up.disabled=!parent||path===root.path||root.file===true;up.onclick=()=>run(()=>browse(parent));
                picker.innerHTML=data.entries.filter(f=>hidden.checked||!f.hidden).map((f,i)=>`<div class="work-pick-row">${f.directory?`<mdui-button variant="text" class="btn btn-sm btn-ghost work-folder" data-folder="${i}" type="button" ${f.link?'disabled':''}>▸ ${esc(f.name)}</mdui-button>`:`<label><input class="checkbox checkbox-sm" type="checkbox" data-file="${i}" ${selection.has(f.path)?'checked':''} ${f.link?'disabled':''}><span>${esc(f.name)}</span></label>`}${f.hidden?'<span class="sub">隐藏</span>':''}${f.link?'<span class="sub">链接不可登记</span>':''}</div>`).join('')||'<p class="sub">这里没有可显示的文件。</p>';
                if(data.truncated)picker.insertAdjacentHTML('beforeend','<p class="works-warning">此目录超过 1000 项，仅显示部分内容。请进入具体子目录缩小范围。</p>');
                // The rendered index uses the same filtered array as event lookup.
                const entries=data.entries.filter(f=>hidden.checked||!f.hidden);
                picker.querySelectorAll<HTMLButtonElement>('[data-folder]').forEach(b=>b.onclick=()=>run(()=>browse(entries[Number(b.dataset.folder)].path)));
                picker.querySelectorAll<HTMLInputElement>('[data-file]').forEach(b=>b.onchange=()=>{const f=entries[Number(b.dataset.file)];if(b.checked){if(selection.size>=200){b.checked=false;say('每次最多登记 200 个文件。');return}selection.set(f.path,f.name);const title=q<HTMLInputElement>('#register-title');if(title&&!title.value)title.value=f.name.replace(/\.[^.]+$/,'')}else selection.delete(f.path);updateSelection()});
            } catch(e){if(valid()&&n===browseSeq){picker.innerHTML=`<p>${esc(errorText(e))}</p>${button('browse-version-retry','重新读取')}`;q<HTMLButtonElement>('#browse-version-retry').onclick=()=>run(()=>browse(next))}}
        }
        const rootSelect=q<HTMLSelectElement>('#register-root');if(rootSelect)rootSelect.onchange=()=>{root=project.roots[Number(rootSelect.value)];run(()=>browse(root.path))};
        hidden.onchange=()=>{if(!hidden.checked)for(const selectedPath of selection.keys())if(selectedPath.split('/').some(p=>p.startsWith('.')))selection.delete(selectedPath);updateSelection();run(()=>browse(path))};
        form.onsubmit=e=>{e.preventDefault();run(async()=>{
            if(previewRequest||!selection.size)return;
            const request=crypto.randomUUID();previewRequest=request;
            const controls=Array.from(form.querySelectorAll<HTMLInputElement|HTMLSelectElement|HTMLButtonElement>('input,select,mdui-button'));
            const disabled=controls.filter(c=>c.disabled);controls.forEach(c=>c.disabled=true);
            q<HTMLButtonElement>('#register-cancel').disabled=true;
            const stop=q<HTMLButtonElement>('#register-stop');stop.hidden=false;stop.disabled=false;
            let cancelled=false;
            stop.onclick=()=>{cancelled=true;stop.disabled=true;void api('CancelVersionPreview',request).catch(e=>say(errorText(e)));say('已取消本次登记检查。')};
            say('正在核对所选文件……');
            let preview:Preview|undefined;
            try {
                preview=await api<Preview>('PreviewRegisterVersion',{projectKey:key,paths:[...selection.keys()],workID:work?.id||'',title:q<HTMLInputElement>('#register-title')?.value.trim()||'',role:q<HTMLSelectElement>('#register-role').value,includeHidden:hidden.checked,requestID:request});
                previewToken=preview.token;
                if(!valid()||cancelled)return;
                const accepted=await confirm(`<h2>登记 ${preview.items.length} 个文件？</h2><p>作品：${esc(work?.title||preview.title)}</p><p>${esc(preview.notice)}</p>${preview.warnings.map(w=>`<p class="works-warning">${esc(w)}</p>`).join('')}<p class="sub">文件保持原位。登记后可再选择可用版。</p>`,'登记版本');
                if(!accepted||!valid())return;
                const receipt=await mutate({kind:'register',token:preview.token});
                if(receipt&&valid()){say('已登记。可以选择现在要用的版本。');await detail(receipt.entityIDs[1])}
            } catch(e){if(valid()&&!cancelled)throw e}
            finally {
                if(preview)void api('DiscardRegisterPreview',preview.token).catch(()=>{});
                previewRequest='';previewToken='';
                if(valid()){controls.forEach(c=>c.disabled=disabled.includes(c));stop.hidden=true;q<HTMLButtonElement>('#register-cancel').disabled=false;updateSelection()}
            }
        })};
        updateSelection();focusHeading();await browse(root.path);
    }
    async function init() {
        shell.innerHTML='<p role="status">正在读取项目……</p>';
        try {
            if(key.startsWith('root:')){const d=await api<{name:string;path:string}>('Project',key.slice(5));project={name:d.name,path:d.path,roots:[{name:d.name,path:d.path}]}}
            else if(key.startsWith('place:')){const d=await api<{project:{name:string;path:string;members:{path:string;isDir:boolean;via?:string}[]}}>('PlaceProject',key.slice(6));const p=d.project;project={name:p.name,path:p.path,roots:[{name:p.name,path:p.path},...p.members.filter(m=>m.path!==p.path&&!m.via).map(m=>({name:basename(m.path),path:m.path,file:!m.isDir}))]}}
            else throw new Error('请选择一个项目。');
            if(!live())return;
            journal=new VersionJournal(api,project.path,key);
            shell.innerHTML=`<header class="bar"><a class="back" href="#/projects">← 项目</a><h1>${esc(project.name)}</h1></header><nav class="work-view-nav" aria-label="项目视图"><a href="${projectHref}">文件夹</a><span aria-current="page">成果</span><a href="#/memory/${encodeURIComponent(key)}">文件记录</a><a href="#/graph/${encodeURIComponent(key)}">关系网</a></nav><p class="works-status" role="status"></p><aside class="works-recovery" hidden></aside><div class="works-content"></div>`;
            content=q('.works-content');status=q('.works-status');recovery=q('.works-recovery');
            showRecovery();if(initialWorkID)await detail(initialWorkID);else await list();
        } catch(e){if(live())shell.innerHTML=`<h1>成果暂时无法打开</h1><p>${esc(errorText(e))}</p><a href="${projectHref}">返回项目文件夹</a>`}
    }
    void init();
    return ()=>{disposed=true;++sequence;if(previewRequest)void api('CancelVersionPreview',previewRequest).catch(()=>{});if(previewToken)void api('DiscardRegisterPreview',previewToken).catch(()=>{})};
}
