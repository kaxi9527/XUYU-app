import type {PreviewPlan} from './organize-structure';
import './organize-result.css';

export type OrganizeResult = {moved:number;copied:number;failed:number;stopped?:number;duplicates?:number;undoPath?:string;warnings?:string[];items?:{source:string;target:string;action:string;error?:string}[]};

// The receipt owns success and destination. The preview supplies only file/folder presentation.
export function mountResultFiles(host:HTMLElement,plan:PreviewPlan,done:OrganizeResult,esc:(s:unknown)=>string,onOpen:(path:string)=>void) {
    const prefix=plan.target.replace(/\/+$/,'')+'/';
    const directories=new Set(plan.rows.filter(row=>row.directory).map(row=>row.source));
    const success=(done.items||[]).filter(row=>!row.error&&['搬走','复制'].includes(row.action));
    const versionSources=new Set(plan.rows.filter(row=>row.versionGroup).map(row=>row.source));
    const versionCount=success.filter(row=>versionSources.has(row.source)).length;
    let versionsOnly=false;
    const rows=success.filter(row=>row.target.startsWith(prefix)&&!row.target.slice(prefix.length).split('/').some(part=>!part||part==='.'||part==='..'));
    const failures=(done.items||[]).filter(row=>row.error||row.action==='失败'||row.action==='未开始');
    const duplicates=(done.items||[]).filter(row=>!row.error&&row.action==='重复保留');
    type Folder={id:number;path:string;name:string;items:typeof rows;children:Folder[];count:number};
    const root:Folder={id:0,path:'',name:plan.project,items:[],children:[],count:0};
    const folders=new Map<string,Folder>([['',root]]);
    for(const row of rows){
        const parts=row.target.slice(prefix.length).split('/');parts.pop();
        let parent=root;
        for(const part of parts){
            const path=parent.path?parent.path+'/'+part:part;
            let child=folders.get(path);
            if(!child){child={id:folders.size,path,name:part,items:[],children:[],count:0};folders.set(path,child);parent.children.push(child)}
            parent=child;
        }
        parent.items.push(row);
    }
    const rank=(path:string)=>{const index=plan.folders.findIndex(folder=>folder===path||folder.startsWith(path+'/'));return index<0?Infinity:index};
    for(const folder of folders.values())folder.children.sort((a,b)=>rank(a.path)-rank(b.path)||a.name.localeCompare(b.name,'zh-CN',{numeric:true}));
    let query='',failureLimit=20,duplicateLimit=50,duplicatesOpen=false;
    const expanded=new Set<string>(),collapsed=new Set<string>(),limits=new Map<number,number>(),folderLimits=new Map<number,number>();
    const basename=(path:string)=>path.split('/').pop()||path;
    if(root.children.length===1&&rows.length<=12)for(const folder of folders.values())expanded.add(folder.path);
    function render() {
        const search=query.trim().toLocaleLowerCase();
        const filtering=!!search||versionsOnly,shown=new Map<number,Folder>();
        const filter=(folder:Folder):Folder=>{
            const items=folder.items.filter(row=>(!versionsOnly||versionSources.has(row.source))&&row.target.slice(prefix.length).toLocaleLowerCase().includes(search));
            const children=folder.children.map(filter).filter(child=>child.count);
            const node={...folder,items,children,count:items.length+children.reduce((count,child)=>count+child.count,0)};
            shown.set(node.id,node);return node;
        };
        const tree=filter(root),scrollTop=host.querySelector('.result-directory-scroll')?.scrollTop||0;
        const contents=(folder:Folder):string=>{
            const limit=limits.get(folder.id)||50,folderLimit=folderLimits.get(folder.id)||50;
            return `<div class="result-contents" data-result-contents="${folder.id}">
              ${folder.children.slice(0,folderLimit).map(branch).join('')}
              ${folder.children.length>folderLimit?`<mdui-button variant="text" class="btn btn-sm btn-ghost result-more" type="button" data-result-more="${folder.id}" data-result-kind="folders">再显示 ${Math.min(50,folder.children.length-folderLimit)} 个文件夹</mdui-button>`:''}
              ${folder.items.length?`<ul class="result-file-list">${folder.items.slice(0,limit).map(row=>`<li><mdui-button variant="text" class="result-file" type="button" data-result-open="${esc(row.target)}" title="${esc(row.target)}"><span class="${directories.has(row.source)?'folder-shape':'result-file-shape'}" aria-hidden="true"></span><span>${esc(basename(row.target))}${directories.has(row.source)?'<small>完整文件夹</small>':versionSources.has(row.source)?'<small>版本保留</small>':''}</span></mdui-button><small>${row.action==='复制'?'已复制 · 原件保留':'已移动'}</small></li>`).join('')}</ul>`:''}
              ${folder.items.length>limit?`<mdui-button variant="text" class="btn btn-sm btn-ghost result-more" type="button" data-result-more="${folder.id}" data-result-kind="files">再显示 ${Math.min(50,folder.items.length-limit)} 项（本层共 ${folder.items.length} 项）</mdui-button>`:''}
            </div>`;
        };
        function branch(folder:Folder):string {
            const open=filtering?!collapsed.has(folder.path):expanded.has(folder.path);
            return `<details class="result-folder" data-result-group="${folder.id}" data-result-path="${esc(folder.path)}" data-result-loaded="${open}" ${open?'open':''}>
              <summary title="${esc(folder.path)}"><span class="folder-shape" aria-hidden="true"></span><span>${esc(folder.name)}</span><small>${filtering?'匹配 ':''}${folder.count} 项</small></summary>
              ${open?contents(folder):''}
            </details>`;
        }
        host.innerHTML=`<section class="result-directory" aria-labelledby="result-directory-title">
          <div class="result-directory-head"><h2 id="result-directory-title"><span class="folder-shape" aria-hidden="true"></span>${esc(plan.project)}</h2><p class="sub">本次放入的文件 · 点文件名在 Finder 中查看</p></div>
          ${versionCount?`<div class="project-suggestion-actions"><mdui-button variant="text" type="button" class="btn btn-sm btn-ghost" id="result-version-filter" aria-pressed="${versionsOnly}">${versionsOnly?'查看全部文件':`查看保留的不同版本（${versionCount} 项）`}</mdui-button></div>`:''}
          ${rows.length?`<label class="result-search"><span class="sr-only">查找整理好的文件</span><input id="result-search" class="input input-bordered" type="search" placeholder="查找文件或文件夹" value="${esc(query)}" aria-label="查找整理好的文件"></label>`:''}
          <div class="result-directory-scroll">${tree.count?contents(tree):`<p class="result-empty">${rows.length?'没有找到匹配的文件':done.moved+done.copied?'整理已完成，暂未返回文件明细。请打开项目文件夹查看。':'这次没有文件整理成功，原文件仍保留。'}</p>`}</div>
          ${rows.length&&rows.length!==done.moved+done.copied?'<p class="result-notice sub">部分整理明细未返回，请打开项目文件夹核对。</p>':''}
        </section>
        ${done.failed||done.stopped?`<details class="result-failures" open><summary>${done.stopped?`待处理 ${(done.stopped||0)+done.failed} 项`:`未整理成功 ${done.failed} 项`}</summary><p class="sub">可以检查原因后重新整理，这些文件仍在原处。</p><ul>${failures.slice(0,failureLimit).map(row=>`<li><strong>${esc(basename(row.source))}</strong><span>${esc(row.action==='未开始'?'尚未开始，文件仍在原处。':row.error||'未完成整理，请重新预览。')}</span></li>`).join('')}</ul>${!failures.length?'<p>没有返回未完成明细，请打开原位置检查。</p>':''}${failures.length>failureLimit?'<mdui-button variant="text" type="button" class="btn btn-sm btn-ghost" id="result-failures-more">查看更多未完成项</mdui-button>':''}</details>`:''}`;
        if(done.duplicates){
            if(!done.moved&&!done.copied&&!done.failed){const empty=host.querySelector('.result-empty');if(empty)empty.textContent='目标已有相同内容，无需再搬入。';}
            host.insertAdjacentHTML('beforeend',`<details class="merge-review" id="result-duplicates" ${duplicatesOpen?'open':''}><summary>重复 ${done.duplicates} 项 · 原件保留在原处</summary><p class="sub">已重新核对保留文件的完整内容；没有删除重复来源。</p><ul>${duplicates.slice(0,duplicateLimit).map(row=>`<li><strong>${esc(basename(row.source))}</strong><p class="sub">原件：${esc(row.source)}</p><p class="sub">对应文件：${esc(row.target)}</p><mdui-button variant="text" class="btn btn-sm btn-ghost" type="button" data-result-open="${esc(row.source)}">查看原件</mdui-button></li>`).join('')}</ul>${duplicates.length<done.duplicates?'<p class="sub">部分重复明细未返回，请查看整理记录。</p>':''}${duplicates.length>duplicateLimit?'<mdui-button variant="text" class="btn btn-sm btn-ghost" type="button" id="result-more-duplicates">再显示 50 项</mdui-button>':''}</details>`);
            const detail=host.querySelector<HTMLDetailsElement>('#result-duplicates')!;detail.ontoggle=()=>{if(host.contains(detail))duplicatesOpen=detail.open};
            const more=host.querySelector<HTMLButtonElement>('#result-more-duplicates');if(more)more.onclick=()=>{duplicateLimit+=50;duplicatesOpen=true;render()};
        }
        host.querySelectorAll<HTMLDetailsElement>('[data-result-group]').forEach(details=>{
            details.ontoggle=()=>{
                if(!host.contains(details))return;
                const group=shown.get(Number(details.dataset.resultGroup))!;
                if(details.open){expanded.add(group.path);collapsed.delete(group.path);if(details.dataset.resultLoaded!=='true'){render();host.querySelector<HTMLElement>(`[data-result-group="${group.id}"] > summary`)?.focus({preventScroll:true})}}
                else {expanded.delete(group.path);if(filtering)collapsed.add(group.path)}
            };
        });
        host.querySelectorAll<HTMLButtonElement>('[data-result-open]').forEach(button=>button.onclick=()=>onOpen(button.dataset.resultOpen!));
        host.querySelectorAll<HTMLButtonElement>('[data-result-more]').forEach(button=>button.onclick=()=>{
            const id=Number(button.dataset.resultMore),isFolder=button.dataset.resultKind==='folders',pagination=isFolder?folderLimits:limits,previous=pagination.get(id)||50;
            pagination.set(id,previous+50);render();
            const selector=isFolder?'> .result-folder > summary':'> .result-file-list > li > button';
            host.querySelectorAll<HTMLElement>(`[data-result-contents="${id}"] ${selector}`)[previous]?.focus();
        });
        const versionFilter=host.querySelector<HTMLButtonElement>('#result-version-filter');if(versionFilter)versionFilter.onclick=()=>{versionsOnly=!versionsOnly;collapsed.clear();render();host.querySelector<HTMLButtonElement>('#result-version-filter')?.focus()};
        const input=host.querySelector<HTMLInputElement>('#result-search');
        if(input)input.oninput=()=>{const start=input.selectionStart,end=input.selectionEnd;query=input.value;limits.clear();folderLimits.clear();collapsed.clear();render();const next=host.querySelector<HTMLInputElement>('#result-search')!;next.focus();next.setSelectionRange(start,end)};
        const more=host.querySelector<HTMLButtonElement>('#result-failures-more');
        if(more)more.onclick=()=>{const previous=failureLimit;failureLimit+=20;render();const next=host.querySelector<HTMLElement>(`.result-failures li:nth-child(${previous+1})`);if(next){next.tabIndex=-1;next.focus()}};
        const scroll=host.querySelector('.result-directory-scroll');if(scroll)scroll.scrollTop=scrollTop;
    }
    render();
    return {invalidate:()=>{host.innerHTML='<p class="result-notice" role="status">撤销后文件位置可能已变化，请打开项目文件夹查看当前内容。</p>'}};
}
