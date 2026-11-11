import {organizerAction} from './ui-actions';
import {MemoryJournal} from './memory-journal';
import {memorySourcesHTML,bindMemorySources,originDetails} from './project-memory-sources';
import {memoryEventLabel,memoryEventPaths,memoryEventActor,type MemoryAPI,type MemoryStatus,type MemoryPage,type MemoryFiles,type MemoryEvent,type MemoryAction,type MemoryReceipt,type MemoryRecord} from './project-memory-types';
import './project-memory.css';

export function mountProjectMemory(view:HTMLElement,key:string,api:MemoryAPI,esc:(x:unknown)=>string,confirm:(html:string,ok:string)=>Promise<boolean>):()=>void {
    const root=document.createElement('section');root.className='memory-screen';view.replaceChildren(root);
    const projectLink=key.startsWith('root:')?'#/project/'+encodeURIComponent(key.slice(5)):'#/place/'+encodeURIComponent(key.slice(6));
    root.innerHTML=`<header class="bar"><a class="back" href="${projectLink}">← 项目</a><h1>文件记录</h1></header><nav class="work-view-nav" aria-label="项目视图"><a href="${projectLink}">文件夹</a><a href="#/works/${encodeURIComponent(key)}">成果</a><span aria-current="page">文件记录</span><a href="#/graph/${encodeURIComponent(key)}/events">关系网</a></nav><p class="memory-feedback" role="status" aria-live="polite">正在读取项目记录…</p><div class="memory-body"></div>`;
    const adviceLink=document.createElement('a');adviceLink.href='#/analysis/'+encodeURIComponent(key);adviceLink.textContent='整理建议';root.querySelector('.work-view-nav')!.append(adviceLink);
    const body=root.querySelector<HTMLElement>('.memory-body')!,feedback=root.querySelector<HTMLElement>('.memory-feedback')!;
    const journal=new MemoryJournal(key);
    let disposed=false,busy=false,epoch=0,state:MemoryStatus|undefined,page:MemoryPage|undefined,files:MemoryFiles|undefined,pending:MemoryAction|undefined,showFiles=false,showHidden=false,fileOffset=0;
    const live=()=>!disposed&&root.isConnected;
    const say=(text:string)=>{if(live())feedback.textContent=text};
    const errorText=(e:unknown)=>e instanceof Error?e.message:String(e);
    const conflict=(e:unknown)=>/项目(记忆|记录)已更新/.test(errorText(e));
    const button=(id:string,label:string,primary=false)=>organizerAction(id,label,primary);
    const q=<T extends HTMLElement>(s:string)=>body.querySelector<T>(s)!;
    const name=(path:string)=>path.split(/[\\/]/).filter(Boolean).pop()||path;
    const date=(at:string)=>at?new Date(at).toLocaleString('zh-CN',{month:'long',day:'numeric',hour:'2-digit',minute:'2-digit'}):'尚未检查';
    const setBusy=(value:boolean)=>{busy=value;if(live())body.querySelectorAll<HTMLButtonElement>('mdui-button').forEach(b=>b.disabled=value||b.dataset.unavailable==='true')};
    async function refresh() {
        const n=++epoch;
        for(let attempt=0;attempt<2;attempt++){
            const next=await api<MemoryStatus>('ProjectMemory',key);
            try{
                const events=await api<MemoryPage>('ProjectMemoryEvents',key,next.revision,'');
                if(!live()||n!==epoch)return;
                state=next;page=events;files=undefined;fileOffset=0;showFiles=false;render();return;
            }catch(e){if(!conflict(e)||attempt===1)throw e}
        }
    }
    async function run(work:()=>Promise<void>,focus='') {
        if(busy||!live())return;
        setBusy(true);epoch++;
        try{await work()}catch(e){
            if(conflict(e)){try{await refresh();say('项目记录已更新，已显示最近变化。请重新选择操作。')}catch(readError){say(errorText(readError))}}
            else say(errorText(e));
            if(live())render();
        }
        finally{if(live()){setBusy(false);if(focus)(document.getElementById(focus)||body.querySelector<HTMLElement>('#memory-scan,#memory-enable,#memory-recover'))?.focus()}}
    }
    async function recover() {
        pending=journal.load();
        if(!pending)return false;
        const result=await api<{found:boolean;record?:MemoryRecord}>('ProjectMemoryOperation',key,pending.operationID);
        if(!live())return false;
        if(result.found&&result.record?.operationID===pending.operationID){journal.clear(pending);pending=undefined;await refresh();say('已核对上次操作，记录已保存。');return true}
        render();say('上次操作还没有明确结果。可以重新核对，或使用原操作重试。');return false;
    }
    async function refreshAndRecover(){await refresh();const recovered=await recover();if(!pending&&!recovered)say('文件记录已读取。')}
    async function sourceAction(adapter:string,path:string,enabled:boolean,directory=false) {
        const snapshot=state;if(!snapshot||pending)return;
        if(!path.startsWith('/')||!directory&&!path.toLowerCase().endsWith('.jsonl'))throw new Error('请填写来源目录或 JSONL 记录文件的完整路径。');
        if(enabled&&!snapshot.enabled)throw new Error('请先恢复项目文件记录，再授权来源。');
        if(enabled&&!await confirm(`<h2>${directory?'读取这个会话目录？':'读取这份来源记录？'}</h2><p class="memory-path">${esc(path)}</p><p>${directory?'包含目录内现有和以后新增的 JSONL 记录，不需要逐份授权。只读结构化文件操作，不跟随链接。':'只读这份 '+esc(adapter)+' 记录中的结构化文件操作，'}关联当前项目范围内的文件。记录可能包含聊天正文，但不会复制正文或发送给模型。开启自动检查时继续读取新增内容；停止授权后保留已有来源历史。</p>`,'授权并读取'))return;
        if(!live())return;
        await apply({kind:enabled?'source_on':'source_off',projectKey:key,revision:snapshot.revision,operationID:crypto.randomUUID(),scopeSHA:snapshot.currentScopeSHA,sourceAdapter:adapter,sourcePath:path,sourceDirectory:directory});
        if(enabled&&!pending&&state?.enabled&&!state.scopeChanged&&state.sources?.some(s=>s.adapter===adapter&&s.path===path&&s.enabled)) {
            await apply({kind:'scan',projectKey:key,revision:state.revision,operationID:crypto.randomUUID(),scopeSHA:state.currentScopeSHA});
        }
    }
    async function apply(a:MemoryAction) {
        journal.save(a);pending=a;render();setBusy(true);say(a.kind==='scan'?'正在检查文件变化…':'正在更新记录设置…');
        const result=await api<{receipt?:MemoryReceipt;error?:string;uncertain:boolean}>('ApplyProjectMemoryAction',a);
        if(!live())return;
        if(result.receipt?.record.operationID===a.operationID){
            journal.clear(a);pending=undefined;await refresh();
            say(result.receipt.projectionPending?'事件已保存，目录视图待更新。下次检查会尝试恢复。':a.kind==='scan'?'本次检查已记录。':a.kind==='pause'?'记录已暂停，已有历史保留。':a.kind==='automatic_on'?'自动检查已开启。应用运行时定期检查，退出后停止。':a.kind==='automatic_off'?'自动检查已关闭，仍可手动检查。':'记录已开启。点击「检查变化」更新目录。');
        }else{
            if(result.uncertain===false){journal.clear(a);pending=undefined;await refresh()}
            throw new Error(result.error||'暂未收到明确结果，请核对上次操作。');
        }
    }
    async function action(kind:MemoryAction['kind']) {
        if(!state||pending)return;
        const snapshot=state;
        if(kind==='enable'){
            const paths=[...(snapshot.currentScope.roots||[]),...(snapshot.currentScope.files||[])];
            if(!await confirm(`<h2>${snapshot.scopeChanged?'更新追踪范围？':'记录这个项目的文件变化？'}</h2><p>检查这些位置的文件信息与普通文件内容摘要，不移动或删除文件。${snapshot.automatic?'自动检查已授权，恢复后将在应用运行时检查这些位置。':'当前按次检查，不在后台监控。'}</p><ul>${paths.map(p=>`<li class="memory-path">${esc(p)}</li>`).join('')}</ul><p>隐藏配置和依赖目录只记录位置；不会读取 AI 会话正文。</p>`,snapshot.scopeChanged?'使用这些位置':'开启记录')||!live())return;
        }
        if(kind==='automatic_on'){
            const paths=[...(snapshot.currentScope.roots||[]),...(snapshot.currentScope.files||[])];
            if(!await confirm(`<h2>自动检查这个项目？</h2><p>应用运行时，文件变化会触发检查，连续保存会合并处理；同时每 5 分钟补扫，多个项目依次检查。监听不可用时继续定期检查。退出后停止，再次打开会补扫；短暂出现又消失的文件可能无法记录。</p><ul>${paths.map(p=>`<li class="memory-path">${esc(p)}</li>`).join('')}</ul><p>不移动或删除文件。隐藏配置和依赖目录只记录位置，不读取 AI 会话正文。随时可以关闭。</p>`,'开启自动检查')||!live())return;
        }
        const a:MemoryAction={kind,projectKey:key,revision:snapshot.revision,operationID:crypto.randomUUID(),scopeSHA:snapshot.currentScopeSHA};
        await apply(a);
    }
    function eventRow(e:MemoryEvent) {
        const paths=memoryEventPaths(e),path=paths.after||paths.before,actor=memoryEventActor(e);
        return `<li class="memory-event"><div class="memory-event-top"><strong>${esc(e.kind==='file_source_recorded'?'文件来源':e.kind==='source_grant_changed'?'来源读取设置':memoryEventLabel(e))}${path?' · '+esc(name(path)):''}</strong><time datetime="${esc(e.observedAt)}">${esc(date(e.observedAt))}</time></div>${e.note?`<p>${esc(e.note.text)}</p>`:''}<p class="sub">${e.origin?esc(e.origin.claim.agent)+' · ':''}${esc(actor)}</p><details><summary>查看依据与位置</summary><p>${esc(e.reason)}</p>${originDetails(e,esc)}${e.auto?`<p>本次回执：${esc(e.auto.receiptPath)}</p>${e.auto.requestedBy?`<p>提出反对：${esc(e.auto.requestedBy)}</p>`:''}${e.auto.items.slice(0,20).map(item=>`<p class="memory-path">${e.auto?.status==='restore_partial'||e.auto?.status==='restore_failed'?'待核对：':''}${esc(item.before)} → ${esc(item.after)}${item.agent?' · '+esc(item.agent):''}${item.session?' · 会话 '+esc(item.session):''}${item.placementReason?'<br>归类依据：'+esc(item.placementReason):''}${item.sourcePath?'<br>来源：'+esc(item.sourcePath)+' · 第 '+esc(item.line)+' 行 · '+esc(item.recordSHA):''}</p>`).join('')}${e.auto.items.length>20?`<p>共 ${e.auto.items.length} 项，其余可查看项目目录和本次回执。</p>`:''}`:''}${paths.before?`<p class="memory-path">之前：${esc(paths.before)}</p>`:''}${paths.after?`<p class="memory-path">记录位置：${esc(paths.after)}</p>`:''}${e.note?.evidence?`<p>${esc(e.note.evidence)}</p>`:''}<p class="sub">记录 ${esc(e.id)} · 记录时间 ${esc(date(e.observedAt))}</p></details></li>`;
    }
    function automaticStatus(s:MemoryStatus):string {
        if(!s.automatic)return '自动检查未开启';
        if(!s.enabled)return '记录已暂停，自动检查也已停止';
        if(s.scopeChanged)return '等待核对新的项目范围';
        if(s.backgroundError)return '自动检查遇到问题：'+s.backgroundError;
        if(!s.backgroundRunning)return '自动检查已授权，当前观察服务未运行';
        if(s.backgroundChecking)return '正在自动检查…';
        if(s.realtimeWatching)return '正在监听文件变化 · 连续保存合并检查 · 每 5 分钟补扫';
        return s.realtimeError||'应用运行时自动检查 · 每 5 分钟一次';
    }
    function render() {
        if(!live())return;
		const sourcesExpanded=body.querySelector<HTMLDetailsElement>('.memory-sources')?.open||false;
        if(!state||!page){body.innerHTML=`<p>暂时无法读取文件记录。</p>${button('memory-reload','重新读取')}`;q<HTMLButtonElement>('#memory-reload').onclick=()=>run(refreshAndRecover);return}
        const enabled=state.enabled,changed=state.scopeChanged;
        const gaps=[...new Map((state.coverage||[]).filter(c=>c.status!=='complete').map(c=>[c.path,c])).values()];
        const filename=esc(page.project.name||key.slice(key.indexOf(':')+1));
        const fileItems=(files?.items||[]).filter(f=>showHidden||!f.fact.path.split(/[\\/]/).some(p=>p.startsWith('.')));
        body.innerHTML=`<div class="memory-heading"><div><h2>${filename}</h2><p class="sub">${state.revision?(enabled?(state.automatic?'文件记录已开启':'手动记录已开启'):'记录已暂停'):'尚未开始记录'}</p></div><div class="memory-actions">${button('memory-reload','重新读取')}${enabled&&!changed?button('memory-scan','检查变化',true):button('memory-enable',changed?'更新追踪范围':state.revision?'恢复记录':'开始记录',true)}${enabled?button('memory-pause','暂停记录'):''}</div></div>
        ${state.revision?`<div class="memory-auto"><p class="memory-auto-status">${esc(automaticStatus(state))}</p>${state.automatic?button('memory-automatic','关闭自动检查'):enabled&&!changed?button('memory-automatic','开启自动检查'):''}</div>`:''}
        ${state.revision?memorySourcesHTML(state,esc,button):''}
        <div class="memory-updates" hidden><span role="status">项目记录有更新。</span>${button('memory-new','查看新记录')}</div>
        ${pending?`<div class="memory-notice" role="alert"><p>上次操作还没有明确结果。先核对它，再开始新操作。</p>${button('memory-recover','核对上次操作')}${button('memory-retry','重试原操作')}</div>`:''}
        ${changed?'<p class="memory-notice">项目登记的位置有变化。更新追踪范围后才能继续检查，旧记录会保留。</p>':''}
        ${state.projectionPending?'<p class="memory-notice">事件已保存，目录文件待更新；这里显示的是从账本恢复的记录。</p>':''}
        <p class="memory-summary">${state.lastObservedAt?'上次检查：'+esc(date(state.lastObservedAt)):'还没有检查过文件'}${state.revision?` · 已记录 ${state.tracked} 项${state.missing?' · 原位置未找到 '+state.missing+' 项':''}`:''}</p>
        ${state.lastObservedAt&&!state.observationComplete?`<div class="memory-notice"><strong>上次检查未覆盖全部目录</strong><p>未检查的部分保留最近记录，不代表现在仍然存在。</p><details><summary>查看未检查的位置（${gaps.length}）</summary><ul>${gaps.slice(0,30).map(g=>`<li><span class="memory-path">${esc(g.path)}</span><p class="sub">${esc(g.reason)}</p></li>`).join('')}</ul>${gaps.length>30?`<p>另有 ${gaps.length-30} 处覆盖说明，完整内容保留在项目记忆中。</p>`:''}</details></div>`:''}
        <div class="memory-section-head"><h2>${showFiles?'记录中的目录':'最近变化'}</h2><div>${button('memory-toggle',showFiles?'查看最近变化':'查看目录')}<a href="#/graph/${encodeURIComponent(key)}/events">在关系网查看</a></div></div>
        ${showFiles?`<label class="memory-hidden"><input type="checkbox" id="memory-hidden" ${showHidden?'checked':''}> 显示隐藏项</label><p class="sub">这是最近记录的位置，打开前请到文件夹页核对。目录没有保存历史正文副本。</p>${files?`<ul class="memory-files">${fileItems.map(f=>`<li><strong>${esc(name(f.fact.path))}</strong><span>${esc({present:'最近记录可见',missing:'原位置未找到',untracked:'已停止追踪'}[f.status]||f.status)}</span><p class="memory-path">${esc(f.fact.path)}</p></li>`).join('')||'<li>这一页没有可显示的条目。</li>'}</ul><div class="memory-pagination"><span>${files.total?fileOffset+1:0}–${Math.min(fileOffset+100,files.total)} / ${files.total} 项</span>${fileOffset?button('memory-file-prev','上一页'):''}${fileOffset+100<files.total?button('memory-file-next','下一页'):''}</div>`:'<p>正在读取目录…</p>'}`:`${page.items.length?`<ol class="memory-events" aria-label="最近文件变化">${page.items.map(eventRow).join('')}</ol>`:`<div class="memory-empty"><h3>${state.revision?'还没有记录到文件变化':'先记录这个项目的文件'}</h3><p>${state.revision?'点击「检查变化」，建立当前目录记录。':'开启后按次检查，记录文件出现、改写和位置变化。'}</p></div>`}${page.next?button('memory-more','更早的记录'):''}`}`;
        const bind=(id:string,fn:()=>Promise<void>)=>{const el=body.querySelector<HTMLButtonElement>('#'+id);if(el)el.onclick=()=>run(fn,id)};
        bind('memory-reload',refreshAndRecover);
        bind('memory-new',refreshAndRecover);
        bind('memory-automatic',()=>action(state?.automatic?'automatic_off':'automatic_on'));
        bind('memory-enable',()=>action('enable'));bind('memory-scan',()=>action('scan'));bind('memory-pause',()=>action('pause'));
        bind('memory-recover',async()=>{await recover()});bind('memory-retry',async()=>{if(pending)await apply(pending)});
        bind('memory-toggle',async()=>{showFiles=!showFiles;if(showFiles)await loadFiles(0);else render()});
        bind('memory-file-prev',()=>loadFiles(Math.max(0,fileOffset-100)));bind('memory-file-next',()=>loadFiles(fileOffset+100));
        const hidden=body.querySelector<HTMLInputElement>('#memory-hidden');if(hidden)hidden.onchange=()=>{showHidden=hidden.checked;render();q<HTMLInputElement>('#memory-hidden').focus()};
        bind('memory-more',async()=>{if(!page||!state)return;const old=page;const n=epoch;const more=await api<MemoryPage>('ProjectMemoryEvents',key,state.revision,page.next);if(!live()||n!==epoch)return;page={...more,items:[...old.items,...more.items]};render();say('已加载更早的记录。')});
        bindMemorySources(body,state,(adapter,path,enabled,directory)=>void run(()=>sourceAction(adapter,path,enabled,directory)),async adapter=>{
            const info=await api<{home:string}>('Info');return adapter==='codex'?info.home+'/.codex/sessions':adapter==='claude'?info.home+'/.claude/projects':adapter==='gemini'?info.home+'/.gemini/tmp':'';
        });
		const sourcesPanel=body.querySelector<HTMLDetailsElement>('.memory-sources');if(sourcesPanel)sourcesPanel.open=sourcesExpanded;
        if(pending)for(const el of body.querySelectorAll<HTMLButtonElement>('#memory-enable,#memory-scan,#memory-pause,#memory-automatic,[id^="memory-source-"]')){el.disabled=true;el.dataset.unavailable='true'}
        setBusy(busy);
    }
    async function loadFiles(offset:number){if(!state)return;const n=epoch;try{const data=await api<MemoryFiles>('ProjectMemoryFiles',key,state.revision,offset);if(!live()||n!==epoch)return;files=data;fileOffset=offset;render()}catch(e){if(live()&&n===epoch&&!files)showFiles=false;throw e}}
    let polling=false;
    const timer=setInterval(async()=>{
        if(!live()||busy||pending||polling||!state?.revision||document.hidden)return;
        polling=true;const n=epoch;
        try{
            const next=await api<MemoryStatus>('ProjectMemory',key);
            if(!live()||busy||n!==epoch)return;
            const status=body.querySelector<HTMLElement>('.memory-auto-status');if(status)status.textContent=automaticStatus(next);
            const updates=body.querySelector<HTMLElement>('.memory-updates');if(updates&&next.revision!==state.revision)updates.hidden=false;
        }catch(e){if(live()&&!busy&&n===epoch)say('自动状态暂时无法更新，当前记录保留。'+errorText(e))}
        finally{polling=false}
    },15000);
    void run(refreshAndRecover);
    return ()=>{disposed=true;epoch++;clearInterval(timer)};
}
