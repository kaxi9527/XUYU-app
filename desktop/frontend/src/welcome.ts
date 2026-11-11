import './welcome.css';
import {checkAccess} from './access-status';

type API=<T=unknown>(method:string,...args:unknown[])=>Promise<T>;
type Escape=(value:unknown)=>string;
type Profile={name:string;folders:string[];roles:Record<string,string>;rules:unknown[]};
type WorkType={label:string;layout:{name:string;folders:string[];roles:Record<string,string>}};
const preference='workshelf.start-purpose.v1';
export function hasStartChoice(){try{return !!localStorage.getItem(preference);}catch{return false;}}
function remember(value:string){try{localStorage.setItem(preference,value);}catch{/* The current choice still works without storage. */}}
const folder='<svg viewBox="0 0 24 24" fill="none" aria-hidden="true"><path d="M3 7a2 2 0 0 1 2-2h5l2 3h7a2 2 0 0 1 2 2v9H3Z" fill="currentColor"/><path d="M3 11h18" stroke="var(--surface)" stroke-width="1.4"/></svg>';
export function mountWelcome(view:HTMLElement,api:API,esc:Escape){
 view.innerHTML=`<section class="welcome"><div class="welcome-mark">${folder}</div><p class="eyebrow">欢迎使用 序屿</p><h1>先从哪件事开始？</h1><p class="welcome-intro">把散落的文件收在一起，或给新项目一个清楚的起点。</p><div class="start-options"><a href="#/organize" data-purpose="old"><span class="start-symbol" aria-hidden="true">↗</span><h2>整理旧项目文件</h2><p>找到散落的项目，按工种整理。<br>先看目录预览，再决定怎么放。</p><span class="start-link">开始整理 <span aria-hidden="true">→</span></span></a><a href="#/new-project" data-purpose="new"><span class="start-symbol" aria-hidden="true">＋</span><h2>管理新项目文件</h2><p>用工种模板建好项目目录，<br>让后续的文件有处可放。</p><span class="start-link">建立项目 <span aria-hidden="true">→</span></span></a></div><a class="welcome-skip" href="#/projects" data-purpose="later">先看看我的项目</a><p class="welcome-footnote">以后都可以在「我的项目」里选择这两种方式。</p></section>`;
 view.querySelectorAll<HTMLAnchorElement>('[data-purpose]').forEach(a=>a.onclick=(event)=>{
  event.preventDefault();const purpose=a.dataset.purpose!,destination=a.getAttribute('href')!;
  view.innerHTML=`<section class="welcome"><p class="eyebrow">设置文件访问</p><h1>让序屿找到你的项目</h1><p class="welcome-intro">默认识别桌面、文稿、下载、Projects 和主目录中的项目。点击检查后，系统可能逐项询问是否允许访问；macOS 通常会记住授权，但权限可能发生变化。</p><p>识别时读取这些位置；移动文件按你的整理设置执行。</p><div class="actions"><mdui-button id="access-check" class="btn btn-primary" variant="filled">检查并授权</mdui-button><mdui-button id="access-later" class="btn" variant="text">稍后设置</mdui-button></div><div id="access-result" role="status" aria-live="polite"></div><p class="welcome-footnote">其他项目目录可在「设置」中加入扫描范围。AI 会话目录需另外授权。权限状态可随时在设置中查看。</p></section>`;
  const finish=()=>{remember(purpose);location.hash=destination;};
  view.querySelector('#access-later')!.addEventListener('click',finish);
  view.querySelector('#access-check')!.addEventListener('click',async()=>{
   const button=view.querySelector<HTMLButtonElement>('#access-check')!,result=view.querySelector<HTMLElement>('#access-result')!;
   button.disabled=true;result.textContent='正在检查，系统可能会询问访问权限……';
   try{const folders=await checkAccess(api);if(!result.isConnected)return;
    result.innerHTML=`<p>${folders.map(f=>`${esc(f.name)}：${esc(f.status)}`).join('　')}</p>${folders.some(f=>f.status==='无法访问')?'<p>如已拒绝，请在 macOS「系统设置 → 隐私与安全性 → 文件与文件夹」中允许 序屿，然后返回重新检查。</p>':''}<mdui-button id="access-continue" class="btn btn-primary" variant="filled">继续</mdui-button>`;
    result.querySelector('#access-continue')!.addEventListener('click',finish);
   }catch(e){if(result.isConnected)result.textContent=`检查失败：${esc(e instanceof Error?e.message:String(e))}。可以稍后在设置中重试。`;}
   finally{if(button.isConnected){button.disabled=false;button.textContent='重新检查';}}
  });
 });
}

