import {aiBrandMark} from './ai-brands';
import './model-center.css';

type API=<T=unknown>(method:string,...args:unknown[])=>Promise<T>;
type Confirm=(html:string,ok:string)=>Promise<boolean>;
type Options={authHeader?:string;anthropicVersion?:string;chatTokenField?:string;probeTokens?:number};
type Provider={id:string;name:string;protocol:string;baseURL:string;hasCredential:boolean;options:Options};
type Model={id:string;providerID:string;modelID:string;name:string;verification?:{at:string}};
type State={revision:number;providers:Provider[];models:Model[];settings:{mode:string;activeModelID?:string};pendingCredentialCleanup:number};
type Preset={id:string;name:string;protocol:string;baseURL:string;options:Options};
type Account={agent:string;user:string;plan:string;on:boolean;lapsed:string};
type Accounts={accounts:Account[];capabilities:{id:string;available:boolean;requirement?:string}[]};
type Login={id:string;agent:string;url:string;code?:string;state:string;pasteCallback?:boolean;opened?:boolean;error?:string};
type Draft={token:string;expiresAt:string;verified:boolean};
type Result={error?:string;uncertain?:boolean;receipt?:{revision:number;modelID?:string;verified?:boolean};draftVerification?:{verified:boolean};models?:{models:{id:string;name:string}[];nextCursor?:string};problem?:{message?:string}};
const protocols:Record<string,string>={responses:'OpenAI Responses',chat:'OpenAI Chat Completions',anthropic:'Anthropic Messages',gemini:'Google Gemini'};
const subscriptions:Record<string,string>={codex:'ChatGPT / Codex',claude:'Claude',copilot:'GitHub Copilot',gemini:'Gemini Code Assist',antigravity:'Antigravity'};
const message=(e:unknown)=>e instanceof Error?e.message:String(e);

