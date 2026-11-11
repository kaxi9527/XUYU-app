import {organizerAction} from './ui-actions';

type Status={supported:boolean;enabled:boolean;startAtLogin:boolean;loginAvailable:boolean;loginProblem?:string;message:string};
export function mountBackgroundSettings(host:HTMLElement,api:<T=unknown>(method:string,...args:unknown[])=>Promise<T>,esc:(value:unknown)=>string):void {
    let state:Status,busy=false;
    const live=()=>host.isConnected;
    function render(problem=''){
        if(!live())return;
        host.innerHTML=`<h2>后台管理</h2><p class="sub">关闭窗口后，继续检查已授权的项目。可从菜单栏重新打开；选择「退出 序屿」会停止检查。</p><label><input id="background-enabled" type="checkbox" ${state.enabled?'checked':''} ${state.supported?'':'disabled'}> 关闭窗口后继续管理</label><label><input id="background-login" type="checkbox" ${state.startAtLogin?'checked':''} ${state.supported&&state.loginAvailable&&!state.loginProblem?'':'disabled'}> 登录时自动启动</label><p class="sub">${esc(state.message)}</p>${state.loginProblem?`<p class="sub">${esc(state.loginProblem)}</p>`:''}<p class="sub">只延续已有授权；不会自动开启项目追踪、读取 AI 会话或调用模型。</p><div class="actions">${organizerAction('background-save','保存后台设置',true)}<span id="background-result" role="status" aria-live="polite">${esc(problem|| (state.enabled?'后台管理已开启':'后台管理未开启'))}</span></div>`;
        const summary=document.createElement('p');summary.id='background-state';summary.textContent=(state.enabled?'后台管理已开启':'后台管理未开启')+(state.startAtLogin?' · 登录启动已开启':' · 登录启动未开启');host.querySelector('h2')!.after(summary);
        const enabled=host.querySelector<HTMLInputElement>('#background-enabled')!,login=host.querySelector<HTMLInputElement>('#background-login')!,save=host.querySelector<HTMLElement&{disabled:boolean}>('#background-save')!;
        save.disabled=!state.supported;
        enabled.onchange=()=>{if(!enabled.checked)login.checked=false};
        login.onchange=()=>{if(login.checked)enabled.checked=true};
        save.onclick=async()=>{
            if(busy)return;busy=true;save.disabled=enabled.disabled=login.disabled=true;
            try{state=await api<Status>('SetBackgroundSettings',enabled.checked,login.checked);render('设置已保存'+(state.startAtLogin?'，下次登录自动启动。':'。'));}
            catch(e){const error=e instanceof Error?e.message:String(e);try{state=await api<Status>('BackgroundStatus')}catch{}render('保存未完成：'+error);}
            finally{busy=false}
        };
    }
    void api<Status>('BackgroundStatus').then(value=>{state=value;render()}).catch(e=>{if(live())host.innerHTML=`<h2>后台管理</h2><p role="alert">${esc(e instanceof Error?e.message:String(e))}</p>`});
}