export function mountNewProject(view:HTMLElement,api:API,esc:Escape,confirm:(html:string,ok:string)=>Promise<boolean>){
 let disposed=false,busy=false,context:{root:string;workTypes:WorkType[]}|undefined;
 let operationID=crypto.randomUUID();
 view.innerHTML=`<header class="bar"><div><a href="#/projects" class="sub">我的项目</a><h1>新建项目</h1></div></header><div class="new-project" id="new-project-form"><p role="status">正在读取工种模板……</p></div>`;
 const host=view.querySelector<HTMLElement>('#new-project-form')!;
 const live=()=>!disposed&&host.isConnected;
 async function load(){try{
  context=await api('OrganizeContext','');if(!live())return;
  host.innerHTML=`<div class="new-project-fields"><label for="new-name">项目名称</label><input id="new-name" maxlength="100" placeholder="例如：春季品牌设计" autocomplete="off"><label for="new-worktype">工种模板</label><select id="new-worktype"><option value="">选择你的工种</option>${context!.workTypes.map((w,i)=>`<option value="${i}">${esc(w.label)}</option>`).join('')}</select><p class="sub">选择最接近的一项，目录名称也可以自己改。</p><label for="new-folders">项目里的文件夹</label><textarea id="new-folders" rows="7" placeholder="选择工种后显示，每行一个文件夹" disabled></textarea><p class="sub">保留工程内部结构，代码和依赖不按扩展名拆开。</p></div><aside class="new-project-preview"><h2>保存位置</h2><p class="path-line">${esc(context!.root)}</p><a href="#/settings">更改保存位置</a><h2>建好后的目录</h2><div id="new-tree" class="new-tree">填写名称并选择工种后，在这里预览。</div></aside><div class="new-project-footer"><p id="new-message" role="status" aria-live="polite"></p><a href="#/projects">取消</a><mdui-button id="create-project" class="btn btn-primary" variant="filled" disabled>建立项目</mdui-button></div>`;
  const name=host.querySelector<HTMLInputElement>('#new-name')!,work=host.querySelector<HTMLSelectElement>('#new-worktype')!,folders=host.querySelector<HTMLTextAreaElement>('#new-folders')!,create=host.querySelector<any>('#create-project')!,message=host.querySelector<HTMLElement>('#new-message')!;
  const list=()=>folders.value.split('\n').map(s=>s.trim()).filter(Boolean);
  function update(){operationID=crypto.randomUUID();create.disabled=busy||!name.value.trim()||work.value===''||!list().length;host.querySelector('#new-tree')!.innerHTML=`<strong>${folder}${esc(name.value.trim()||'项目名称')}</strong><ul>${list().map(f=>`<li>${folder}${esc(f)}</li>`).join('')}</ul>`;message.textContent='';}
  name.oninput=update;folders.oninput=update;work.onchange=()=>{const w=context!.workTypes[Number(work.value)];folders.disabled=work.value==='';folders.value=work.value===''?'':w.layout.folders.join('\n');update();};
  create.onclick=async()=>{
   if(busy||create.disabled)return;
   const w=context!.workTypes[Number(work.value)],profile:Profile={name:w.layout.name,folders:list(),roles:Object.fromEntries(Object.entries(w.layout.roles||{}).filter(([,v])=>list().includes(v))),rules:[]};
   const input={operationID,name:name.value.trim(),workType:w.label,root:context!.root,profile};
   busy=true;create.disabled=true;name.disabled=work.disabled=folders.disabled=true;
   try{
    const yes=await confirm(`<h2>建立「${esc(input.name)}」？</h2><p>保存在 ${esc(input.root)}/${esc(input.name)}</p><ul>${profile.folders.map(f=>`<li>${esc(f)}</li>`).join('')}</ul><p>只建立这些目录和项目说明。文件追踪可稍后在项目内开启。</p>`,'建立项目');
    if(!yes||!live())return;
    message.textContent='正在建立项目……';
    const result=await api<{name:string;path:string}>('CreateProject',input);
    if(!live())return;remember('new');location.hash='#/project/'+encodeURIComponent(result.name);
   }catch(e){if(live())message.textContent=e instanceof Error?e.message:String(e);}
   finally{busy=false;if(live()){create.disabled=false;name.disabled=work.disabled=folders.disabled=false;}}
  };
 }catch(e){if(live()){host.innerHTML=`<p role="alert">${esc(e instanceof Error?e.message:String(e))}</p><mdui-button class="btn" id="new-retry">重新读取</mdui-button>`;host.querySelector('#new-retry')!.addEventListener('click',()=>void load());}}}
 void load();return()=>{disposed=true;};
}
