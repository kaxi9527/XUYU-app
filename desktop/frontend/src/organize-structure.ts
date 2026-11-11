export type PreviewFile = {id:string;source:string;target?:string;folder?:string;reason:string;selected:boolean;blocked:boolean;directory:boolean;crossDevice:boolean;keepOriginal?:boolean;existing?:boolean;operation?:string;versionGroup?:string;representative?:string};
export type PreviewPlan = {project:string;target:string;folders:string[];rows:PreviewFile[];merge?:boolean;mergedFolders?:number};
type Escape = (value:unknown)=>string;
export function selectedFiles(plan:PreviewPlan,choices:Map<string,boolean>) {
    return plan.rows.filter(r=>!r.blocked&&!r.existing&&!!r.target&&(choices.get(r.source)??r.selected));
}
export function structureModel(plan:PreviewPlan,choices:Map<string,boolean>) {
    const selected=selectedFiles(plan,choices),chosen=new Set(selected.map(r=>r.id));
    const existing=plan.rows.filter(r=>r.existing);
    const kept=plan.rows.filter(r=>r.operation!=='duplicate'&&!r.existing&&!chosen.has(r.id)&&(r.blocked||r.keepOriginal||choices.get(r.source)===false));
    const keptIds=new Set(kept.map(r=>r.id));
    const review=plan.rows.filter(r=>r.operation!=='duplicate'&&!r.existing&&!chosen.has(r.id)&&!keptIds.has(r.id));
    const grouped=new Map<string,PreviewFile[]>();
    for(const row of review){const key=row.reason||'尚未匹配归档规则';const group=grouped.get(key);if(group)group.push(row);else grouped.set(key,[row])}
    const reviewGroups=[...grouped].map(([reason,rows],i)=>({id:String(i),reason,rows}));
    const paths=new Set<string>();
    for(const folder of [...plan.folders,...[...selected,...existing].map(r=>r.folder).filter((f):f is string=>!!f)]){
        const parts=folder.split('/');parts.forEach((_,i)=>paths.add(parts.slice(0,i+1).join('/')));
    }
    const under=(r:PreviewFile,path:string)=>r.folder===path||r.folder?.startsWith(path+'/');
    const folders=[...paths].map(path=>({path,name:path.split('/').pop()!,depth:path.split('/').length-1,count:selected.filter(r=>under(r,path)).length,existing:existing.some(r=>under(r,path))}));
    return {selected,existing,kept,review,reviewGroups,folders};
}

