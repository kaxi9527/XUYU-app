import {organizerAction} from './ui-actions';
import {agentPresentation,enrollmentReason,type AgentAPI,type AgentCandidate,type AgentState,type AgentReceipt,type AgentReview} from './agent-enrollment-types';
import './agent-enrollment.css';
import {aiBrandMark,aiIconCount,popularAI} from './ai-brands';

type Confirm=(html:string,ok:string,onlyOK?:boolean)=>Promise<boolean>;
const names:Record<string,string>={codex:'Codex',claude:'Claude Code',gemini:'Gemini CLI'};
const date=(s?:string)=>!s||s.startsWith('0001-')?'尚未检查':new Date(s).toLocaleString('zh-CN',{month:'long',day:'numeric',hour:'2-digit',minute:'2-digit'});

/** Tool-level decisions. Receipt revisions, not DOM state, authorize writes. */
export function mountAgentEnrollment(view:HTMLElement,api:AgentAPI,esc:(s:unknown)=>string,confirm:Confirm,initialAgent=''):()=>void {
    const shell=document.createElement('section');shell.className='agent-screen';view.replaceChildren(shell);
    shell.innerHTML=`<header class="bar"><h1>文件标记</h1><a href="#/models">模型与账号</a></header><p class="agent-intro">让工具在写文件时留下名字、项目和时间，来源一眼就能看清。</p><p class="agent-feedback" role="status" aria-live="polite">正在读取标记状态…</p><div class="agent-content"></div><details class="brand-directory"><summary>工具图标与兼容说明</summary><p class="sub">已内置 ${aiIconCount} 个品牌图标。图标用于辨认来源；能否自动写入标记规则，以每个工具的状态为准。</p><div class="brand-list">${popularAI.map(name=>`<span class="agent">${aiBrandMark(name,esc)}${esc(name)}</span>`).join('')}</div><p class="sub">新工具未匹配到图标时显示名称头像，仍可保留文件来源。</p></details>`;
    const content=shell.querySelector<HTMLElement>('.agent-content')!;
    const feedback=shell.querySelector<HTMLElement>('.agent-feedback')!;
    let disposed=false,busy=false,readEpoch=0,linked=false,state:AgentState|undefined;
    const live=()=>!disposed&&shell.isConnected;
    const say=(message:string)=>{if(live())feedback.textContent=message};
    const button=(id:string,label:string,primary=false)=>organizerAction(id,label,primary);
    const q=<T extends HTMLElement>(selector:string)=>content.querySelector<T>(selector)!;
    const lock=(locked:boolean)=>content.querySelectorAll<HTMLButtonElement|HTMLInputElement>('mdui-button,input').forEach(e=>{e.disabled=locked||e.dataset.unavailable==='true'});
    const currentPolicy=()=>state?.receipt.policy;
    const grants=()=>currentPolicy()?.grants?.map(g=>g.agent)||[];
    const focus=(id:string)=>{if(live()&&id)document.getElementById(id)?.focus()};

    async function refresh(force=false) {
        const epoch=++readEpoch;
        const next=await api<AgentState>('AgentEnrollmentStatus');
        if(!live()||epoch!==readEpoch)return;
        const changed=force||!state||JSON.stringify(next)!==JSON.stringify(state);
        state=next;
        if(changed)render();
    }
    async function run(work:()=>Promise<string>,target='') {
        if(busy||!live())return;
        busy=true;readEpoch++;lock(true);say('正在处理…');
        let message='';
        try { message=await work(); }
        catch(e) {message=e instanceof Error?e.message:String(e);}
        finally {
            if(live()) {
                try {await refresh(true);} catch {message+=' 状态暂时无法读取，请重新检查。';}
                busy=false;lock(false);say(message);focus(target);
            }
        }
    }
    function render() {
        if(!state||!live())return;
        const r=state.receipt,enabled=r.policy?.enabled??false;
        const records=r.records||[],tools=records.filter(c=>c.kind==='tool_candidate'),others=records.filter(c=>c.kind!=='tool_candidate');
        const oldOpen=new Set(Array.from(content.querySelectorAll<HTMLDetailsElement>('details[open]')).map(d=>d.id));
        const active=content.contains(document.activeElement)?(document.activeElement as HTMLElement).id:'';
        content.innerHTML=`<div class="agent-setting">
            <div><label for="agent-auto">为新工具启用文件标记</label><p class="sub">每天检查 Codex、Claude Code、Gemini CLI，发现后追加来源规则，先备份原文件。</p>
            <p class="sub">仅在 序屿 运行时检查，关闭后暂停。${enabled?'每 24 小时检查一次，休眠后补查。':'开启前会让你确认接入范围。'}</p></div>
            <input id="agent-auto" class="toggle toggle-primary" type="checkbox" ${enabled?'checked':''} ${!state.supported&&!enabled?'disabled data-unavailable="true"':''}></div>
            ${!state.supported?'<p class="agent-notice">当前版本不能安全自动接入，仍可查看检查结果。</p>':''}
            ${enabled&&!state.backgroundRunning?'<p class="agent-notice">每日检查当前未运行，请重新打开 序屿。仍可立即检查。</p>':''}
            ${state.lastError?`<p class="agent-notice" role="alert">上次自动检查未完成：${esc(state.lastError)}</p>`:''}
            <div class="agent-toolbar"><div><h2>这台电脑上的 AI</h2><p class="sub">${r.revision?'上次检查：'+esc(date(r.scan.startedAt)):'还没有检查过这台电脑'}${enabled&&r.revision?' · 下次：'+esc(date(r.nextDue)):''}</p></div>${button('agent-scan','立即检查')}</div>
            ${tools.length?`<ul class="agent-list" aria-label="工具的文件标记状态">${tools.map(row).join('')}</ul>`:`<div class="agent-empty"><h3>${r.revision?'暂未发现 AI 工具':'先看看有哪些 AI 工具'}</h3><p>点击「立即检查」，按工具查看标记状态，无需逐个确认文件。</p></div>`}
            ${others.length?`<details class="agent-details" id="agent-other"><summary>其他规则线索 · ${others.length} 处</summary><p class="sub">这些规则还不能确定属于哪个工具，不会自动写入。</p>${others.map(c=>`<div class="agent-evidence"><strong>${esc(c.name)}</strong>${evidence(c)}</div>`).join('')}</details>`:''}
            ${r.scan?.gaps?.length?`<details class="agent-details" id="agent-gaps"><summary>有 ${r.scan.gaps.length} 处未能检查</summary><p class="sub">未扫描的位置不会被当作没有安装 AI。</p>${r.scan.gaps.map(g=>`<p class="agent-path">${esc(g.path)}<span class="sub"> · ${esc(g.code)}</span></p>`).join('')}</details>`:''}
            <p class="agent-footnote">规则写入后，已有会话可能需要重新开启。文件是否真的标记来源，会单独核验。</p>`;
        oldOpen.forEach(id=>{const d=document.getElementById(id);if(d instanceof HTMLDetailsElement&&content.contains(d))d.open=true});
        q<HTMLInputElement>('#agent-auto').onchange=()=>{const on=q<HTMLInputElement>('#agent-auto').checked;q<HTMLInputElement>('#agent-auto').checked=enabled;void run(()=>toggle(on),'agent-auto')};
        q<HTMLButtonElement>('#agent-scan').onclick=()=>void run(async()=>{await api('CheckAgentDiscovery',true);return '检查完成。规则写入状态和产物核验分别显示。'},'agent-scan');
        tools.forEach((c,i)=>{
            content.querySelector<HTMLButtonElement>(`#agent-review-${i}`)?.addEventListener('click',()=>void run(()=>review(c),`agent-review-${i}`));
            content.querySelector<HTMLButtonElement>(`#agent-pause-${i}`)?.addEventListener('click',()=>void run(()=>pause(c),`agent-pause-${i}`));
        });
        lock(busy);focus(active);
        if(initialAgent&&!linked){const i=tools.findIndex(c=>c.id===initialAgent);const row=content.querySelector<HTMLElement>(`.agent-row:nth-child(${i+1})`);if(row){linked=true;row.tabIndex=-1;row.focus();row.scrollIntoView({block:'nearest'});}}
    }
    function evidence(c:AgentCandidate) {
        return (c.evidence||[]).map(e=>`<p class="agent-path">${esc(e.path)}</p>`).join('');
    }
    function row(c:AgentCandidate,i:number) {
        const display=agentPresentation(c,state!.receipt);
        const supported=state!.supported&&state!.automaticAgents.includes(c.id);
        const granted=grants().includes(c.id),ignored=display.ignored;
        const canReview=supported&&c.status!=='not_observed';
        return `<li class="agent-row"><div class="agent-identity"><h3>${aiBrandMark(names[c.id]||c.name,esc)}${esc(names[c.id]||c.name)}</h3><p class="agent-state">${esc(display.label)}${ignored?' · 自动接入已暂停':''}</p><p class="sub">${esc(supported?display.detail:'发现安装或规则线索，标记入口还需核实。')}</p>
            <details id="agent-paths-${i}"><summary>查看位置</summary>${evidence(c)}</details></div>
            <div class="agent-actions">${canReview?button(`agent-review-${i}`,'设置文件标记'):''}${supported&&(granted||ignored)?button(`agent-pause-${i}`,ignored?'恢复自动启用':'暂停自动启用'):''}</div></li>`;
    }
    async function toggle(on:boolean) {
        if(on) {
            const ok=await confirm(`<h2>允许新 AI 自动接入？</h2><p>序屿 运行期间每天检查一次。发现 Codex、Claude Code 或 Gemini CLI 后，在已核实的默认规则入口追加来源标记要求。</p><p>会先备份，保留原规则。未知工具、修改过的规则不会被自动覆盖。关闭开关后停止自动接入，已经写入的规则保留。</p><p>要求 AI 为新增和修改的文件标记：工具名字、项目、任务、工种和时间。</p>`,'开启每日检查');
            if(!ok||!live())return '已取消，设置未改变。';
        }
        if(!state!.receipt.revision) {await api('CheckAgentDiscovery',false);await refresh();if(!live())return '';}
        const allowed=on?state!.automaticAgents:grants();
        await api<AgentReceipt>('SetAgentEnrollmentPolicy',state!.receipt.revision,on,allowed,currentPolicy()?.ignored||[]);
        return on?'每日检查已开启。写入规则后，仍需 AI 加载并验证产物来源。':'每日检查已关闭。已写入的规则保留。';
    }
    async function review(c:AgentCandidate) {
        const result=await api<AgentReview>('PreviewAgentEnrollment',c.id);
        if(!live())return '';
        const p=result.preview,actionable=['append','create'].includes(p.status);
        const message=p.status==='already_present'?'这份入口已有完整来源规则。尚未确认当前 AI 会话已加载，也未验证产物。':actionable?'确认后只追加下面这段来源规则，先备份原文件。':enrollmentReason(p.reason);
        const ok=await confirm(`<h2>${esc(names[c.id]||c.name)} 的来源规则</h2><p>${esc(message)}</p><p class="agent-path">${esc(p.path)}</p>${p.instruction?`<details class="agent-rule"><summary>查看规则全文</summary><pre>${esc(p.instruction)}</pre></details>`:''}`,actionable?'确认接入':'知道了',!actionable);
        if(!actionable||!ok||!live())return actionable?'已取消接入。':'规则已查看，未修改文件。';
        await api('ApplyAgentEnrollment',result.revision,c.id,p.digest);
        return `${names[c.id]||c.name} 的来源规则已写入，等待 AI 加载。`;
    }
    async function pause(c:AgentCandidate) {
        const ignored=new Set(currentPolicy()?.ignored||[]),resume=ignored.has(c.id);
        if(resume&&currentPolicy()?.enabled) {
            if(!await confirm(`<h2>恢复 ${esc(names[c.id]||c.name)} 的自动接入？</h2><p>恢复后会核对默认入口，符合接入条件时追加来源规则。</p>`,'恢复自动接入')||!live())return '已取消。';
        }
        if(resume)ignored.delete(c.id);else ignored.add(c.id);
        await api('SetAgentEnrollmentPolicy',state!.receipt.revision,currentPolicy()?.enabled??false,grants(),Array.from(ignored));
        return resume?'已恢复这个工具的自动接入设置。':'已暂停这个工具的自动接入，已写规则保留。';
    }
    const timer=window.setInterval(()=>{if(!busy&&live()&&!document.hidden)void refresh().catch(()=>say('状态暂时无法刷新，可点击「立即检查」重试。'))},30000);
    void refresh(true).then(()=>say('')).catch(e=>{
        if(!live())return;say(`接入状态读取失败：${e instanceof Error?e.message:String(e)}`);
        content.innerHTML=button('agent-retry','重试');q<HTMLButtonElement>('#agent-retry').onclick=()=>void run(async()=>{await refresh(true);return '';},'agent-scan');
    });
    return ()=>{disposed=true;window.clearInterval(timer)};
}
