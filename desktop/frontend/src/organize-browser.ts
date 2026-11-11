type API = <T>(method: string, ...args: unknown[]) => Promise<T>;
type Entry = {name:string;path:string;directory:boolean;hidden:boolean;link:boolean;size:number};
type Listing = {path:string;parent:string;entries:Entry[];hiddenCount:number;truncated:boolean};
export function mountBrowser(host: HTMLElement,api:API,esc:(s:unknown)=>string,confirm:(html:string,ok:string)=>Promise<boolean>,add:(path:string)=>void) {
    host.innerHTML=`<details id="organize-browser"><summary>浏览电脑文件</summary><div class="organize-browser-toolbar"><mdui-button variant="text" class="btn btn-sm btn-ghost" type="button" id="browse-home">主目录</mdui-button><mdui-button variant="text" class="btn btn-sm btn-ghost" type="button" id="browse-up">上一级</mdui-button><label class="organize-hidden-toggle"><input class="toggle toggle-primary toggle-sm" type="checkbox" id="browse-hidden">显示隐藏文件</label></div><div class="organize-inline"><input class="input input-sm" id="browse-path" aria-label="浏览文件夹路径" placeholder="文件夹的绝对路径"><mdui-button variant="text" class="btn btn-sm btn-ghost" type="button" id="browse-go">前往</mdui-button><mdui-button variant="text" class="btn btn-sm btn-ghost" type="button" id="browse-add-current">加入此文件夹</mdui-button></div><p id="browse-status" class="sub" role="status"></p><div id="browse-list"></div><div id="browse-delete-result" role="status"></div></details>`;
    let path='',parent='',seq=0,loaded=false;
    const status=host.querySelector<HTMLElement>('#browse-status')!;
    const hidden=host.querySelector<HTMLInputElement>('#browse-hidden')!;
    async function load(next:string) {
        const request=++seq;status.textContent='正在读取文件夹……';host.querySelector('#browse-list')!.innerHTML='';
        try {
            const data=await api<Listing>('OrganizeBrowse',next,hidden.checked);if(request!==seq||!host.isConnected)return;
            path=data.path;parent=data.parent;loaded=true;host.querySelector<HTMLInputElement>('#browse-path')!.value=path;
            host.querySelector<HTMLButtonElement>('#browse-up')!.disabled=!parent;
            status.textContent=`${data.entries.length} 项${hidden.checked?' · 已显示隐藏文件':` · ${data.hiddenCount} 项隐藏`}${data.truncated?' · 仅显示前 1000 项，请缩小范围':''}`;
            host.querySelector('#browse-list')!.innerHTML=`<div class="organize-table-wrap"><table class="organize-file-table table"><thead><tr><th>名称</th><th>类型</th><th>操作</th></tr></thead><tbody>${data.entries.map((f,i)=>`<tr><td>${f.directory&&!f.link?`<mdui-button variant="text" class="organize-name-link btn btn-sm" type="button" data-open="${i}">${esc(f.name)}/</mdui-button>`:`<span>${esc(f.name)}</span>`}${f.hidden?'<span class="organize-hidden-label">隐藏</span>':''}</td><td>${f.link?'链接':f.directory?'文件夹':'文件'}</td><td><div class="organize-file-actions"><mdui-button variant="text" class="btn btn-sm btn-ghost" type="button" data-add="${i}" ${f.link?'disabled':''}>加入来源</mdui-button><mdui-button variant="text" class="btn btn-sm btn-ghost" type="button" data-delete="${i}" ${f.link?'disabled':''}>删除</mdui-button></div></td></tr>`).join('')||'<tr><td colspan="3">此文件夹没有可显示的文件。</td></tr>'}</tbody></table></div>`;
            host.querySelectorAll<HTMLButtonElement>('[data-open]').forEach(b=>b.onclick=()=>{void load(data.entries[Number(b.dataset.open)].path)});
            host.querySelectorAll<HTMLButtonElement>('[data-add]').forEach(b=>b.onclick=()=>{add(data.entries[Number(b.dataset.add)].path);status.textContent='已加入整理来源，尚未移动文件。'});
            host.querySelectorAll<HTMLButtonElement>('[data-delete]').forEach(b=>b.onclick=async()=>{
                const file=data.entries[Number(b.dataset.delete)];b.disabled=true;
                try {
                    const preview=await api<{token:string;hiddenCount:number;preview?:{skip?:boolean;reason?:string;label:string}[];methodHint:string}>('PreviewDelete',[file.path]);
                    if(!preview.preview?.some(p=>!p.skip)){status.textContent=preview.preview?.map(p=>p.reason).join('；')||'此路径不能删除。';return}
                    if(!preview.token)throw new Error('删除预览已失效，请重新预览。');
                    const isHidden=preview.hiddenCount>0;
                    const warning=isHidden?`<p class="organize-delete-warning"><b>包含 ${preview.hiddenCount} 项隐藏内容，包括所选文件夹内部。</b>它可能包含工具配置、会话记录或运行环境。删除后，相关工具可能无法正常工作，请确认不再需要。</p>`:'';
                    if(!await confirm(`<h2>${isHidden?'确认删除隐藏文件':'确认删除'}</h2>${warning}<p class="organize-path">${esc(file.path)}</p>${preview.preview.map(p=>`<p>${esc(p.label)}</p>`).join('')}<p>将移入废纸篓或删除暂存，可以撤销。</p>`,isHidden?'我已了解，删除隐藏文件':'移入废纸篓'))return;
                    const done=await api<{trashed:number;staged:number;failed:number;undoPath?:string}>('ApplyDeletePlan',preview.token,isHidden);
                    const result=host.querySelector<HTMLElement>('#browse-delete-result')!;
                    result.innerHTML=`<p>已移出 ${done.trashed+done.staged} 项 · 失败 ${done.failed} 项 ${done.undoPath?'<mdui-button variant="text" class="btn btn-sm btn-ghost" type="button" id="browse-undo">撤销删除</mdui-button>':''}</p>`;
                    const undo=result.querySelector<HTMLButtonElement>('#browse-undo');if(undo)undo.onclick=async()=>{undo.disabled=true;try{const r=await api<{restored:number;failed:number}>('UndoDelete',done.undoPath);result.textContent=`已恢复 ${r.restored} 项 · 失败 ${r.failed} 项`;await load(path)}catch(e){result.textContent=String(e)}};
                    await load(path);
                }catch(e){status.textContent=e instanceof Error?e.message:String(e)}finally{b.disabled=false}
            });
        }catch(e){if(request===seq)status.textContent=e instanceof Error?e.message:String(e)}
    }
    host.querySelector<HTMLDetailsElement>('#organize-browser')!.ontoggle=e=>{if((e.target as HTMLDetailsElement).open&&!loaded)void load('')};
    host.querySelector<HTMLButtonElement>('#browse-home')!.onclick=()=>{void load('')};
    host.querySelector<HTMLButtonElement>('#browse-up')!.onclick=()=>{if(parent)void load(parent)};
    host.querySelector<HTMLButtonElement>('#browse-go')!.onclick=()=>{void load(host.querySelector<HTMLInputElement>('#browse-path')!.value.trim())};
    host.querySelector<HTMLInputElement>('#browse-path')!.onkeydown=e=>{if(e.key==='Enter'){e.preventDefault();void load((e.target as HTMLInputElement).value.trim())}};
    host.querySelector<HTMLButtonElement>('#browse-add-current')!.onclick=()=>{if(path){add(path);status.textContent='已加入来源。请选择具体项目文件夹，主目录不能整体整理。'}};
    hidden.onchange=()=>{void load(path)};
}
