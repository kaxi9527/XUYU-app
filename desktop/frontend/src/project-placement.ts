import {organizerAction as button} from './ui-actions';
import {mountStructure,selectedFiles,type PreviewPlan} from './organize-structure';
import type {MemoryAPI} from './project-memory-types';
import './organize-simple.css';
import './organize-result.css';
import './project-placement.css';

type PlacementFile={id:string;source:string;target?:string;folder:string;action:string;reason:string;stage?:string};
type Preview={token:string;expiresAt:string;project:string;target:string;folders:string[];rows:PlacementFile[];untouched:number;undo:boolean};
type UndoGroup={id:string;sha:string;status:string;restored:number;total:number};
type Operation={id:string;intentSHA:string;project:string;revision:number;status:string;running:boolean;activeUndoGroupID?:string;needsReconciliation:boolean;stopRequested:boolean;problem?:string;rows:PlacementFile[];undoGroups:UndoGroup[]};
type Params={key:string;kind?:string;id?:string;operationID?:string};
type Saved={id:string;groupID?:string};
export const placementLink=(key:string,kind:string,id:string)=>'#/placement/'+encodeURIComponent(JSON.stringify({key,kind,id}));

export function mountProjectPlacement(view:HTMLElement,arg:string,api:MemoryAPI,esc:(v:unknown)=>string):()=>void {
    let params:Params;
    try{params=JSON.parse(arg);if(typeof params.key!=='string')throw new Error()}catch{view.textContent='没有找到要整理的项目，请返回项目重新打开。';return ()=>{}}
    const key=params.key,storageKey='workshelf:placement:v1:'+key;
    const root=document.createElement('section');root.className='organizer-simple project-placement';view.replaceChildren(root);
    const projectLink=key.startsWith('root:')?'#/project/'+encodeURIComponent(key.slice(5)):'#/place/'+encodeURIComponent(key.slice(6));
    root.innerHTML=`<header class="bar"><a href="${projectLink}">← 项目</a><h1>按建议整理</h1></header><p class="placement-feedback" role="status" aria-live="polite">正在读取…</p><div class="placement-body"></div>`;
    const body=root.querySelector<HTMLElement>('.placement-body')!,feedback=root.querySelector<HTMLElement>('.placement-feedback')!;
    let disposed=false,preview:Preview|undefined,plan:PreviewPlan|undefined,operation:Operation|undefined,saved:Saved|undefined,working='',poll:ReturnType<typeof setTimeout>|undefined,limit=50;
    const choices=new Map<string,boolean>();
    const live=()=>!disposed&&root.isConnected;
    const say=(text:string)=>{if(live())feedback.textContent=text};
    const message=(e:unknown)=>e instanceof Error?e.message:String(e);
    const persist=(value:Saved)=>{localStorage.setItem(storageKey,JSON.stringify(value));saved=value};
    const release=()=>{if(preview){void api('ReleaseProjectPlacementPreview',key,preview.token).catch(()=>{});preview=undefined;plan=undefined}};
    const bind=(id:string,fn:()=>void)=>{const el=root.querySelector<HTMLElement>('#'+id);if(el)el.onclick=fn};
    const disable=()=>root.querySelectorAll<HTMLElement>('mdui-button').forEach(el=>{(el as HTMLElement&{disabled:boolean}).disabled=!!working&&el.id!=='placement-stop'||el.id==='placement-confirm'&&!count()});
    const action=async(fn:()=>Promise<void>)=>{if(working)return;try{await fn()}catch(e){if(working==='confirm'){release();operation=undefined}say(message(e));render()}finally{working='';if(live()){render();schedule()}}};
    const count=()=>plan?selectedFiles(plan,choices).length:0;
    const activeGroup=()=>{
        if(!operation)return;
        if(operation.running)return operation.undoGroups.find(g=>g.id===operation!.activeUndoGroupID);
        const savedGroup=operation.undoGroups.find(g=>g.id===saved?.groupID);
        if(savedGroup)return savedGroup;
        const waiting=operation.undoGroups.filter(g=>g.status==='confirmed'||g.status==='running');
        return waiting.length===1?waiting[0]:undefined;
    };
    function updateConfirm(){const el=root.querySelector('#placement-confirm');if(el){el.textContent=`${preview?.undo?'恢复':'确认整理'} ${count()} 项`;(el as HTMLElement&{disabled:boolean}).disabled=!count()||!!working}}
    function render(){
        if(!live())return;
        if(preview&&plan){
            body.innerHTML=`<h2>${preview.undo?'恢复到整理前的位置':'整理后，文件会在这里'}</h2><p class="sub">${preview.undo?'只恢复能核实的原文件。遇到新文件或修改会保留现场。':'已经按文件夹分组，可以直接确认；不想整理的整组留在原处。'}</p>${preview.untouched?`<p class="placement-notice">另有 ${preview.untouched} 份记录不在这次建议中，保持原处。</p>`:''}<div id="placement-structure"></div><footer class="placement-actions">${button('placement-all','重新全选')}${button('placement-cancel','取消')}${button('placement-confirm',`${preview.undo?'恢复':'确认整理'} ${count()} 项`,true)}</footer>`;
            mountStructure(root.querySelector('#placement-structure')!,plan,choices,esc,updateConfirm,()=>{},'overview',undefined,{retarget:false,restore:preview.undo});
            bind('placement-all',()=>{choices.clear();render()});
            bind('placement-cancel',()=>{release();render();say('已取消预览，文件没有移动。')});
            bind('placement-confirm',()=>void action(confirmAndExecute));updateConfirm();disable();return;
        }
        if(operation){
            const done=operation.rows.filter(r=>r.stage==='moved'||r.stage==='copied').length,restored=operation.rows.filter(r=>r.stage==='undone').length;
            const pending=operation.rows.filter(r=>r.stage==='pending').length,uncertain=operation.rows.some(r=>r.stage==='claimed'||r.stage==='uncertain');
            const running=operation.running||working==='execute';
            const last=activeGroup();
            const title=running?'正在处理文件':last?last.status==='completed'?'恢复已结束':'本次恢复已停止':operation.status==='completed'?'整理完成':operation.status==='stopped'?'整理已停止':'本次整理记录';
            const groups=new Map<string,PlacementFile[]>();operation.rows.forEach(r=>{const rows=groups.get(r.folder)||[];rows.push(r);groups.set(r.folder,rows)});
            body.innerHTML=`<h2>${title}</h2><p>${done} 项已整理 · ${restored} 项已恢复${pending?` · ${pending} 项未开始`:''}</p>${operation.problem?`<p class="placement-notice">${esc(operation.problem)}</p>`:''}${operation.needsReconciliation&&!running?'<p class="placement-notice">上次操作尚需核对，当前不会自动再次移动。已完成和未开始的记录都保留。</p>':''}<div class="result-directory"><div class="result-directory-scroll">${[...groups].map(([folder,rows])=>`<details class="result-folder" open><summary><span class="folder-shape" aria-hidden="true"></span><span>${esc(folder||'原来的位置')}</span><small>${rows.length} 项</small></summary><ul class="result-file-list">${rows.slice(0,limit).map(r=>`<li><details class="placement-result-file"><summary><strong>${esc((r.stage==='undone'?r.source:r.target||r.source).split('/').pop())}</strong><span>${esc(({pending:'未开始',moved:'已归位',copied:'已复制，原件保留',undone:'已恢复',claimed:'需要核对',uncertain:'需要核对',memory_pending:'等待写入记录',reconciliation_pending:'等待补记',reconciled:'已核对',unchanged:'未移动',failed:'未完成'} as Record<string,string>)[r.stage||'']||r.stage)}</span></summary><p>${esc(r.reason)}</p><p class="memory-path">原位置：${esc(r.source)}</p><p class="memory-path">整理位置：${esc(r.target||'保持原处')}</p></details></li>`).join('')}</ul>${rows.length>limit?`<p class="sub">已显示 ${limit} / ${rows.length} 项</p>`:''}</details>`).join('')}</div></div><footer class="placement-actions">${[...groups.values()].some(rows=>rows.length>limit)?button('placement-more','再显示 50 项'):''}${running?button('placement-stop','停止后续'):button('placement-refresh','重新读取')}${!running&&!uncertain&&!operation.stopRequested&&operation.status==='confirmed'?button('placement-resume','继续本次整理',true):''}${!running&&last&&last.status==='confirmed'?button('placement-resume-undo','继续本次恢复',true):''}${!running&&done?button('placement-undo','撤销本次整理'):''}${!running&&params.id?button('placement-new','查看新的整理预览'):''}<a href="#/memory/${encodeURIComponent(key)}">文件记录</a><a href="#/graph/${encodeURIComponent(key)}/events">关系网</a></footer>`;
            bind('placement-refresh',()=>void action(refresh));bind('placement-more',()=>{limit+=50;render()});
            bind('placement-resume',()=>void action(execute));bind('placement-resume-undo',()=>void action(execute));
            bind('placement-stop',()=>void stop());bind('placement-undo',()=>void action(previewUndo));bind('placement-new',()=>void action(newPreview));disable();return;
        }
        body.innerHTML=`<p>${saved?'上次请求的结果还没有读到。先核对原记录，不会重复执行。':'选择一份已完成的分析，查看实际文件去向。'}</p><div class="placement-actions">${saved?button('placement-refresh','核对上次请求',true):''}${params.id?button('placement-preview','查看整理预览',!saved):''}<a href="#/analysis/${encodeURIComponent(key)}">返回整理建议</a></div>`;
        bind('placement-refresh',()=>void action(refresh));bind('placement-preview',()=>void action(newPreview));disable();
    }
    async function loadPreview(promise:Promise<Preview>){
        working='preview';say('正在核对文件和目标位置…');render();
        const result=await promise;if(!live()){void api('ReleaseProjectPlacementPreview',key,result.token).catch(()=>{});return}
        release();preview=result;choices.clear();
        // Labels group original folders without exposing a false destination.
        const undoFolders=new Map(result.folders.map((f,i)=>[f,`原位置 ${i+1} · ${f.split('/').filter(Boolean).pop()||f}`]));
        plan={project:result.project,target:result.target,folders:result.undo?[...undoFolders.values()]:result.folders,rows:result.rows.map(r=>({id:r.id,source:r.source,target:r.target,folder:result.undo?undoFolders.get(r.folder):r.folder,reason:r.reason,selected:r.action==='move'||r.action==='copy',blocked:r.action==='keep',directory:false,crossDevice:r.action==='copy'}))};
        say('预览已准备好。确认前不会移动文件。');
    }
    async function newPreview(){
        if(!params.id||!params.kind)return;
        await loadPreview(api<Preview>('PreviewProjectPlacement',key,params.kind,params.id));
    }
    async function previewUndo(){if(operation)await loadPreview(api<Preview>('PreviewProjectPlacementUndo',key,operation.id,operation.intentSHA))}
    async function confirmAndExecute(){
        if(!preview||!plan||!count())return;
        const selected=selectedFiles(plan,choices).map(r=>r.id),token=preview.token,undo=preview.undo;
        working='confirm';say('正在保存这次确认…');disable();
        if(undo){
            if(!operation)throw new Error('请重新读取原整理记录');
            const groupID=crypto.randomUUID();persist({id:operation.id,groupID});
            operation=await api<Operation>('ConfirmProjectPlacementUndo',key,token,groupID,selected);
        }else{
            const id=crypto.randomUUID();persist({id});
            operation=await api<Operation>('ConfirmProjectPlacement',key,token,id,selected);
        }
        release();await execute();
    }
    async function execute(){
        if(!operation)return;
        working='execute';say('正在处理文件，可以随时停止后续。');render();schedule();
        const group=activeGroup();
        const result=group?await api<Operation>('ExecuteProjectPlacementUndo',key,operation.id,operation.intentSHA,group.id,group.sha):await api<Operation>('ExecuteProjectPlacement',key,operation.id,operation.intentSHA);
        if(!live())return;operation=result;say(result.problem||'本次操作已结束，结果和来源已记录。');
    }
    async function refresh(){if(!saved&&!params.operationID)return;const result=await api<Operation>('ProjectPlacementOperation',key,saved?.id||params.operationID);if(live()){operation=result;say('已读取原操作记录，没有重新移动文件。')}}
    async function stop(){
        if(!operation)return;
        const group=activeGroup();
        try{await api('StopProjectPlacement',key,operation.id,operation.intentSHA,group?.id||'',group?.sha||'');say('已请求停止，正在完成当前文件的记录。')}catch(e){say(message(e))}
    }
    function schedule(){
        if(poll)clearTimeout(poll);
        if(!live()||!operation||!(operation.running||working==='execute'))return;
        poll=setTimeout(async()=>{try{const next=await api<Operation>('ProjectPlacementOperation',key,operation!.id);if(live()&&(!operation||next.revision>=operation.revision)){operation=next;render()}}catch(e){say('暂时没有读到最新进度：'+message(e))}finally{schedule()}},3000);
    }
    try{const raw=localStorage.getItem(storageKey);if(raw){const p=JSON.parse(raw);if(typeof p.id!=='string'||!/^[a-zA-Z0-9_-]{1,128}$/.test(p.id)||p.groupID!==undefined&&(typeof p.groupID!=='string'||!/^[a-zA-Z0-9_-]{1,128}$/.test(p.groupID)))throw new Error('本地整理记录无法读取');saved=p}if(params.operationID&&saved?.id!==params.operationID)persist({id:params.operationID});render();void action(saved||params.operationID?refresh:newPreview)}catch(e){say(message(e));render()}
    return ()=>{disposed=true;if(poll)clearTimeout(poll);release()};
}