// Selection belongs to the full retained plan, never to a filtered or paginated view.
export function mountStructure(host:HTMLElement,plan:PreviewPlan,choices:Map<string,boolean>,esc:Escape,onChoice:()=>void,onFolder:(rows:PreviewFile[],folder:string,refresh?:boolean)=>void,initial='overview',onOpen?:(path:string)=>void,options:{retarget?:boolean;restore?:boolean}={}) {
    let active=initial,query='',limit=50;
    let mergeLimit=50,versionLimit=10;
    const mergeExpanded=new Set<string>(),versionRows=new Map<string,number>();
    const name=(path:string)=>path.split('/').pop()||path;
    const folderIcon='<span class="folder-shape" aria-hidden="true"></span>';
    const folderOptions=(chosen='')=>`<option value="">选择文件夹</option>${chosen&&!plan.folders.includes(chosen)?`<option value="${esc(chosen)}" selected>${esc(chosen)}（保持层级）</option>`:''}${plan.folders.map(f=>`<option value="${esc(f)}" ${chosen===f?'selected':''}>${esc(f)}</option>`).join('')}`;
    function mergeReviewHTML() {
        const duplicates=plan.rows.filter(r=>r.operation==='duplicate'),groups=new Map<string,PreviewFile[]>();
        for(const row of plan.rows){if(!row.versionGroup)continue;const group=groups.get(row.versionGroup)||[];group.push(row);groups.set(row.versionGroup,group)}
        const open=(key:string)=>mergeExpanded.has(key)?'open':'';
        const chosen=new Set(selectedFiles(plan,choices).map(r=>r.source));
        return `${plan.mergedFolders?`<p class="sub merge-folder-note">${plan.mergedFolders} 个同名文件夹将合并层级，内部路径保留。</p>`:''}
          ${duplicates.length?`<details class="merge-review" data-merge-expand="duplicates" ${open('duplicates')}><summary>重复文件 ${duplicates.length} 项 · 原件保留在原处</summary><p class="sub">已核对完整内容。目标只保留一份，不删除这些来源。</p><ul>${duplicates.slice(0,mergeLimit).map(r=>`<li><strong>${esc(name(r.source))}</strong><p class="sub">${esc(r.source)}</p><p class="sub">对应文件：${esc(r.target)}</p>${r.representative!==r.target&&!chosen.has(r.representative||'')?'<p class="sub">保留版本尚未选中，这组先留在原处。</p>':''}<mdui-button variant="text" class="btn btn-sm btn-ghost" type="button" data-open="${esc(r.source)}">查看原件</mdui-button></li>`).join('')}</ul>${duplicates.length>mergeLimit?'<mdui-button variant="text" class="btn btn-sm btn-ghost" type="button" id="merge-more-duplicates">再显示 50 项</mdui-button>':''}</details>`:''}
          ${groups.size?`<details class="merge-review" data-merge-expand="versions" ${open('versions')}><summary>不同版本 ${groups.size} 组 · 默认全部保留</summary><p class="sub">同名不同内容会加来源后缀；已有名称不同的版本保留原名，不覆盖已有文件。</p>${[...groups].slice(0,versionLimit).map(([id,rows])=>`<section class="merge-version-group"><h2>${esc(name(rows[0].source))} 等 · ${rows.length} 份</h2><ul tabindex="0" aria-label="这组的不同版本">${rows.slice(0,versionRows.get(id)||20).map(r=>`<li><strong>${esc(name(r.target||r.source))}</strong><p class="sub">${esc(r.source)}</p><span>${r.existing?'已有内容，保持不变':(choices.get(r.source)??r.selected)?'已选，保留此版本':'未选，保持原处'}</span></li>`).join('')}</ul>${rows.length>(versionRows.get(id)||20)?`<mdui-button variant="text" class="btn btn-sm btn-ghost" type="button" data-version-more="${esc(id)}">再显示 20 份</mdui-button>`:''}<div class="project-suggestion-actions"><mdui-button variant="text" class="btn btn-sm btn-ghost" type="button" data-version-choice="${esc(id)}" data-keep="true">保留这组所有版本</mdui-button><mdui-button variant="text" class="btn btn-sm btn-ghost" type="button" data-version-choice="${esc(id)}" data-keep="false">这组先留在原处</mdui-button></div></section>`).join('')}${groups.size>versionLimit?'<mdui-button variant="text" class="btn btn-sm btn-ghost" type="button" id="merge-more-versions">再显示 10 组</mdui-button>':''}</details>`:''}`;
    }
    function fileItem(r:PreviewFile) {
        const status=r.existing?'已有内容，保持不变':r.blocked?'保留原位':r.keepOriginal?'默认保留原处':r.folder?'放入 '+esc(r.folder):'尚未匹配归档规则';
        return `<li data-file-id="${esc(r.id)}"><div class="structure-file-line">${r.existing?folderIcon:`<input class="checkbox checkbox-primary checkbox-sm" type="checkbox" data-row="${esc(r.id)}" data-source="${esc(r.source)}" aria-label="整理 ${esc(name(r.source))}" ${(choices.get(r.source)??r.selected)?'checked':''} ${r.blocked||!r.target?'disabled':''}><span class="structure-file-kind" aria-hidden="true">${r.directory?'目录':esc(name(r.source).split('.').pop()?.slice(0,5).toUpperCase())}</span>`}<div class="structure-file-name"><strong>${esc(name(r.operation==='version'?r.target||r.source:r.source))}${r.directory?'（保留内部结构）':''}</strong><span>${status}</span></div>${r.crossDevice?'<span class="structure-copy">复制</span>':''}</div><details class="structure-file-detail"><summary>详情与调整</summary><dl><dt>现在的位置</dt><dd>${esc(r.source)}</dd><dt>判断依据</dt><dd>${esc(r.reason)}</dd>${r.target?`<dt>整理后的位置</dt><dd>${esc(r.target)}</dd>`:''}</dl>${r.blocked?'':`<label for="organize-folder-${esc(r.id)}">放入文件夹</label><select class="select select-sm" id="organize-folder-${esc(r.id)}" data-folder-source="${esc(r.source)}">${folderOptions(r.folder)}</select>`}${r.directory&&onOpen?`<mdui-button variant="text" class="btn btn-sm btn-ghost" type="button" data-open="${esc(r.source)}">查看文件夹</mdui-button>`:''}</details></li>`;
    }
    function render() {
        const model=structureModel(plan,choices);
        if(active==='review'&&!model.review.length)active='overview';
        if(active.startsWith('folder:')&&!model.folders.some(f=>'folder:'+f.path===active))active='overview';
        const folder=active.startsWith('folder:')?active.slice(7):'';
        const activeRows=active==='review'?model.review:active==='kept'?model.kept:folder?[...model.selected,...model.existing].filter(r=>r.folder===folder||r.folder?.startsWith(folder+'/')):[];
        const visible=activeRows.filter(r=>(r.source+' '+r.folder).toLowerCase().includes(query.toLowerCase()));
        const title=active==='review'?'需要你决定':active==='kept'?'保留原处':folder||'整理后的项目';
        const description=active==='review'?'同类文件一起处理，选一次文件夹即可。也可以整组留在原处。':active==='kept'?'这些内容不参与本次整理，无需逐项确认。':folder?'文件已自动归类；文件夹内的结构会完整保留。':'目录已经安排好。已有内容保持不变，确认后归入新文件。';
        const shownFolders=model.folders.filter(f=>f.count||f.existing);
        const folderNote=(f:typeof model.folders[number])=>f.existing?(f.count?`已有内容 · 新增 ${f.count} 项`:'已有内容 · 保持不变'):`归入 ${f.count} 项`;
        const files=model.selected.filter(r=>!r.directory).length,dirs=model.selected.length-files;
        host.innerHTML=`<div class="structure-summary"><span>${model.selected.length?`自动归档 <b>${files}</b> 个文件${dirs?`、<b>${dirs}</b> 个完整文件夹`:''}`:'没有需要移动的新文件'}</span>${model.reviewGroups.length?`<span>需要决定 <b>${model.reviewGroups.length}</b> 组</span>`:'<span>无需逐项确认</span>'}${model.existing.length?`<span>已有 ${model.existing.length} 项保持不变</span>`:''}</div>
          <div class="structure-workspace"><nav class="structure-tree" aria-label="整理后的目录结构">
            <mdui-button variant="text" type="button" class="structure-root" data-view="overview" ${active==='overview'?'aria-current="true"':''}>${folderIcon}<span>${esc(plan.project)}</span></mdui-button>
            <ul>${shownFolders.map(f=>`<li style="--folder-depth:${f.depth}"><mdui-button variant="text" type="button" data-view="folder:${esc(f.path)}" aria-label="${esc(f.path)}，${folderNote(f)}" ${active==='folder:'+f.path?'aria-current="true"':''}>${folderIcon}<span>${esc(f.name)}</span><small>${f.existing?(f.count?'+'+f.count:'已有'):f.count}</small></mdui-button></li>`).join('')}</ul>
            <div class="structure-other">${model.reviewGroups.length?`<mdui-button variant="text" type="button" data-view="review" ${active==='review'?'aria-current="true"':''}><span>需要决定</span><small>${model.reviewGroups.length} 组</small></mdui-button>`:''}<mdui-button variant="text" type="button" data-view="kept" ${active==='kept'?'aria-current="true"':''}><span>保留原处</span><small>${model.kept.length}</small></mdui-button></div>
          </nav><section class="structure-content" aria-label="文件夹内容"><header class="structure-content-head"><div class="structure-breadcrumb">${esc(plan.project)}${folder?' / '+esc(folder):''}</div><h2 tabindex="-1" id="structure-title">${esc(title)}${active!=='overview'?` <small>${active==='review'?model.reviewGroups.length+' 组':activeRows.length+' 项'}</small>`:''}</h2><p>${esc(description)}</p></header>
          ${active==='overview'?`<div class="structure-scroll"><ul class="structure-folders">${shownFolders.map(f=>`<li><mdui-button variant="text" type="button" data-view="folder:${esc(f.path)}">${folderIcon}<span><strong>${esc(f.path)}</strong><small>${folderNote(f)}</small></span><span class="structure-folder-arrow" aria-hidden="true">›</span></mdui-button></li>`).join('')||'<li class="structure-no-files">项目已在原处，无需移动。可以继续加入散落在其他位置的文件。</li>'}</ul></div>`:active==='review'?`<div class="structure-scroll"><ul class="structure-groups">${model.reviewGroups.map(g=>`<li><h3>${g.rows.length} 项文件 · 一起归档</h3><p>${esc(g.reason)}</p><p class="structure-group-examples">${g.rows.slice(0,3).map(r=>esc(name(r.source))).join('、')}${g.rows.length>3?' 等':''}</p><label for="group-folder-${g.id}">这组放入</label><select class="select select-sm" id="group-folder-${g.id}">${folderOptions()}</select><div class="structure-group-actions"><mdui-button variant="text" class="btn btn-sm btn-primary" type="button" data-place-group="${g.id}" disabled>整组放入</mdui-button><mdui-button variant="text" class="btn btn-sm btn-ghost" type="button" data-keep-group="${g.id}">整组保留原处</mdui-button></div></li>`).join('')}</ul></div>`:`<label class="structure-filter"><span class="sr-only">筛选当前列表</span><input class="input input-sm" id="structure-filter" value="${esc(query)}" placeholder="在这里找文件"></label><div class="structure-scroll"><ul class="structure-files">${visible.slice(0,limit).map(fileItem).join('')||'<li class="structure-no-files">这里暂时没有文件。</li>'}</ul>${visible.length>limit?`<div class="structure-more"><span>已显示 ${limit} / ${visible.length} 项，筛选可查找全部内容</span><mdui-button variant="text" class="btn btn-sm btn-ghost" type="button" id="structure-more">再显示 50 项</mdui-button></div>`:''}</div>`}
          </section></div><details class="structure-save-path"><summary>保存到哪里？</summary><code class="organize-path">${esc(plan.target)}</code></details>`;
        host.querySelector('.structure-summary')!.insertAdjacentHTML('afterend',mergeReviewHTML());
        host.querySelectorAll<HTMLDetailsElement>('[data-merge-expand]').forEach(el=>el.ontoggle=()=>{if(!host.contains(el))return;const key=el.dataset.mergeExpand!;if(el.open)mergeExpanded.add(key);else mergeExpanded.delete(key)});
        host.querySelectorAll<HTMLButtonElement>('[data-version-choice]').forEach(el=>el.onclick=()=>{plan.rows.filter(r=>r.versionGroup===el.dataset.versionChoice&&!r.blocked&&!r.existing).forEach(r=>choices.set(r.source,el.dataset.keep==='true'));mergeExpanded.add('versions');render();onChoice();host.querySelector<HTMLElement>('[data-merge-expand="versions"] summary')?.focus()});
        host.querySelectorAll<HTMLButtonElement>('[data-version-more]').forEach(el=>el.onclick=()=>{const id=el.dataset.versionMore!;versionRows.set(id,(versionRows.get(id)||20)+20);mergeExpanded.add('versions');render()});
        const moreDup=host.querySelector<HTMLButtonElement>('#merge-more-duplicates');if(moreDup)moreDup.onclick=()=>{mergeLimit+=50;mergeExpanded.add('duplicates');render()};
        const moreVersions=host.querySelector<HTMLButtonElement>('#merge-more-versions');if(moreVersions)moreVersions.onclick=()=>{versionLimit+=10;mergeExpanded.add('versions');render()};
        const focus=()=>host.querySelector<HTMLElement>('#structure-title')!.focus({preventScroll:true});
        host.querySelectorAll<HTMLButtonElement>('[data-view]').forEach(button=>button.onclick=()=>{active=button.dataset.view!;query='';limit=50;render();focus()});
        host.querySelectorAll<HTMLInputElement>('[data-row]').forEach(input=>input.onchange=()=>{choices.set(input.dataset.source!,input.checked);render();onChoice();focus()});
        host.querySelectorAll<HTMLSelectElement>('[data-folder-source]').forEach(select=>{
            if(options.retarget===false){host.querySelector(`label[for="${select.id}"]`)?.remove();select.remove();return}
            select.onchange=()=>{const row=plan.rows.find(r=>r.source===select.dataset.folderSource)!;onFolder([row],select.value)};
        });
        if(options.retarget===false&&folder){
            const groupButton=document.createElement('mdui-button');groupButton.setAttribute('variant','text');groupButton.className='btn btn-sm';groupButton.textContent='这一组保留原处';
            groupButton.onclick=()=>{activeRows.filter(r=>!r.blocked&&!r.existing).forEach(r=>choices.set(r.source,false));render();onChoice();focus()};
            host.querySelector('.structure-content-head')?.append(groupButton);
        }
        if(options.restore){
            const title=host.querySelector('#structure-title');if(active==='overview'&&title)title.textContent='恢复后的文件夹';
            const summary=host.querySelector('.structure-summary > span');if(summary)summary.textContent=`将恢复 ${model.selected.length} 份文件`;
            host.querySelectorAll('.structure-file-detail summary').forEach(el=>el.textContent='查看依据与位置');
        }
        for(const group of model.reviewGroups){
            const select=host.querySelector<HTMLSelectElement>('#group-folder-'+group.id),place=host.querySelector<HTMLButtonElement>(`[data-place-group="${group.id}"]`);
            if(select&&place){select.onchange=()=>{place.disabled=!select.value};place.onclick=()=>onFolder(group.rows,select.value,true)}
            const keep=host.querySelector<HTMLButtonElement>(`[data-keep-group="${group.id}"]`);
            if(keep)keep.onclick=()=>{group.rows.forEach(row=>choices.set(row.source,false));render();onChoice();focus()};
        }
        host.querySelectorAll<HTMLButtonElement>('[data-open]').forEach(button=>button.onclick=()=>onOpen?.(button.dataset.open!));
        const more=host.querySelector<HTMLButtonElement>('#structure-more');if(more)more.onclick=()=>{limit+=50;render()};
        const search=host.querySelector<HTMLInputElement>('#structure-filter');
        if(search)search.oninput=()=>{const start=search.selectionStart;query=search.value;limit=50;render();const next=host.querySelector<HTMLInputElement>('#structure-filter')!;next.focus();next.setSelectionRange(start,start)};
    }
    render();return {active:()=>active};
}
