import {organizerAction as button,restoreActionFocus} from './ui-actions';
import {batchLink} from './project-analysis-batch';
import type {MemoryAPI} from './project-memory-types';
import {debitDescription,recipientRows,AnalysisJournal,analysisStatus,analysisLabels,type AnalysisPreview,type AnalysisRecord,type AnalysisHistory,type Recipient,type Packet,type Budget,type AnalysisScopePage} from './project-analysis-types';
import './project-analysis.css';

export function mountProjectAnalysis(view:HTMLElement,key:string,api:MemoryAPI,esc:(x:unknown)=>string,confirm:(html:string,ok:string)=>Promise<boolean>):()=>void {
    const root=document.createElement('section');root.className='analysis-screen';view.replaceChildren(root);
    const memoryLink='#/memory/'+encodeURIComponent(key);
    root.innerHTML=`<header class="bar"><a class="back" href="${memoryLink}">← 文件记录</a><h1>整理建议</h1></header><p class="analysis-feedback" role="status" aria-live="polite">正在读取分析记录…</p><div class="analysis-body"></div>`;
    const body=root.querySelector<HTMLElement>('.analysis-body')!,feedback=root.querySelector<HTMLElement>('.analysis-feedback')!;
    const journal=new AnalysisJournal(key);
    let disposed=false,busy=false,polling=false,preview:AnalysisPreview|undefined,record:AnalysisRecord|undefined,history:AnalysisHistory|undefined,pending:string|undefined,accepted=false,offset=0,renderedRecord='',notePollUntil=0;
    let scope:AnalysisScopePage|undefined,choosingScope=false,scopeEpoch=0,submissionProblem='';
    const live=()=>!disposed&&root.isConnected;
    const say=(s:string)=>{if(live())feedback.textContent=s};
    const error=(e:unknown)=>e instanceof Error?e.message:String(e);
    const date=(s:string)=>new Date(s).toLocaleString('zh-CN',{month:'long',day:'numeric',hour:'2-digit',minute:'2-digit'});
    const status=()=>record?analysisStatus(record):pending?'unknown':'';
    const receiver=(r:Recipient)=>`<dl class="analysis-recipient">${recipientRows(r).map(([label,value])=>`<div><dt>${esc(label)}</dt><dd>${esc(value)}</dd></div>`).join("")}</dl>`;
    const quota=(b:Budget)=>`最近 24 小时已预留 ${b.reserved.requests} / ${b.limit.requests} 次 · 输入 ${Math.ceil(b.reserved.inputBytes/1024)} / ${b.limit.inputBytes/1024} KB · 输出 ${b.reserved.outputTokens} / ${b.limit.outputTokens} token`;
    const payload=(system:string,input:string)=>`<details class="analysis-payload"><summary>查看完整发送内容</summary><h3>分析要求</h3><pre>${esc(system)}</pre><h3>项目资料</h3><pre>${esc(input)}</pre></details>`;
    function render() {
        if(!live())return;
        const s=status();
        renderedRecord=[record?.id,s,record?.noteSaved].join('|');
        const projectName=preview?.packet.body.projectName||(record?(JSON.parse(record.spec.request.input) as Packet).body.projectName:scope?.projectName||'');
        body.innerHTML=`<div class="analysis-intro"><div><h2>${esc(projectName||'按项目目录，看看怎么整理')}</h2><p>根据文件记录和工种模板生成分组建议。文件仍留在原处。</p></div><a href="#/models">模型与账号</a></div>
        ${preview?previewHTML(preview):choosingScope&&scope?scopeHTML(scope):pending||record?resultHTML(s):`<div class="analysis-start"><p>先查看将发送给模型的资料，再决定是否分析。</p><div class="analysis-actions">${button('analysis-preview','查看发送预览',true)}${button('analysis-choose','选择目录')}<a href="${batchLink(key)}">范围分析记录</a></div><p class="analysis-meta">需要先开启<a href="${memoryLink}">文件记录</a>、检查文件，并确认项目的工种模板。</p></div>`}
        ${history?`<section class="analysis-history" aria-label="分析记录"><div class="analysis-section-title"><h2>分析记录</h2>${button('analysis-refresh','刷新记录')}</div><p class="analysis-meta">共 ${history.total} 次分析${history.archived?` · ${history.archived} 次已归档，原结果仍可查看`:""}</p>${history.budgetProblem?`<p role="status">${esc(history.budgetProblem)}</p>`:""}<p class="analysis-meta">${esc(quota(history.budget))}。中断的请求仍占用预留额度，实际费用请在服务商账单核对。</p><ul>${(history.items||[]).map((r,i)=>`<li><div><strong>${esc(analysisLabels[r.status]||r.status)}</strong><span>${esc(r.recipient.modelName||r.recipient.modelID)} · ${esc(date(r.startedAt))}${r.archivedAt?" · 已归档":""}</span></div>${button('analysis-history-'+i,'查看')}</li>`).join('')||'<li>还没有分析记录。</li>'}</ul><div class="analysis-actions">${offset?button('analysis-prev','上一页'):''}${offset+20<history.total?button('analysis-next','下一页'):''}</div></section>`:''}`;
        if(!history)body.insertAdjacentHTML('beforeend',`<section class="analysis-history" aria-label="分析记录"><div class="analysis-section-title"><h2>分析记录</h2>${button('analysis-refresh','刷新记录')}</div><p>记录尚未读取。可刷新重试，原有结果会保留。</p></section>`);
        const bind=(id:string,fn:()=>Promise<void>)=>{const el=body.querySelector<HTMLButtonElement>('#'+id);if(el)el.onclick=()=>void run(fn,id)};
        bind('analysis-preview',makePreview);bind('analysis-send',send);bind('analysis-close-preview',releasePreview);
        bind('analysis-choose',async()=>{await releasePreview();await loadScope()});
        bind('analysis-scope-reload',async()=>{await loadScope()});
        bind('analysis-scope-cancel',async()=>{choosingScope=false;render();say('已退出范围选择，没有发送资料。')});
        bind('analysis-scope-current',async()=>{if(scope)await previewGroup(scope.id,scope.sha)});
        bind('analysis-batch',async()=>{if(scope)location.hash=batchLink(key,scope.sha,scope.id)});
        bind('analysis-scope-prev',async()=>{if(scope)await loadScope(scope.id,scope.sha,Math.max(0,scope.offset-50))});
        bind('analysis-scope-next',async()=>{if(scope)await loadScope(scope.id,scope.sha,scope.offset+50)});
        scope?.breadcrumbs.forEach((c,i)=>bind('analysis-crumb-'+i,async()=>{if(scope)await loadScope(c.id,scope.sha,0)}));
        scope?.rows.forEach((r,i)=>{
            bind('analysis-scope-open-'+i,async()=>{if(scope)await loadScope(r.id,scope.sha,0)});
            bind('analysis-scope-preview-'+i,async()=>{if(scope)await previewGroup(r.id,scope.sha)});
        });
        bind('analysis-refresh',async()=>{await refresh(0)});bind('analysis-check',recover);
        bind('analysis-stop',async()=>{if(pending)await api('CancelProjectAnalysis',key,pending);say('已请求停止，正在核对记录。');await recover()});
        bind('analysis-dismiss',async()=>{if(!pending)return;const id=pending;if(!await confirm('<h3>结束这次核对？</h3><p>上次请求可能已发送或产生费用。这里只关闭本页的核对提示；项目中的分析记录会保留。再次分析需要重新预览、确认，也可能另外计费。</p>','结束核对'))return;await api('CancelProjectAnalysis',key,id);if(!live()||pending!==id)return;journal.clear(id);pending=undefined;record=undefined;render();say('已结束核对，没有重发。可以稍后在分析记录中查询。');await refresh()});
        bind('analysis-again',async()=>{record=undefined;render();await makePreview()});
        bind('analysis-note',async()=>{if(!record)return;await api('RecordProjectAnalysisNote',key,record.id);record=await api<AnalysisRecord>('ProjectAnalysisResult',key,record.id);say('建议已补记到项目记录。');render()});
        bind('analysis-prev',async()=>{await refresh(Math.max(0,offset-20))});bind('analysis-next',async()=>{await refresh(offset+20)});
        (history?.items||[]).forEach((r,i)=>bind('analysis-history-'+i,async()=>{if(pending)throw new Error('请先核对正在进行的分析。');await releasePreview();choosingScope=false;record=await api<AnalysisRecord>('ProjectAnalysisResult',key,r.id);if(!live())return;render();body.querySelector<HTMLElement>('.analysis-result')?.focus()}));
        const check=body.querySelector<HTMLInputElement>('#analysis-consent');if(check)check.onchange=()=>{accepted=check.checked;setDisabled()};
        setDisabled();
    }
    function previewHTML(p:AnalysisPreview):string {
        const b=p.packet.body,files=b.files.filter(f=>f.kind==='file'&&f.status==='present'),excluded=Object.values(b.excluded||{}).reduce((a,n)=>a+n,0);
        return `<section class="analysis-preview" aria-labelledby="analysis-preview-title"><h2 id="analysis-preview-title" tabindex="-1">发送前，确认这一次</h2>${receiver(p.recipient)}<div class="analysis-includes"><strong>本次 ${files.length} 份文件记录${scope?` / 项目可分析 ${scope.totalFiles} 份`:''} · ${(b.notes||[]).length} 条项目笔记</strong><p>包含相对路径、文件信息、目录规则及项目笔记；不读取文件正文。未纳入 ${excluded} 条记录。</p><p>记录时间：${esc(date(b.observedAt))}${b.observationComplete?'':' · 本次观察不完整，建议只基于已记录的部分'}</p></div><details class="analysis-file-preview"><summary>查看本次文件</summary><ul class="analysis-file-list" tabindex="0" aria-label="本次文件记录">${files.map(f=>`<li>${esc(f.position.relative)}</li>`).join('')}</ul></details>${payload(p.system,p.input)}<p class="analysis-meta">${esc(quota(p.budget))}。本次输出上限 ${p.maxOutputTokens} token；预览 ${esc(date(p.expiresAt))} 过期。</p><label class="analysis-consent"><input id="analysis-consent" type="checkbox" ${accepted?'checked':''}><span>同意将上述内容发送给这个模型。费用由服务商计收，金额可能未知；当前只能限制次数和数据量。</span></label><div class="analysis-actions">${button('analysis-send','同意发送并分析',true)}${button('analysis-choose','更换范围')}${button('analysis-close-preview','取消')}</div></section>`;
    }
    function scopeHTML(p:AnalysisScopePage):string {
        const omitted=Object.values(p.excluded||{}).reduce((a,n)=>a+n,0);
        return `<section class="analysis-scopes" aria-labelledby="analysis-scope-title"><div class="analysis-section-title"><h2 id="analysis-scope-title" tabindex="-1">先选一个目录</h2>${button('analysis-scope-reload','刷新范围')}</div><p class="analysis-meta">项目可分析 ${p.totalFiles} 份文件；隐藏、敏感、依赖等 ${omitted} 条记录未纳入。这里只读取已有记录，不会调用模型。</p><nav class="analysis-crumbs" aria-label="分析范围路径">${p.breadcrumbs.map((c,i)=>i===p.breadcrumbs.length-1?`<span aria-current="location">${esc(c.name)}</span>`:i===0||i>=p.breadcrumbs.length-3?button('analysis-crumb-'+i,c.name):i===1?'<span>…</span>':'').filter(Boolean).join('<span aria-hidden="true">/</span>')}</nav><div class="analysis-scope-summary"><strong>当前范围 ${p.files} 份</strong>${p.files?button('analysis-batch','分析这个范围',true):''}</div><ul class="analysis-scope-list">${p.rows.map((r,i)=>`<li><div><strong>${esc(r.name)}</strong><p>${r.files} 份文件${r.kind==='group'?' · 自动分组':''}</p></div><div class="analysis-actions">${r.kind==='directory'?button('analysis-scope-open-'+i,'打开目录'):''}${r.canPreview?button('analysis-scope-preview-'+i,'查看这组'):''}</div></li>`).join('')||'<li><p>这个范围没有可分析的普通文件。可返回项目记录检查覆盖情况。</p></li>'}</ul><div class="analysis-actions">${p.offset?button('analysis-scope-prev','上一页'):''}${p.offset+p.rows.length<p.total?button('analysis-scope-next','下一页'):''}${button('analysis-scope-cancel','返回')}</div><p class="analysis-meta">可以一次确认整个范围，序屿 会自动分组分析。也可打开子目录，或单独预览一组。</p></section>`;
    }
    function resultHTML(s:string):string {
        let content='';
        if(record?.advice&&s==='succeeded'){
            const packet=JSON.parse(record.spec.request.input) as Packet,files=new Map(packet.body.files.map(f=>[f.id,f])),folders=new Map(packet.body.folders.map(f=>[f.id,f.path]));
            const fileList=(ids:string[])=>`<ul class="analysis-file-list" tabindex="0" aria-label="本次文件记录">${ids.map(id=>`<li>${esc(files.get(id)?.position.relative||id)}</li>`).join('')}</ul>`;
            content=`<p>本次分析 ${packet.body.files.filter(f=>f.kind==='file'&&f.status==='present').length} 份文件，仅覆盖本次发送的记录。</p><p>${record.noteSaved?'建议已记入这个项目的文件记录。':'建议已保存，项目记录尚未写入。'} 这是 ${esc(date(record.startedAt))} 的分析结果。</p>${!record.noteSaved?button('analysis-note','补记到项目记录'):''}<div class="analysis-groups">${(record.advice.groups||[]).map(g=>`<details><summary><span>${esc(folders.get(g.folderID)||g.folderID)}</span><span>${g.fileIDs.length} 项</span></summary><p>${esc(g.reason)}</p>${fileList(g.fileIDs)}</details>`).join('')}${record.advice.uncertainIDs?.length?`<details><summary><span>暂留原处</span><span>${record.advice.uncertainIDs.length} 项</span></summary><p>依据还不足，可以稍后补充项目规则。</p>${fileList(record.advice.uncertainIDs)}</details>`:''}</div>`;
        } else {
            const explanations:Record<string,string>={running:'正在等待模型返回。离开页面会请求停止；已发送的内容无法撤回。',failed:'这次未生成可用建议。可以查看记录，再决定是否重新预览。',cancelled:'本次分析已停止，没有移动文件。',stale:'记录或模板已经变化，这次结果未采用。请先查看最新文件记录。',unknown:'上次请求的结果尚未确定。这里只核对记录，不会重新发送。'};
            content=`<p>${esc(explanations[s]||'正在核对记录…')}</p>`;
        }
        return `<section class="analysis-result" tabindex="-1"><h2>${esc(analysisLabels[s]||'分析记录')}</h2>${record&&s==='succeeded'?`<p><a class="placement-entry" href="${esc(placementLink(key,'single',record.id))}">按建议整理文件 →</a></p>`:''}${content}${record?`<details class="analysis-source"><summary>本次模型与发送记录</summary>${receiver(record.spec.recipient)}<p class="analysis-meta analysis-debit">${esc(debitDescription(record.usage,s))}</p>${payload(record.spec.request.system,record.spec.request.input)}<p class="analysis-meta">请求编号：${esc(record.id)}</p></details>`:''}<div class="analysis-actions">${pending?button('analysis-check','核对请求')+button('analysis-stop','停止分析')+(s==='unknown'?button('analysis-dismiss','结束核对'):''):button('analysis-again','重新预览')}<a href="${memoryLink}">查看文件记录</a></div></section>`;
    }
    function setDisabled(){if(!live())return;body.querySelectorAll<HTMLButtonElement>('mdui-button').forEach(el=>{el.disabled=busy||(el.id==='analysis-send'&&!accepted)||(!!pending&&(el.id.startsWith('analysis-history-')||el.id==='analysis-preview'))})}
    async function run(fn:()=>Promise<void>,focus='') {
        if(!live()||busy)return;busy=true;setDisabled();
        try{await fn()}catch(e){say(error(e))}finally{if(live()){busy=false;setDisabled();if(focus)await restoreActionFocus(body.querySelector<HTMLElement>('#'+focus),()=>live()&&!root.contains(document.activeElement))}}
    }
    async function refresh(nextOffset=offset){const h=await api<AnalysisHistory>('ProjectAnalysisHistoryPage',key,nextOffset,nextOffset?history?.revision||0:0);if(!live())return;history=h;offset=nextOffset;render()}
    async function releasePreview(){const token=preview?.token;preview=undefined;accepted=false;render();if(token)await api('ReleaseProjectAnalysis',token)}
    async function makePreview(){
        if(pending)throw new Error('请先核对上次分析。');await loadScope();if(!live()||!scope)return;
        if(scope.canPreview)await previewGroup(scope.id,scope.sha);else say(scope.totalFiles?'已按目录和文件组列出。选择一组后再查看发送预览。':'暂无可分析文件，可返回文件记录检查覆盖情况。');
    }
    async function loadScope(id='',sha='',start=0){
        if(pending)throw new Error('请先核对上次分析。');const epoch=++scopeEpoch;say('正在读取目录范围…');
        const next=await api<AnalysisScopePage>('ProjectAnalysisScopes',key,id,sha,start);if(!live()||epoch!==scopeEpoch)return;
        scope=next;choosingScope=true;record=undefined;render();say('目录范围已读取，还没有发送。');body.querySelector<HTMLElement>('#analysis-scope-title')?.scrollIntoView({block:'nearest'});body.querySelector<HTMLElement>('#analysis-scope-title')?.focus({preventScroll:true});
    }
    async function previewGroup(id:string,sha:string){
        const epoch=++scopeEpoch;say('正在准备这一组的发送预览…');
        const p=await api<AnalysisPreview>('PreviewProjectAnalysisGroup',key,sha,id);
        if(!live()||epoch!==scopeEpoch){await api('ReleaseProjectAnalysis',p.token);return}
        preview=p;choosingScope=false;record=undefined;accepted=false;render();say('预览已准备好，还没有发送。');body.querySelector<HTMLElement>('#analysis-preview-title')?.scrollIntoView({block:'nearest'});body.querySelector<HTMLElement>('#analysis-preview-title')?.focus({preventScroll:true});
    }
    async function recover(id=pending){
        if(!id)return;
        let r:AnalysisRecord;
        try{r=await api<AnalysisRecord>('ProjectAnalysisResult',key,id)}catch(e){throw new Error((submissionProblem?submissionProblem+' ':'')+'暂时无法核对分析记录，不会自动重发。'+error(e))}
        if(!live()||(pending?pending!==id:record?.id!==id))return;
        submissionProblem='';
        if(r.status==='succeeded'&&!r.noteSaved&&(record?.id!==id||record.status!=='succeeded'))notePollUntil=Date.now()+6000;
        record=r;
        const s=analysisStatus(r);if(pending===id&&s!=='running'&&s!=='unknown'){journal.clear(id);pending=undefined}
        if(renderedRecord!==[r.id,s,r.noteSaved].join('|')){render();say(s==='running'?'正在分析…':analysisLabels[s]);await refresh()}
    }
    async function send(){
        if(!preview||!accepted||pending)return;
        const p=preview,id=crypto.randomUUID();journal.save(id);pending=id;submissionProblem='';
        preview=undefined;accepted=false;record=undefined;render();say('已确认，正在提交这一次分析…');
        try{await api('ConfirmProjectAnalysis',key,p.token,id,true,false)}catch(e){submissionProblem=error(e);say('提交结果尚未核实：'+submissionProblem)}
        finally{await api('ReleaseProjectAnalysis',p.token).catch(()=>undefined)}
        if(!live()){await api('CancelProjectAnalysis',key,id).catch(()=>undefined);return}
        // A failed/ambiguous bridge reply is followed only by a receipt lookup.
        await recover();
        if(live())body.querySelector<HTMLElement>('.analysis-result')?.focus();
    }
    const timer=setInterval(()=>{
        const id=pending||(record?.status==='succeeded'&&!record.noteSaved&&Date.now()<notePollUntil?record.id:undefined);
        if(!live()||busy||polling||!id||document.hidden)return;
        polling=true;void recover(id).catch(e=>say(error(e))).finally(()=>{polling=false});
    },1500);
    render();
    void run(async()=>{pending=journal.load();await refresh();if(pending)await recover();else say('预览和查看记录都不会调用模型。')});
    return ()=>{disposed=true;scopeEpoch++;clearInterval(timer);const token=preview?.token;if(token)void api('ReleaseProjectAnalysis',token).catch(()=>undefined);if(pending)void api('CancelProjectAnalysis',key,pending).catch(()=>undefined)};
}
import {placementLink} from './project-placement';
