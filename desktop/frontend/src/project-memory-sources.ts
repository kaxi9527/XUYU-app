import type {MemoryEvent,MemoryStatus} from './project-memory-types';

type Escape=(value:unknown)=>string;
type Action=(id:string,label:string,primary?:boolean)=>string;
export const sourceBindingLabel=(binding:string)=>({snapshot_match:'与这次文件快照内容匹配',snapshot_differs:'声明与这次文件快照内容不同',path_only:'仅有路径关联',not_observed:'尚未观察到对应文件'}[binding]||'来源待核对');
const sourceProgress=(s:NonNullable<MemoryStatus['sources']>[number])=>s.directory?`会话目录 · 已读取 ${s.readRecords||0} / ${s.records||0} 份记录${s.unreadable?' · '+s.unreadable+' 份暂不可读':''}`:`已读至第 ${s.line} 行`;

export function memorySourcesHTML(state:MemoryStatus,esc:Escape,button:Action):string {
    const sources=state.sources||[];
    return `<details class="memory-sources"><summary>AI 文件来源${sources.length?' · 已关联 '+sources.length+' 份记录':''}</summary><p class="sub">关联本项目使用的 AI 记录，查看文件由哪个工具、会话或任务产出。只保存文件操作依据，不复制聊天正文，也不执行记录中的指令。</p>
    ${sources.length?`<ul class="memory-source-list">${sources.map(s=>`<li><div class="memory-section-head"><strong>${esc(s.adapter==='workshelf'?'序屿 标记':s.adapter==='codex'?'Codex':s.adapter==='gemini'?'Gemini':'Claude')}</strong>${button('memory-source-'+s.id,s.enabled&&!s.scopeChanged?'停止读取':'重新授权')}</div><p class="memory-path">${esc(s.path)}</p><p class="sub">${s.scopeChanged?'项目范围变化，需要重新授权':!s.enabled?'已停止，历史保留':!state.enabled?'项目已暂停，停止读取':s.lastReadAt?sourceProgress(s):'已授权，等待检查'}${s.more?' · 正在分批读取':''} · ${s.origins} 条文件来源</p>${s.problem?`<p role="status">${esc(s.problem)}</p>`:''}${s.skipped?`<p class="sub">${s.skipped} 条记录未映射为文件操作；格式或操作可能尚未支持。</p>`:''}</li>`).join('')}</ul>`:''}
    ${state.enabled&&!state.scopeChanged?`<div class="memory-source-form"><label>来源格式<select id="memory-source-adapter"><option value="codex">Codex 会话 JSONL</option><option value="claude">Claude 会话 / PostToolUse JSONL</option><option value="gemini">Gemini CLI 会话 JSONL</option><option value="workshelf">序屿 来源标记 JSONL</option></select></label><label>读取范围<select id="memory-source-kind"><option value="file">一份记录</option><option value="directory">会话目录（含新增记录）</option></select></label><label>来源的完整路径<input id="memory-source-path" type="text" maxlength="4096" placeholder="记录文件或会话目录的完整路径" autocomplete="off" spellcheck="false"></label>${button('memory-source-preset','使用常见会话目录')}${button('memory-source-add','关联并读取')}<p class="sub memory-source-hint" role="status"></p></div>`:''}<p class="sub">开启项目自动检查后会继续发现会话和读取追加记录；随时可停止。未支持的工具暂可使用 序屿 文件标记。内容匹配只证明记录与快照相符，不能证明作者身份。</p></details>`;
}

export function bindMemorySources(host:HTMLElement,state:MemoryStatus,action:(adapter:string,path:string,enabled:boolean,directory:boolean)=>void,preset:(adapter:string)=>Promise<string>):void {
    host.querySelector<HTMLElement>('#memory-source-add')?.addEventListener('click',()=>{
        const adapter=host.querySelector<HTMLSelectElement>('#memory-source-adapter')!.value,path=host.querySelector<HTMLInputElement>('#memory-source-path')!.value.trim();
        action(adapter,path,true,host.querySelector<HTMLSelectElement>('#memory-source-kind')!.value==='directory');
    });
    host.querySelector<HTMLElement>('#memory-source-preset')?.addEventListener('click',async()=>{
        const adapter=host.querySelector<HTMLSelectElement>('#memory-source-adapter')!.value,hint=host.querySelector<HTMLElement>('.memory-source-hint')!;
        try{const path=await preset(adapter);if(!host.isConnected||host.querySelector<HTMLSelectElement>('#memory-source-adapter')?.value!==adapter)return;
            if(!path){hint.textContent='序屿 标记请填写实际记录文件或所在目录。';return}
            host.querySelector<HTMLInputElement>('#memory-source-path')!.value=path;host.querySelector<HTMLSelectElement>('#memory-source-kind')!.value='directory';hint.textContent='已填入常见位置。确认路径后再授权读取；自定义位置可以直接修改。';
        }catch{if(host.isConnected)hint.textContent='暂时无法获取常见位置，请填写实际目录。'}
    });
    for(const s of state.sources||[])host.querySelector<HTMLElement>('#memory-source-'+s.id)?.addEventListener('click',()=>action(s.adapter,s.path,!s.enabled||s.scopeChanged,!!s.directory));
}

export function originDetails(e:MemoryEvent,esc:Escape):string {
    const models=(e.auto?.modelDecisions||[]).map(d=>`<p>模型判断：${esc(d.provider)} / ${esc(d.model)}${d.reason?' · '+esc(d.reason):''}</p><p class="memory-path">判断输入摘要：${esc(d.inputSHA)}</p>`).join('');
    const objection=e.auto?.objection,objectionDetail=objection?`<p>反对来自 ${esc(objection.adapter)} 会话 ${esc(objection.session)}${objection.provider?' · 模型识别：'+esc(objection.provider)+' / '+esc(objection.model):''}</p><p class="memory-path">来源：${esc(objection.sourcePath)} · 第 ${esc(objection.line)} 行 · ${esc(objection.recordSHA)}</p>`:'';
	const o=e.origin;if(!o)return models+objectionDetail;
	return models+objectionDetail+`<p>${esc(sourceBindingLabel(o.binding))}；${o.claim.basis==='tool_result'?'工具记录报告完成':'Agent 自报'}，作者身份未独立验证。</p>${o.claim.session?`<p>会话：${esc(o.claim.session)}</p>`:''}${o.claim.task?`<p>任务：${esc(o.claim.task)}</p>`:''}<p class="memory-path">来源：${esc(o.sourcePath)} · 第 ${o.claim.line} 行</p><p class="memory-path">记录摘要：${esc(o.claim.recordSHA)}</p>`;
}
