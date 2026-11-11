// Development fixtures only. Never imported by production builds.
const state:any={revision:0,providers:[],models:[],settings:{mode:'tool'},pendingCredentialCleanup:0};
const presets=[{id:'openai',name:'OpenAI',protocol:'responses',baseURL:'https://api.openai.com/v1',options:{}},{id:'deepseek',name:'DeepSeek',protocol:'chat',baseURL:'https://api.deepseek.com',options:{}},{id:'ollama',name:'Ollama',protocol:'chat',baseURL:'http://127.0.0.1:11434/v1',options:{}}];
const accounts:any[]=[];const previews=new Map<string,any>();const receipts=new Map<string,any>();let login:any;
const drafts=new Map<string,any>();
const draftRequests=new Map<string,(value:any)=>void>();
function invalidateAccounts(agent:string){
 const providers=state.providers.filter((p:any)=>p.protocol==='subscription'&&p.baseURL.endsWith('/'+agent)).map((p:any)=>p.id);
 state.models.forEach((m:any)=>{if(providers.includes(m.providerID)){delete m.verification;if(state.settings.activeModelID===m.id)state.settings={mode:'tool'};}});
 if(providers.length)state.revision++;
}
export function modelCenterMock(method:string,args:any[],scenario:string):any{
 if(method==='CreateModelDraft'){
  const p={...args[0],hasCredential:!!args[0].apiKey};delete p.apiKey;
  const token=crypto.randomUUID(),expiresAt=new Date(Date.now()+300000).toISOString();drafts.set(token,{...p,expiresAt,verified:false});return{token,expiresAt,verified:false};
 }
 if(method==='DiscardModelDraft')return drafts.delete(args[0]);
 if(method==='CancelModelDraftRequest'){draftRequests.get(args[0])?.({error:'操作已取消'});draftRequests.delete(args[0]);return true;}
 if(method==='SelectModelDraft'){
  const d=drafts.get(args[0]);if(!d)throw new Error('连接草稿已失效，请重新填写并测试');d.modelID=args[1];d.modelName=args[2];d.verified=false;
  return{token:args[0],expiresAt:d.expiresAt,verified:false};
 }
 if(method==='ListModelDraft')return scenario==='models-draft-list-failure'?{error:'此服务暂不支持读取列表，请手动填写模型名称'}:{models:{models:[{id:'fixture-model',name:'Fixture model'},{id:'fixture-second',name:'Fixture second'}]}};
 if(method==='VerifyModelDraft'){
  const d=drafts.get(args[0]);if(!d)throw new Error('连接草稿已失效，请重新填写并测试');
  if(scenario==='models-draft-cancel')return new Promise(resolve=>draftRequests.set(args[1],resolve));
  d.verified=scenario!=='models-draft-failure';return d.verified?{draftVerification:{verified:true}}:{error:'API Key 无效或没有权限',draftVerification:{verified:false}};
 }
 if(method==='SaveModelDraft'){
  if(receipts.has(args[1]))return{receipt:receipts.get(args[1])};
  const d=drafts.get(args[0]);if(!d?.verified)return{error:'请先测试连接'};
  const token=crypto.randomUUID();previews.set(token,d);const result=modelCenterMock('SaveModelConnection',[token,args[1]],scenario);
  state.models.find((m:any)=>m.id===result.receipt.modelID).verification={at:new Date().toISOString()};state.settings={mode:'agent',activeModelID:result.receipt.modelID};result.receipt.verified=true;
  return scenario==='models-draft-ack-lost'?Promise.reject(new Error('连接中断')):result;
 }
 if(method==='ListSubscriptionModels')return{models:[{id:args[0]+'/fixture-subscription',name:'Fixture subscription model'}]};
 if(method==='PreviewSubscriptionModel'){const token=crypto.randomUUID();previews.set(token,{providerName:args[0]+' 订阅',protocol:'subscription',baseURL:'https://accounts.workshelf.invalid/'+args[0],modelID:args[1],modelName:args[1].split('/')[1],options:{}});return{token};}
 if(method==='ModelConnectionPresets')return presets;
 if(method==='ModelConnections')return {state:structuredClone(state),credentialStorage:true,protocolReady:true};
 if(method==='ModelAccounts'){if(scenario==='models-accounts-error')throw new Error('订阅授权模块暂时无法连接，请重试');return {accounts:structuredClone(accounts),capabilities:['codex','claude','copilot','gemini','antigravity'].map(id=>({id,available:true}))};}
 if(method==='PreviewModelConnection'){const p={...args[0]};p.hasCredential=!!p.apiKey;delete p.apiKey;const token=crypto.randomUUID();previews.set(token,p);return{token,connection:{credentialAction:p.hasCredential?'replace':'keep'}};}
 if(method==='DiscardModelConnection'){previews.delete(args[0]);return true;}
 if(method==='SaveModelConnection'){
  const p=previews.get(args[0]);if(!p)throw new Error('连接预览已失效');const providerID=p.providerID||crypto.randomUUID(),id=p.modelRecordID||crypto.randomUUID();
  state.providers=state.providers.filter((x:any)=>x.id!==providerID);state.providers.push({id:providerID,name:p.providerName,protocol:p.protocol,baseURL:p.baseURL,options:p.options,hasCredential:p.hasCredential});
  state.models=state.models.filter((x:any)=>x.id!==id);state.models.push({id,providerID,modelID:p.modelID,name:p.modelName});state.revision++;const receipt={revision:state.revision,modelID:id};receipts.set(args[1],receipt);return{receipt};
 }
 if(method==='ModelOperation')return{found:receipts.has(args[0]),receipt:receipts.get(args[0])};
 if(method==='VerifyModelConnection'){if(scenario==='models-probe-error')return{error:'服务商拒绝了测试请求，请检查 API Key。'};const m=state.models.find((x:any)=>x.id===args[0]);m.verification={at:new Date().toISOString()};return{receipt:{revision:++state.revision,verified:true}};}
 if(method==='SetWorkShelfMode'){state.settings={mode:args[0],activeModelID:args[1]};return{receipt:{revision:++state.revision}};}
 if(method==='ListConnectionModels')return{models:{models:[{id:'fixture-model-2',name:'Fixture Model 2'}]}};
 if(method==='DeleteModelProvider'){state.providers=state.providers.filter((p:any)=>p.id!==args[0]);state.models=state.models.filter((m:any)=>m.providerID!==args[0]);state.settings={mode:'tool'};return{receipt:{revision:++state.revision}};}
 if(method==='RetryModelCredentialCleanup')return{pending:0};
 if(method==='CancelModelDiscovery'||method==='CancelModelVerification')return true;
 if(method==='StartModelAccountLogin'){login={id:crypto.randomUUID(),agent:args[0],url:'https://example.test/authorize',state:'waiting',pasteCallback:true};return{...login};}
 if(method==='ModelAccountLoginStatus')return{...login};
 if(method==='OpenModelAccountLoginPage')return true;
 if(method==='FinishModelAccountLogin'){if(args[2])login.state='canceled';else{login.state='done';accounts.push({agent:login.agent,user:'fixture@example.test',plan:'测试订阅',on:true,lapsed:false});}return{...login};}
 if(method==='SetModelAccount'){invalidateAccounts(args[0]);const i=accounts.findIndex(a=>a.agent===args[0]&&a.user===args[1]);if(args[3])accounts.splice(i,1);else accounts.forEach(a=>{if(a.agent===args[0])a.on=a.user===args[1]&&args[2]});return true;}
 return undefined;
}
