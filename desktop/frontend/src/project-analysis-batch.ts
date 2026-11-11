import {organizerAction as button,restoreActionFocus} from './ui-actions';
import {debitDescription,recipientRows,AnalysisJournal,type AnalysisScopePage,type Packet,type Quota} from './project-analysis-types';
import type {MemoryAPI} from './project-memory-types';
import {batchLabels,batchProblems,type BatchPreview,type BatchItem,type BatchDetail,type BatchHistory} from './project-analysis-batch-types';
import './project-analysis.css';
import './project-analysis-batch.css';

export const batchLink=(key:string,scopeSHA='',scopeID='')=>'#/analysis-batch/'+encodeURIComponent(JSON.stringify({key,scopeSHA,scopeID}));

export function mountProjectAnalysisBatch(view:HTMLElement,arg:string,api:MemoryAPI,esc:(x:unknown)=>string,confirm:(html:string,ok:string)=>Promise<boolean>):()=>void{
    let params:{key:string;scopeSHA?:string;scopeID?:string};
    try{params=arg.startsWith('{')?JSON.parse(arg):{key:arg};if(typeof params.key!=='string'||!params.key)throw new Error()}catch{view.innerHTML='<p role="alert">分析范围无法读取，请返回项目重新选择。</p>';return()=>{}}
    const {key}=params,root=document.createElement('section');root.className='analysis-screen batch-screen';view.replaceChildren(root);
    root.innerHTML=`<header class="bar"><a class="back" href="#/analysis/${encodeURIComponent(key)}">← 选择范围</a><h1>整理建议</h1></header><p class="analysis-feedback" role="status" aria-live="polite">正在读取分析记录…</p><div class="batch-body"></div>`;
    const body=root.querySelector<HTMLElement>('.batch-body')!,feedback=root.querySelector<HTMLElement>('.analysis-feedback')!,journal=new AnalysisJournal('batch:'+key);
    let disposed=false,busy=false,polling=false,epoch=0,preview:BatchPreview|undefined,item:BatchItem|undefined,detail:BatchDetail|undefined,history:BatchHistory|undefined,pending:string|undefined,consent=false,quotaConsent=false,payloadIndex=0,offset=0,rendered='',noteUntil=0;
    const fileNames=new Map<string,string>(),folderNames=new Map<string,string>(),visible=new Map<string,number>();
    let submissionProblem='';
    const live=()=>!disposed&&root.isConnected;
    const error=(e:unknown)=>e instanceof Error?e.message:String(e);
    const say=(s:string)=>{if(live())feedback.textContent=s};
    const quota=(q:Quota)=>`${q.requests} 次 / ${Math.ceil(q.inputBytes/1024)} KB 输入 / ${q.outputTokens} 输出 token`;
    const receiverHTML=(r:BatchItem["recipient"])=>`<dl class="analysis-recipient">${recipientRows(r).map(([label,value])=>`<div><dt>${esc(label)}</dt><dd>${esc(value)}</dd></div>`).join("")}</dl>`;
    const quotaChanges=()=>!!preview&&(['requests','inputBytes','outputTokens'] as const).some(k=>preview!.spec.quota[k]!==preview!.spec.previousQuota[k]);
    const status=(r:BatchItem)=>r.status==='active'&&r.canResume?'等待继续':batchLabels[r.status]||r.status;
    const finished=(r:BatchItem)=>r.status==='completed'||r.status==='stopped';
    const renderKey=()=>JSON.stringify([item?.id,item?.status,item?.problem,item?.canResume,item?.noteSaved,!!item?.running,!!detail]);
    function disabled(){
        root.querySelectorAll<HTMLElement>('mdui-button').forEach(el=>(el as HTMLElement&{disabled:boolean}).disabled=busy||el.id==='batch-start'&&(!consent||quotaChanges()&&!quotaConsent));
        root.querySelectorAll<HTMLInputElement>('input').forEach(el=>el.disabled=busy);
    }
    function render(){
        if(!live())return;
        const focused=document.activeElement instanceof HTMLElement&&root.contains(document.activeElement)?document.activeElement.id:'';
        const opened=[...body.querySelectorAll<HTMLDetailsElement>('details[open]')].map(d=>d.id),scroll=view.scrollTop;
        rendered=renderKey();
        body.innerHTML=preview?previewHTML(preview):pending&&!item?`<section class="batch-state"><h2>正在核对这次分析</h2><p>请求可能已经开始。这里只查询项目记录，不会再次发送。</p><div class="analysis-actions">${button('batch-check','刷新状态',true)}${button('batch-dismiss','结束核对')}</div></section>`:item?resultHTML(item):`<section class="batch-state"><h2>一次确认，连续分析</h2><p>选择整个项目或一个目录，序屿 会自动分组处理，再把建议汇总在一起。</p><div class="analysis-actions">${button('batch-preview','分析整个项目',true)}<a href="#/analysis/${encodeURIComponent(key)}">选择其他目录</a></div><p class="analysis-meta">先预览范围、模型和调用上限，确认后才发送。</p></section>`;
        if(!preview)body.insertAdjacentHTML('beforeend',historyHTML());
        const bind=(id:string,fn:()=>Promise<void>|void)=>{const el=body.querySelector<HTMLElement>('#'+id);if(el)el.onclick=()=>void run(fn,id)};
        bind('batch-preview',()=>prepare());bind('batch-cancel-preview',release);bind('batch-start',start);bind('batch-check',()=>recover());
        bind('batch-pause',async()=>{if(!item)return;item=await api<BatchItem>('PauseProjectAnalysisBatch',key,item.id);if(!live())return;render();say('已暂停后续分析。正在发送的一组会完成，不会再启动下一组。')});
        bind('batch-resume',resume);bind('batch-stop',stop);bind('batch-dismiss',dismiss);
        bind('batch-new',async()=>{if(pending)throw new Error('请先暂停或结束当前分析。');item=undefined;detail=undefined;await prepare()});
        bind('batch-note',async()=>{if(!item)return;await api('RecordProjectAnalysisBatchNote',key,item.id);await recover()});
        bind('batch-history-refresh',async()=>{await refreshHistory(0)});bind('batch-history-prev',async()=>{await refreshHistory(Math.max(0,offset-20))});bind('batch-history-next',async()=>{await refreshHistory(offset+20)});
        history?.items.forEach((r,i)=>bind('batch-history-'+i,async()=>{if(pending&&pending!==r.id)throw new Error('请先暂停当前分析，再查看其他记录。');epoch++;preview=undefined;item=r;detail=undefined;await recover(r.id)}));
        body.querySelectorAll<HTMLElement>('[data-more]').forEach(el=>el.onclick=()=>{const group=el.dataset.more!;visible.set(group,(visible.get(group)||50)+50);render()});
        const consentEl=body.querySelector<HTMLInputElement>('#batch-consent');if(consentEl)consentEl.onchange=()=>{consent=consentEl.checked;disabled()};
        const quotaEl=body.querySelector<HTMLInputElement>('#batch-quota-consent');if(quotaEl)quotaEl.onchange=()=>{quotaConsent=quotaEl.checked;disabled()};
        const select=body.querySelector<HTMLSelectElement>('#batch-payload-index');if(select)select.onchange=()=>{payloadIndex=Number(select.value);render()};
        for(const id of opened){const el=body.querySelector<HTMLDetailsElement>('#'+id);if(el)el.open=true}
        disabled();updateProgress();if(focused)body.querySelector<HTMLElement>('#'+focused)?.focus({preventScroll:true});view.scrollTop=scroll;
    }
    function previewHTML(p:BatchPreview):string{
        const spec=p.spec,receiver=spec.requests[0].recipient,packet=JSON.parse(spec.requests[0].request.input) as Packet,selected=spec.requests[payloadIndex]||spec.requests[0];
        return `<section class="analysis-preview" aria-labelledby="batch-preview-title"><h2 id="batch-preview-title" tabindex="-1">${esc(spec.scopeName)}</h2><p>${esc(packet.body.projectName)} · 共 ${p.totalFiles} 份文件记录，将自动完成 ${p.required.requests} 组分析。</p>${receiverHTML(receiver)}<div class="analysis-includes"><strong>一次确认，后续各组自动处理</strong><p>只发送文件记录、相对路径、目录规则和项目笔记，不读取文件正文。</p><p>最多 ${esc(quota(p.required))}。费用由服务商计收，金额可能未知。</p><p>离开此页后会继续；可随时暂停后续组。授权在开始后两小时到期。</p></div><details id="batch-payload" class="analysis-payload"><summary>查看完整发送内容（${p.required.requests} 组）</summary><label for="batch-payload-index">查看哪一组</label><select id="batch-payload-index">${spec.requests.map((_,i)=>`<option value="${i}" ${i===payloadIndex?'selected':''}>第 ${i+1} 组</option>`).join('')}</select><h3>分析要求</h3><pre tabindex="0">${esc(selected.request.system)}</pre><h3>项目资料</h3><pre tabindex="0">${esc(selected.request.input)}</pre></details>${quotaChanges()?`<div class="batch-quota-change"><h3>本项目需要增加分析额度</h3><p>原 24 小时上限：${esc(quota(spec.previousQuota))}</p><p>调整后上限：${esc(quota(spec.quota))}</p><label class="analysis-consent"><input id="batch-quota-consent" type="checkbox" ${quotaConsent?'checked':''}><span>同意调整本项目的 24 小时次数和数据上限。此设置会保留，仅影响这个项目，不是金额上限。</span></label></div>`:''}<label class="analysis-consent"><input id="batch-consent" type="checkbox" ${consent?'checked':''}><span>同意将这个范围的全部分组发送给上述模型，接受费用可能未知。未发送或失败的组也会保守占用本项目预留额度，不会自动重试。</span></label><div class="analysis-actions">${button('batch-start','开始分析',true)}${button('batch-cancel-preview','取消')}<a href="#/models">模型与账号</a></div></section>`;
    }
    function filesHTML(ids:string[],group:string):string{
        const count=visible.get(group)||50;return `<ul class="analysis-file-list" tabindex="0" aria-label="文件记录">${ids.slice(0,count).map(id=>`<li>${esc(fileNames.get(id)||id)}</li>`).join('')}</ul>${ids.length>count?button('batch-more-'+group,`再显示 ${Math.min(50,ids.length-count)} 份`).replace('<mdui-button','<mdui-button data-more="'+group+'"'):''}`;
    }
    function resultHTML(r:BatchItem):string{
        const terminal=finished(r),summary=detail?.summary;
        const placement= r.completed>0&&r.status!=='active'?`<p><a class="placement-entry" href="${esc(placementLink(key,'batch',r.id))}">按已有建议整理文件 →</a></p>`:'';
        const debitHTML=detail?`<ol class="analysis-file-list analysis-debits" tabindex="0" aria-label="各组扣费回执">${detail.batch.items.map((part,i)=>`<li>第 ${i+1} 组：${esc(debitDescription(part.usage,part.status))}</li>`).join('')}</ol>`:'<p class="analysis-meta">扣费回执尚未读取。</p>';
        return placement+`<section class="batch-state" aria-labelledby="batch-status"><div class="analysis-section-title"><h2 id="batch-status">${esc(status(r))}</h2><span>${esc(r.scopeName)}</span></div><p id="batch-count"></p><progress id="batch-progress" aria-label="分组分析进度" max="${r.total}" value="${r.completed}"></progress>${r.problem?`<p class="batch-problem">${esc(batchProblems[r.problem]||'分析未能继续，请查看项目记录。')}</p>`:''}${!terminal?`<p class="analysis-meta">${r.status==='paused'?(r.running?'当前一组完成后停下。':'已完成的组会保留。继续时只发送尚未处理的组。'):'后续各组自动处理。暂停会在当前一组结束后生效。'} 离开此页不会重发请求。</p><div class="analysis-actions">${r.status==='active'?button('batch-pause','暂停'):''}${r.canResume?button('batch-resume','继续分析',true):''}${button('batch-stop','结束分析')}${button('batch-check','刷新状态')}</div>`:`<p>${r.noteSaved?'汇总已记入这个项目的文件记录。':'分析记录已保存，项目汇总尚未写入。'} 文件没有移动。</p>${!r.noteSaved?button('batch-note','补记汇总'):''}`}${summary?`<div class="analysis-groups">${summary.groups.map((g,i)=>`<details id="batch-group-${i}"><summary><span>${esc(folderNames.get(g.folderID)||g.folderID)}</span><span>${g.fileIDs.length} 份</span></summary>${g.reasons.map(reason=>`<p>${esc(reason)}</p>`).join('')}${filesHTML(g.fileIDs,'g'+i)}</details>`).join('')}${summary.uncertainIDs.length?`<details id="batch-uncertain"><summary><span>暂时无法确定</span><span>${summary.uncertainIDs.length} 份</span></summary><p>这些文件已经分析，但依据不足，建议留在原处。</p>${filesHTML(summary.uncertainIDs,'uncertain')}</details>`:''}${summary.unanalyzedIDs.length?`<details id="batch-unanalyzed"><summary><span>尚未分析</span><span>${summary.unanalyzedIDs.length} 份</span></summary><p>这些文件没有成功的分析结果，不能当成已整理。</p>${filesHTML(summary.unanalyzedIDs,'unanalyzed')}</details>`:''}</div>`:''}<details id="batch-recipient-record" class="analysis-source"><summary>本次模型与发送记录</summary>${receiverHTML(r.recipient)}${debitHTML}</details>${terminal?`<div class="analysis-actions">${button('batch-new','重新选择分析范围')}<a href="#/memory/${encodeURIComponent(key)}">查看文件记录</a></div>`:''}</section>`;
    }
    function historyHTML():string{return `<section class="analysis-history"><div class="analysis-section-title"><h2>范围分析记录</h2>${button('batch-history-refresh','刷新记录')}</div>${history?`<p class="analysis-meta">共 ${history.total} 次分析${history.archived?` · ${history.archived} 次已归档，原结果仍可查看`:""}</p>`:""}${!history?'<p>暂时无法读取记录，可刷新重试。</p>':!history.items.length?'<p class="analysis-meta">还没有范围分析记录。</p>':`<ul>${history.items.map((r,i)=>`<li><div><strong>${esc(r.scopeName)} · ${r.analyzedFiles} / ${r.totalFiles} 份</strong><span>${esc(status(r))} · ${esc(new Date(r.createdAt).toLocaleString('zh-CN'))}${r.archivedAt?" · 已归档":""}</span></div>${button('batch-history-'+i,'查看')}</li>`).join('')}</ul>`}<div class="analysis-actions">${offset?button('batch-history-prev','上一页'):''}${history&&offset+history.items.length<history.total?button('batch-history-next','下一页'):''}</div></section>`}
    function updateProgress(){if(!item)return;const count=body.querySelector('#batch-count');if(count)count.textContent=`已完成 ${item.completed} / ${item.total} 组，覆盖 ${item.analyzedFiles} / ${item.totalFiles} 份文件。`;const progress=body.querySelector<HTMLProgressElement>('#batch-progress');if(progress)progress.value=item.completed}
    async function run(fn:()=>Promise<void>|void,focus=''){if(busy||!live())return;busy=true;disabled();try{await fn()}catch(e){say(error(e))}finally{busy=false;if(live()){disabled();if(focus)await restoreActionFocus(body.querySelector<HTMLElement>('#'+focus),()=>live()&&!root.contains(document.activeElement))}}}
    async function release(){const token=preview?.token;preview=undefined;consent=false;quotaConsent=false;epoch++;if(token)await api('ReleaseProjectAnalysisBatch',token);if(live()){render();say('已取消预览，没有开始新的分析。')}}
    async function prepare(scopeSHA='',scopeID=''){
        if(pending)throw new Error('请先核对当前分析。');await release();const mine=++epoch;item=undefined;detail=undefined;say('正在准备整个范围的发送预览…');render();
        if(!scopeSHA||!scopeID){const scope=await api<AnalysisScopePage>('ProjectAnalysisScopes',key,'','',0);scopeSHA=scope.sha;scopeID=scope.id}
        if(!live()||mine!==epoch)return;
        const next=await api<BatchPreview>('PreviewProjectAnalysisBatch',key,scopeSHA,scopeID);
        if(!live()||mine!==epoch){await api('ReleaseProjectAnalysisBatch',next.token);return}
        preview=next;payloadIndex=0;render();say('预览已准备好，还没有调用模型。');body.querySelector<HTMLElement>('#batch-preview-title')?.focus({preventScroll:true});
    }
    async function start(){
        if(!preview||!consent||quotaChanges()&&!quotaConsent)return;
        const p=preview,id=crypto.randomUUID();journal.save(id);pending=id;epoch++;preview=undefined;item=undefined;detail=undefined;submissionProblem='';render();
        try{const next=await api<BatchItem>('ConfirmProjectAnalysisBatch',key,p.token,id,true,quotaConsent,false);if(!live())return;item=next;render();say('已开始，将自动分析后续分组。')}catch(e){submissionProblem=error(e);if(live())say('正在核对是否已开始；不会重复发送。'+submissionProblem)}
        if(live())await recover(id);
    }
    async function refreshHistory(nextOffset=offset){const mine=epoch;try{const next=await api<BatchHistory>('ProjectAnalysisBatchHistoryPage',key,nextOffset,nextOffset?history?.revision||0:0);if(!live()||mine!==epoch)return;history=next;offset=nextOffset;render()}catch(e){say('分析记录读取失败：'+error(e))}}
    async function recover(id=pending||item?.id){
        if(!id||polling)return;polling=true;const mine=epoch;
        try{const next=await api<BatchItem>('ProjectAnalysisBatchResult',key,id);if(!live()||mine!==epoch)return;submissionProblem='';const old=item,oldHistory=history;item=next;
            if(old?.status!==next.status||old?.completed!==next.completed||offset===0&&!history?.items.some(r=>r.id===id)){try{const updated=await api<BatchHistory>('ProjectAnalysisBatchHistoryPage',key,offset,offset?history?.revision||0:0);if(!live()||mine!==epoch)return;history=updated}catch{history=undefined}}
            if(old?.status!==next.status)say(next.status==='completed'?'整个范围已分析完成，可以按文件夹查看汇总。':next.status==='stopped'?'分析已结束。已完成和尚未分析的文件会分别列出。':next.status==='paused'?'后续分析已暂停，已完成部分会保留。':'正在自动处理后续分组，无需逐组确认。');
            if(finished(next)||next.status==='paused'&&!next.running){journal.clear(id);if(pending===id)pending=undefined}
            if(finished(next)&&(!old||!finished(old)))noteUntil=Date.now()+6000;
            if((finished(next)||next.status==='paused'&&!next.running)&&(detail?.item.id!==id||detail.item.completed!==next.completed||detail.item.status!==next.status)){const full=await api<BatchDetail>('ProjectAnalysisBatchDetail',key,id);if(!live()||mine!==epoch)return;detail=full;fileNames.clear();folderNames.clear();visible.clear();for(const r of full.batch.spec.requests){const packet=JSON.parse(r.request.input) as Packet;for(const f of packet.body.files)fileNames.set(f.id,f.position.relative);for(const f of packet.body.folders)folderNames.set(f.id,f.path)}}
            if(rendered!==renderKey()||oldHistory!==history)render();else updateProgress();
        }catch(e){say((submissionProblem?submissionProblem+' ':'')+'暂时无法核对分析记录，可刷新重试。'+error(e))}finally{polling=false}
    }
    async function resume(){
        if(!item?.canResume)return;const current=item;
        if(!await confirm(`<h3>继续这个范围的分析？</h3>${receiverHTML(current.recipient)}<p>只继续原计划中尚未发送的部分，已完成部分不会重发。次数和数据额度已预留，费用仍由服务商计收。</p>`,'继续分析'))return;
        journal.save(current.id);pending=current.id;detail=undefined;epoch++;
        try{item=await api<BatchItem>('ResumeProjectAnalysisBatch',key,current.id,current.specSHA,true);if(live()){render();say('正在继续尚未发送的组。')}}finally{if(live())await recover(current.id)}
    }
    async function stop(){if(!item)return;const id=item.id;if(!await confirm('<h3>结束这次分析？</h3><p>保留已完成的建议，停止后续组。已发送的请求可能产生费用，预留额度不会自动返还。</p>','结束分析'))return;await api('StopProjectAnalysisBatch',key,id);await recover(id)}
    async function dismiss(){if(!pending)return;const id=pending;if(!await confirm('<h3>只关闭本页核对提示？</h3><p>原分析可能仍在后台进行，可在范围分析记录中查看或结束。这里只关闭提示，不会重新发送；再次分析可能另外计费。</p>','关闭提示'))return;journal.clear(id);pending=undefined;item=undefined;detail=undefined;epoch++;render();say('已关闭核对提示。原分析记录会保留，没有重新发送。');await refreshHistory()}
    const timer=setInterval(()=>{if(!busy&&!polling&&(pending||item&&(item.status==='active'||item.status==='paused'&&item.running>0||finished(item)&&!item.noteSaved&&Date.now()<noteUntil)))void recover()},1000);
    void run(async()=>{pending=journal.load();await refreshHistory();if(!live())return;if(pending)await recover();else if(params.scopeSHA&&params.scopeID)await prepare(params.scopeSHA,params.scopeID);else{render();if(history)say('选择范围后，先查看发送预览。')}});
    return()=>{disposed=true;epoch++;clearInterval(timer);if(preview)void api('ReleaseProjectAnalysisBatch',preview.token).catch(()=>undefined)};
}
import {placementLink} from './project-placement';