export function mountModelCenter(view:HTMLElement,api:API,esc:(v:unknown)=>string,confirm:Confirm){
 let disposed=false,busy=false,loadEpoch=0,previewToken='',requestID='',requestKind='',login:Login|undefined,poll:ReturnType<typeof setTimeout>|undefined;
 let state:State,presets:Preset[]=[],accounts:Accounts={accounts:[],capabilities:[]},accountError='',cleanupDraft:(()=>void)|undefined;
 view.innerHTML=`<section class="model-center"><header class="bar"><div><a href="#/settings" class="sub">设置 · 模型与账号</a><h1>模型接入中心</h1></div></header><p class="model-intro">选择你习惯的模型，或连接已有的订阅账号。</p><p id="model-message" role="status" aria-live="polite">正在读取配置……</p><div id="model-body"></div></section>`;
 const shell=view.querySelector<HTMLElement>('.model-center')!,body=shell.querySelector<HTMLElement>('#model-body')!,status=shell.querySelector<HTMLElement>('#model-message')!;
 const live=()=>!disposed&&shell.isConnected;
 const say=(text:string)=>{if(live())status.textContent=text;};
 const button=(id:string,label:string,tone='')=>`<mdui-button id="${id}" class="btn btn-sm ${tone}" variant="${tone==='btn-primary'?'filled':'text'}">${label}</mdui-button>`;
 function lock(on:boolean){busy=on;body.querySelectorAll<any>('mdui-button,input,select,textarea').forEach(el=>{if(!el.id.startsWith('login-')&&el.id!=='model-cancel-request')el.disabled=on||el.dataset.unavailable==='true';});}
 async function checked(method:string,...args:unknown[]){const r=await api<Result>(method,...args);if(r.error||r.uncertain)throw new Error(r.error||'结果尚未确认，请重新读取配置后查看。');return r;}
 async function refresh(){
  const epoch=++loadEpoch;
  const results=await Promise.allSettled([api<{state:State}>('ModelConnections'),api<Preset[]>('ModelConnectionPresets'),api<Accounts>('ModelAccounts')]);
  if(!live()||epoch!==loadEpoch)return;
  if(results[0].status==='rejected')throw results[0].reason;
  state=results[0].value.state;presets=results[1].status==='fulfilled'?results[1].value:[];
  accountError=results[2].status==='rejected'?message(results[2].reason):'';
  if(results[2].status==='fulfilled')accounts=results[2].value;
  render();
 }
 async function run(work:()=>Promise<void>){if(busy||!live())return;lock(true);try{await work();}catch(e){say(message(e));}finally{if(live())lock(false);}}
 function render(){
  cleanupDraft?.();cleanupDraft=undefined;
  const active=state.models.find(m=>m.id===state.settings.activeModelID);
  body.innerHTML=`<div class="model-mode"><div><strong>${state.settings.mode==='agent'?'当前模型：'+esc(active?.name||active?.modelID||'未选择'):'工具模式'}</strong><p>${state.settings.mode==='agent'?'模型连接已选定。文件处理仍遵循各项目的授权范围。':'整理、浏览和文件标记可以直接使用；需要模型时再连接。'}</p></div>${state.settings.mode==='agent'?button('model-tool-mode','切回工具模式'):''}</div><section class="model-section"><div class="model-section-title"><div><h2>服务商与模型</h2><p>官方 API、本地服务和已授权的订阅模型。</p></div>${button('model-add','添加服务商','btn-primary')}</div><div id="provider-list">${state.providers.length?state.providers.map(providerRow).join(''):'<p class="model-empty">还没有连接服务商。你可以添加 API，也可以使用下方的订阅账号。</p>'}</div><div id="model-editor"></div></section><section class="model-section"><div class="model-section-title"><div><h2>订阅账号</h2><p>连接你已有的账号，授权过程由服务商完成。</p></div></div><div id="account-list">${accountError?`<p class="model-error" role="alert">${esc(accountError)}</p>${button('account-retry','重新读取账号')}`:accountRows()}</div><div class="subscription-options">${accounts.capabilities.map(c=>`<mdui-button variant="outlined" class="btn" data-login="${esc(c.id)}" ${c.available?'':`disabled data-unavailable="true"`} title="${esc(c.available?'添加订阅账号':'需要先安装 '+(c.requirement||'官方客户端'))}">${aiBrandMark(c.id,esc)}添加 ${esc(subscriptions[c.id]||c.id)}${c.available?'':'（需官方客户端）'}</mdui-button>`).join('')}</div><div id="model-login"></div></section><details class="model-protocols"><summary>协议适配与数据存放</summary><p>支持 ${Object.values(protocols).map(esc).join('、')}。API 服务商可自定义地址、认证头和模型名称。</p><p>API Key 由本机凭据存储管理。订阅授权保存在 序屿 独立账号目录，不读取其他客户端的登录配置。切换模型不会移动项目文件或清除项目记忆。</p><p>连接测试会向所选服务发送一条简短测试请求，可能使用少量额度。</p></details>${state.pendingCredentialCleanup?`<p class="model-error">有旧凭据等待清理。${button('model-cleanup','重试清理')}</p>`:''}`;
  body.querySelector('#model-add')?.addEventListener('click',()=>editor());
  body.querySelector('#model-tool-mode')?.addEventListener('click',()=>void run(async()=>{await checked('SetWorkShelfMode','tool','',state.revision,crypto.randomUUID());await refresh();say('已切回工具模式。');}));
  body.querySelector('#account-retry')?.addEventListener('click',()=>void run(async()=>{await refresh();say(accountError||'账号状态已更新。');}));
  body.querySelector('#model-cleanup')?.addEventListener('click',()=>void run(async()=>{await checked('RetryModelCredentialCleanup');await refresh();say('旧凭据清理状态已更新。');}));
  state.providers.forEach((p,i)=>{
   body.querySelector(`#provider-edit-${i}`)?.addEventListener('click',()=>editor(p));
   body.querySelector(`#provider-list-${i}`)?.addEventListener('click',()=>void run(()=>discover(p,i)));
   body.querySelector(`#provider-remove-${i}`)?.addEventListener('click',()=>void run(async()=>{if(!await confirm(`<h2>移除 ${esc(p.name)}？</h2><p>移除这项模型配置及其 API Key 引用，项目文件和记忆保留。</p>`,'移除服务商'))return;await checked('DeleteModelProvider',p.id,state.revision,crypto.randomUUID());await refresh();say('服务商已移除。');}));
  });
  state.models.forEach((m,i)=>{
   body.querySelector(`#model-verify-${i}`)?.addEventListener('click',()=>void run(()=>verify(m)));
   body.querySelector(`#model-use-${i}`)?.addEventListener('click',()=>void run(async()=>{if(!await confirm(`<h2>使用 ${esc(m.name||m.modelID)}？</h2><p>设为 序屿 当前模型。项目记忆保留，观察与整理权限不随模型切换自动扩大。</p>`,'使用这个模型'))return;await checked('SetWorkShelfMode','agent',m.id,state.revision,crypto.randomUUID());await refresh();say('当前模型已更新。');}));
  });
  body.querySelectorAll<HTMLElement>('[data-login]').forEach(b=>b.onclick=()=>void run(()=>startLogin(b.dataset.login!)));
  accounts.accounts.forEach((a,i)=>{
   body.querySelector(`#account-models-${i}`)?.addEventListener('click',()=>void run(()=>subscriptionModels(a,i)));
   body.querySelector(`#account-toggle-${i}`)?.addEventListener('click',()=>void run(async()=>{await api('SetModelAccount',a.agent,a.user,!a.on,false);await refresh();say(a.on?'账号已停用。':'已切换账号，同一服务商的其他账号已停用。');}));
   body.querySelector(`#account-remove-${i}`)?.addEventListener('click',()=>void run(async()=>{if(!await confirm(`<h2>移除此订阅账号？</h2><p>${esc(subscriptions[a.agent]||a.agent)} · ${esc(a.user)}</p><p>移除 序屿 保存的授权。以后可以重新登录。</p>`,'移除账号'))return;await api('SetModelAccount',a.agent,a.user,false,true);await refresh();say('账号已移除。');}));
  });
  if(login)renderLogin();lock(busy);
 }
 function providerRow(p:Provider,index:number){
  const subscription=p.protocol==='subscription';
  return `<article class="provider-row"><div class="provider-title">${aiBrandMark(p.name,esc)}<div><h3>${esc(p.name)}</h3><p>${subscription?'订阅授权 · 跟随当前启用账号':esc(protocols[p.protocol]||p.protocol)+' · '+(p.hasCredential?'已保存 API Key':'未保存 API Key')}</p></div><div class="provider-actions">${subscription?'':button('provider-edit-'+index,'编辑')}${button('provider-list-'+index,'获取模型')}${button('provider-remove-'+index,'移除')}</div></div>${subscription?'':`<p class="model-endpoint">${esc(p.baseURL)}</p>`}<ul class="provider-models">${state.models.filter(m=>m.providerID===p.id).map(m=>{const i=state.models.indexOf(m);return `<li><div><strong>${esc(m.name||m.modelID)}</strong><span>${m.verification?'已测试连接':'尚未测试'}${state.settings.activeModelID===m.id?' · 当前模型':''}</span></div>${button('model-verify-'+i,'测试连接')}${m.verification?button('model-use-'+i,'设为当前模型'):''}</li>`;}).join('')}</ul><div id="discovered-${index}"></div></article>`;
 }
 function accountRows(){return accounts.accounts.length?accounts.accounts.map((a,i)=>`<div><article class="account-row">${aiBrandMark(a.agent,esc)}<div><h3>${esc(subscriptions[a.agent]||a.agent)}</h3><p>${esc(a.user)}${a.plan?' · '+esc(a.plan):''}</p><span>${a.lapsed?'授权已过期':a.on?'正在使用':'已停用'}</span></div><div class="provider-actions">${a.on&&!a.lapsed?button('account-models-'+i,'查看模型'):''}${button('account-toggle-'+i,a.on?'停用':'切换到此账号')}${button('account-remove-'+i,'移除')}</div></article><div id="subscription-models-${i}"></div></div>`).join(''):'<p class="model-empty">还没有订阅账号。选择一个服务商开始授权。</p>';}

 async function saveSubscription(agent:string,modelID:string){
  const preview=await api<{token:string}>('PreviewSubscriptionModel',agent,modelID,state.revision);previewToken=preview.token;
  try{if(!live()||!await confirm(`<h2>添加订阅模型？</h2><p>${esc(subscriptions[agent])} · ${esc(modelID)}</p><p>使用当前启用的订阅账号。添加后测试连接，再设为当前模型。</p>`,'添加模型'))return;await checked('SaveModelConnection',previewToken,crypto.randomUUID());await refresh();say('订阅模型已添加，可以测试连接。');}
  finally{if(previewToken){void api('DiscardModelConnection',previewToken).catch(()=>{});previewToken='';}}
 }
 async function subscriptionModels(account:Account,index:number){
  say('正在读取订阅可用模型……');const page=await api<{models:{id:string;name:string}[]}>('ListSubscriptionModels',account.agent);if(!live())return;
  const host=body.querySelector<HTMLElement>('#subscription-models-'+index)!;
  host.innerHTML=page.models.length?`<label for="subscription-choice-${index}">选择订阅模型</label><select id="subscription-choice-${index}"><option value="">选择模型</option>${page.models.map(m=>`<option value="${esc(m.id)}">${esc(m.name||m.id)}</option>`).join('')}</select>`:'<p class="sub">此账号尚未返回可用模型，请检查套餐或重新授权。</p>';
  host.querySelector<HTMLSelectElement>('select')?.addEventListener('change',e=>{const id=(e.target as HTMLSelectElement).value;if(id)void run(()=>saveSubscription(account.agent,id));});say(`读取到 ${page.models.length} 个订阅模型。`);
 }
 function editor(provider?:Provider,modelID?:string){
  if(busy)return;cleanupDraft?.();
  const host=body.querySelector<HTMLElement>('#model-editor')!,model=state.models.find(m=>m.providerID===provider?.id);
  let draft:Draft|undefined,closed=false,expiry:ReturnType<typeof setTimeout>|undefined,verifiedModel='',saveAttempt='',nextCursor='';
  const available=new Map<string,string>();
  host.innerHTML=`<section class="model-editor"><h3>${provider?'编辑服务商':'添加服务商'}</h3><p class="sub">先读取模型或填写模型名称，测试通过后即可保存并使用。</p>
   <div class="model-fields">
    <label for="model-preset">快速填写</label><select id="model-preset" data-connection-field><option value="">自定义服务商</option>${presets.map((p,i)=>`<option value="${i}">${esc(p.name)}</option>`).join('')}</select>
    <label for="provider-name">服务商名称</label><input id="provider-name" data-connection-field value="${esc(provider?.name||'')}" maxlength="100" autocomplete="off">
    <label for="model-protocol">API 协议</label><select id="model-protocol" data-connection-field>${Object.entries(protocols).map(([k,v])=>`<option value="${k}" ${provider?.protocol===k?'selected':''}>${esc(v)}</option>`).join('')}</select>
    <label for="model-url">API 地址</label><input id="model-url" data-connection-field value="${esc(provider?.baseURL||'')}" placeholder="https://api.example.com/v1" spellcheck="false" autocomplete="off">
    <label for="model-key">API Key</label><input id="model-key" data-connection-field type="password" autocomplete="new-password" placeholder="${provider?.hasCredential?'留空保留现有密钥':'本地服务可留空'}">
   </div>
   ${provider?.hasCredential?'<label class="model-remove-key"><input id="model-remove-key" data-connection-field type="checkbox">移除已有密钥，使用无需密钥的服务</label>':''}
   <details class="model-advanced"><summary>高级协议设置</summary><div class="model-fields">
    <label for="model-auth-header">认证头</label><input id="model-auth-header" data-connection-field value="${esc(provider?.options?.authHeader||'')}" placeholder="默认由协议决定">
    <label for="model-version">Anthropic API 版本</label><input id="model-version" data-connection-field value="${esc(provider?.options?.anthropicVersion||'')}" placeholder="协议默认值">
    <label for="model-token-field">Chat token 字段</label><select id="model-token-field" data-connection-field><option value="">协议默认值</option><option value="max_tokens">max_tokens</option><option value="max_completion_tokens">max_completion_tokens</option></select>
   </div></details>
   <div class="model-draft-tools">${button('draft-list','读取模型列表')}${button('draft-edit','修改连接信息')}</div>
   <div id="draft-available" class="model-fields" hidden></div>
   <div class="model-fields model-draft-choice"><label for="model-id">模型名称 / ID</label><input id="model-id" value="${esc(modelID||model?.modelID||'')}" placeholder="从列表选择，也可以直接填写" spellcheck="false" autocomplete="off"></div>
   <p id="model-editor-message" role="status" aria-live="polite">草稿尚未保存。测试会发送简短请求，可能使用少量额度，不包含项目文件。</p>
   <div class="actions">${button('draft-test','测试连接','btn-primary')}${button('model-save','保存并使用')}${button('model-editor-cancel','取消')}</div>
  </section>`;
  const q=<T extends HTMLInputElement|HTMLSelectElement>(id:string)=>host.querySelector<T>('#'+id)!;
  const alive=()=>live()&&!closed&&host.isConnected;
  const empty=body.querySelector<HTMLElement>('#provider-list .model-empty');if(empty)empty.hidden=true;
  const add=body.querySelector<HTMLElement>('#model-add');if(add)add.hidden=true;
  const keyPlaceholder=q<HTMLInputElement>('model-key').placeholder;
  const note=(s:string)=>{if(alive())host.querySelector('#model-editor-message')!.textContent=s;};
  function controls(){
   if(!alive())return;
   host.querySelectorAll<any>('[data-connection-field]').forEach(el=>{el.dataset.unavailable=String(!!draft||!!saveAttempt);el.disabled=busy||!!draft||!!saveAttempt;});
   const ready=!!draft?.verified&&verifiedModel===q('model-id').value.trim();
   for(const [id,off] of [['draft-edit',!draft||!!saveAttempt],['model-save',!ready&&!saveAttempt],['draft-list',!!saveAttempt],['draft-test',!!saveAttempt],['model-editor-cancel',!!saveAttempt],['model-id',!!saveAttempt],['draft-models',!!saveAttempt],['draft-more',!!saveAttempt]] as [string,boolean][]){
    const el=host.querySelector<any>('#'+id);if(el){el.dataset.unavailable=String(off);el.disabled=busy||off;}
   }
   const save=host.querySelector<HTMLElement>('#model-save')!;save.textContent=saveAttempt?'确认保存结果':'保存并使用';
   save.setAttribute('variant',ready||saveAttempt?'filled':'text');
   host.querySelector('#draft-test')!.setAttribute('variant',ready?'text':'filled');
   host.querySelector('#draft-test')!.classList.toggle('btn-primary',!ready);
  }
  function discard(){
   clearTimeout(expiry);const token=draft?.token;draft=undefined;verifiedModel='';q<HTMLInputElement>('model-key').placeholder=keyPlaceholder;
   if(token)void api('DiscardModelDraft',token).catch(()=>{});
  }
  cleanupDraft=()=>{closed=true;clearTimeout(expiry);q('model-key').value='';discard();if(empty)empty.hidden=false;if(add)add.hidden=false;};
  q<HTMLSelectElement>('model-token-field').value=provider?.options?.chatTokenField||'';
  q('model-preset').onchange=()=>{const p=presets[Number(q('model-preset').value)];if(q('model-preset').value===''||!p)return;q('provider-name').value=p.name;q('model-protocol').value=p.protocol;q('model-url').value=p.baseURL;q('model-auth-header').value=p.options?.authHeader||'';q('model-version').value=p.options?.anthropicVersion||'';q('model-token-field').value=p.options?.chatTokenField||'';};
  host.querySelector('#model-remove-key')?.addEventListener('change',()=>{if(q<HTMLInputElement>('model-remove-key').checked)q('model-key').value='';});
  q('model-id').oninput=()=>{if(draft)draft.verified=false;verifiedModel='';controls();note('模型已更改，请测试连接。');};
  async function ensureDraft(){
   if(draft)return draft;
   const input={revision:state.revision,providerID:provider?.id||'',modelRecordID:provider&&(!modelID||modelID===model?.modelID)?model?.id||'':'',providerName:q('provider-name').value.trim(),protocol:q('model-protocol').value,baseURL:q('model-url').value.trim(),modelID:q('model-id').value.trim(),modelName:q('model-id').value.trim(),apiKey:q('model-key').value,removeCredential:host.querySelector<HTMLInputElement>('#model-remove-key')?.checked||false,options:{...provider?.options,authHeader:q('model-auth-header').value.trim(),anthropicVersion:q('model-version').value.trim(),chatTokenField:q('model-token-field').value}};
   if(!input.providerName||!input.baseURL){input.apiKey='';throw new Error('请填写服务商名称和 API 地址。');}
   let result:Draft;
   try{result=await api<Draft>('CreateModelDraft',input);}finally{input.apiKey='';q('model-key').value='';}
   if(!alive()){void api('DiscardModelDraft',result.token);throw new Error('编辑已结束');}
   draft=result;q<HTMLInputElement>('model-key').placeholder='密钥设置已暂存于本次草稿';expiry=setTimeout(()=>{if(!saveAttempt){discard();controls();note('草稿已过期，请重新填写密钥并测试。');}},Math.max(0,Date.parse(result.expiresAt)-Date.now()));
   controls();return result;
  }
  async function request(method:string,...args:unknown[]){
   requestID=crypto.randomUUID();requestKind='draft';cancelRequestButton();
   try{return await checked(method,...args,requestID);}catch(e){note(message(e));throw e;}finally{requestID='';shell.querySelector('#model-cancel-request')?.remove();}
  }
  async function readModels(cursor=''){
   const current=await ensureDraft();note('正在读取模型列表……');
   const r=await request('ListModelDraft',current.token,cursor);if(!alive()||draft!==current)return;
   if(!cursor)available.clear();for(const m of r.models?.models||[])available.set(m.id,m.name||m.id);nextCursor=r.models?.nextCursor||'';
   const choices=host.querySelector<HTMLElement>('#draft-available')!;choices.hidden=false;
   choices.innerHTML=`<label for="draft-models">可用模型</label><select id="draft-models"><option value="">选择模型</option>${[...available].map(([id,name])=>`<option value="${esc(id)}">${esc(name)}</option>`).join('')}</select>${nextCursor?button('draft-more','读取更多'):''}`;
   q('draft-models').value=q('model-id').value;
   q('draft-models').onchange=()=>{if(q('draft-models').value){q('model-id').value=q('draft-models').value;q('model-id').dispatchEvent(new Event('input'));}};
   host.querySelector('#draft-more')?.addEventListener('click',()=>void run(()=>readModels(nextCursor)));
   note(available.size?`已读取 ${available.size} 个模型。选择一个，然后测试连接。`:'服务商没有返回列表。可直接填写模型名称并测试。');controls();
  }
  host.querySelector('#draft-list')!.addEventListener('click',()=>void run(()=>readModels()));
  host.querySelector('#draft-edit')!.addEventListener('click',()=>{discard();available.clear();host.querySelector<HTMLElement>('#draft-available')!.hidden=true;controls();note('可以修改连接信息。新填写的密钥已清空；更改地址、协议或认证头时须重新输入密钥，或明确移除凭据。');});
  host.querySelector('#model-editor-cancel')!.addEventListener('click',()=>{cleanupDraft?.();cleanupDraft=undefined;host.replaceChildren();say('已取消，当前模型保持原样。');});
  host.querySelector('#draft-test')!.addEventListener('click',()=>void run(async()=>{
   const id=q('model-id').value.trim();if(!id){note('请先选择或填写模型名称。');q('model-id').focus();return;}
   const current=await ensureDraft();current.verified=false;verifiedModel='';controls();
   draft=await api<Draft>('SelectModelDraft',current.token,id,id);if(!alive()){discard();return;}
   note('正在测试连接……');const result=await request('VerifyModelDraft',draft.token);
   if(!alive()||!draft)return;draft.verified=result.draftVerification?.verified===true;verifiedModel=draft.verified?id:'';
   note(draft.verified?'测试通过。保存后将使用这个模型，项目文件与记忆保持原样。':'测试未通过，请检查连接信息。');controls();
  }));
  host.querySelector('#model-save')!.addEventListener('click',()=>void run(async()=>{
   if(!draft)return;
   if(saveAttempt){
    const known=await api<{found:boolean}>('ModelOperation',saveAttempt);
    if(known.found){await finishSave();return;}
   }else{
    if(!draft.verified||verifiedModel!==q('model-id').value.trim())return;
    saveAttempt=crypto.randomUUID();controls();
   }
   let definiteFailure=false;
   try{
    const result=await api<Result>('SaveModelDraft',draft.token,saveAttempt);
    definiteFailure=!!result.error&&!result.uncertain;
    if(result.error||result.uncertain)throw new Error(result.error||'结果尚未确认');
    await finishSave();
   }catch(e){
    const known=await api<{found:boolean}>('ModelOperation',saveAttempt).catch(()=>null);
    if(known?.found){await finishSave();return;}
    if(known&&definiteFailure){saveAttempt='';draft.verified=false;controls();note('未保存这份草稿。请检查提示，修改连接信息后重新测试。');}
    else note('保存结果尚未确认。点击“确认保存结果”读取记录；请勿重复添加。');throw e;
   }
  }));
  async function finishSave(){cleanupDraft?.();cleanupDraft=undefined;await refresh();say('已保存并使用这个模型。');}
  controls();q('provider-name').focus();host.scrollIntoView({block:'nearest'});
 }
 async function verify(model:Model){
  if(!await confirm(`<h2>测试 ${esc(model.name||model.modelID)}？</h2><p>发送一条简短测试请求，不包含项目文件。可能使用少量模型额度。</p>`,'测试连接'))return;
  requestID=crypto.randomUUID();requestKind='verify';say('正在测试连接……');cancelRequestButton();
  try{const r=await checked('VerifyModelConnection',model.id,state.revision,requestID);await refresh();say(r.receipt?.verified===false?'连接未通过，请检查配置后重试。':'连接测试通过。');}finally{requestID='';shell.querySelector('#model-cancel-request')?.remove();}
 }
 function cancelMethod(){return requestKind==='draft'?'CancelModelDraftRequest':requestKind==='verify'?'CancelModelVerification':'CancelModelDiscovery';}
 function cancelRequestButton(){if(!live())return;shell.querySelector('#model-cancel-request')?.remove();status.insertAdjacentHTML('afterend',button('model-cancel-request','取消请求'));shell.querySelector('#model-cancel-request')!.addEventListener('click',()=>{void api(cancelMethod(),requestID).catch(e=>say(message(e)));say('正在取消请求……');});}
 async function discover(p:Provider,index:number){
  requestID=crypto.randomUUID();requestKind='list';say('正在获取模型列表……');cancelRequestButton();
  try{const r=await checked('ListConnectionModels',p.id,state.revision,'',requestID);if(!live())return;const models=r.models?.models||[];const host=body.querySelector<HTMLElement>('#discovered-'+index)!;host.innerHTML=models.length?`<label for="available-${index}">可用模型</label><select id="available-${index}"><option value="">选择要添加的模型</option>${models.map(m=>`<option value="${esc(m.id)}">${esc(m.name||m.id)}</option>`).join('')}</select>`:'<p>服务商未返回模型。你仍可在编辑中填写自定义模型名称。</p>';host.querySelector<HTMLSelectElement>('select')?.addEventListener('change',e=>{const value=(e.target as HTMLSelectElement).value;if(value){if(p.protocol==='subscription')void run(()=>saveSubscription(value.split('/')[0],value));else editor(p,value)};});say(r.models?.nextCursor?'已读取一页模型；也可按名称手动添加。':`已读取 ${models.length} 个模型。`);}finally{requestID='';shell.querySelector('#model-cancel-request')?.remove();}
 }
 async function startLogin(agent:string){
  if(login?.state==='waiting'){say('请先完成或取消当前授权。');return;}
  login=await api('StartModelAccountLogin',agent);if(!live()){if(login)void api('FinishModelAccountLogin',login.id,'',true);return;}renderLogin();scheduleLogin();say('授权已开始。完成登录后会自动更新账号。');
 }
 function renderLogin(){
  if(!login||!live())return;const host=body.querySelector<HTMLElement>('#model-login')!;
  if(login.state==='done'){host.innerHTML='<p role="status">账号授权完成。</p>';return;}
  if(login.state==='failed'||login.state==='canceled'){host.innerHTML=`<p role="status">${login.state==='canceled'?'已取消授权。':esc(login.error||'授权未完成，请重试。')}</p>`;return;}
  // Preserve typed callback text while polling.
  if(host.dataset.flow===login.id)return;host.dataset.flow=login.id;
  host.innerHTML=`<div class="model-login"><h3>连接 ${esc(subscriptions[login.agent]||login.agent)}</h3><p>在服务商页面完成登录，序屿 会在这里等待。</p>${login.code?`<p>设备验证码：<strong class="device-code">${esc(login.code)}</strong></p>`:''}${login.url?button('login-open','打开授权页面','btn-primary'):''}${button('login-cancel','取消授权')}${login.pasteCallback?'<label for="login-callback">无法自动返回时，粘贴授权后的完整地址</label><input id="login-callback" type="password" autocomplete="off" spellcheck="false">'+button('login-submit','提交回调'):''}<p id="login-message" role="status"></p></div>`;
  host.querySelector('#login-open')?.addEventListener('click',()=>void api('OpenModelAccountLoginPage',login!.id).catch(()=>say('授权页未打开，请取消后重试。')));
  host.querySelector('#login-cancel')?.addEventListener('click',()=>void finishLogin(true));
  host.querySelector('#login-submit')?.addEventListener('click',()=>void finishLogin(false));
 }
 async function finishLogin(cancel:boolean){
  if(!login)return;const input=body.querySelector<HTMLInputElement>('#login-callback'),callback=cancel?'':input?.value||'';if(input)input.value='';
  try{const next=await api<Login>('FinishModelAccountLogin',login.id,callback,cancel);login=next;if(!live())return;renderLogin();if(next.state==='done')await refresh();}catch(e){say(message(e));}
 }
 function scheduleLogin(){clearTimeout(poll);if(!login||!['waiting','installing'].includes(login.state)||!live())return;poll=setTimeout(async()=>{try{const next=await api<Login>('ModelAccountLoginStatus',login!.id);if(!live())return;login=next;renderLogin();if(next.state==='done'){await refresh();say('订阅账号已连接。');}}catch(e){say(message(e));}scheduleLogin();},1500);}
 void refresh().then(()=>say('')).catch(e=>{if(live()){say(message(e));body.innerHTML=button('model-load-retry','重新读取');body.querySelector('#model-load-retry')!.addEventListener('click',()=>void run(async()=>{await refresh();say('配置已读取。');}));}});
 return()=>{disposed=true;cleanupDraft?.();cleanupDraft=undefined;loadEpoch++;clearTimeout(poll);const key=body.querySelector<HTMLInputElement>('#model-key');if(key)key.value='';if(previewToken)void api('DiscardModelConnection',previewToken).catch(()=>{});if(requestID)void api(cancelMethod(),requestID).catch(()=>{});if(login&&['waiting','installing'].includes(login.state))void api('FinishModelAccountLogin',login.id,'',true).catch(()=>{});};
}
