// Development-only evidence fixture. Real ledger and byte checks have Go tests.
export function createWorkGraphMock(scenario:string){
    let reads=0;
    const file=(id:string)=>({source:'首页设计源稿.fig',export:'首页导出.png',missing:'早期草稿.fig',ref:'品牌色板.fig'}[id]||id+'.txt');
    const work=(id:string)=>({id,title:id==='brand'?'品牌色板':id==='home'?'首页设计':'其他成果 '+id,currentUsableVersionID:id==='home'?'export':''});
    const version=(id:string)=>({version:{id,workID:id==='ref'?'brand':'home',role:id==='export'?'export':'source',registeredAt:'2026-11-09T00:00:00Z',content:{sha256:'fixture',size:100}},locations:[{location:{path:'/Users/demo/Work/Agentour/'+file(id)},state:id==='missing'?'missing':'available'}],state:id==='missing'?'missing':'available',recordedUsable:id==='export',usable:id==='export',provenance:[],confirmations:[]});
    return async(method:string,args:unknown[])=>{
        const key=String(args[0]),id=String(args[1]||'');
        if(method==='VersionFile'){
            if(scenario==='work-graph-open-fail')throw new Error('这个版本的原内容已改变，请重新读取。');
            return '/Users/demo/Work/Agentour/'+file(id);
        }
        if(method==='VersionDetail')return {revision:7,item:version(id)};
        if(method==='WorkVersions')return {work:work(id),revision:7,total:id==='brand'?1:3,offset:0,limit:20,items:(id==='brand'?['ref']:['export','source','missing']).map(version),relations:[{versionID:'export',parentVersionID:'source',kind:'exported_from'}]};
        if(method==='ProjectWorks')return {revision:7,total:2,offset:0,limit:50,items:['home','brand'].map(id=>({...work(id),versions:id==='home'?3:1,usable:id==='home',usableState:id==='home'?'available':'not_confirmed'}))};
        if(method!=='WorkGraph')throw new Error('Unsupported graph fixture method');
        if(scenario==='work-graph-error'&&++reads===1)throw new Error('作品账本暂时无法读取');
        if(scenario==='work-graph-race')await new Promise(resolve=>setTimeout(resolve,key==='root:Agentour'?800:100));
        const project=key.replace(/^(root|place):/,'');
        const overview=scenario==='work-graph-pages'?Array.from({length:32},(_,i)=>i===0?'home':`extra${i}`):['home','brand'];
        const ids=id?(id==='brand'?['ref']:['export','source','missing']):overview;
        const offset=Number(args[2]||0),limit=id?20:30,total=scenario==='work-graph-empty'?0:ids.length;
        const selected=total?ids.slice(offset,offset+limit):[];
        const wn=(wid:string)=>({id:'work:'+wid,kind:'work',name:work(wid).title,workId:wid,versionCount:wid==='home'?3:1,state:wid==='home'?'available':'not_confirmed',usable:wid==='home'});
        const vn=(vid:string,reference=false)=>({id:'version:'+vid,kind:'version',name:file(vid),workId:vid==='ref'?'brand':'home',versionId:vid,source:version(vid).version.role,state:reference?'unchecked':version(vid).state,usable:!reference&&vid==='export',recordedUsable:!reference&&vid==='export',reference});
        const nodes:any[]=id?[wn(id),...selected.map(v=>vn(v))]:selected.map(wn);
        const edges:any[]=id?selected.map(v=>({id:'membership:'+v,type:'work_version',from:'work:'+id,to:'version:'+v,reason:'同一作品下的登记版本，不表示演变顺序',confidence:'账本记录',weight:1})):[];
        if(id==='home'&&total){nodes.push(vn('ref',true));edges.push({id:'export:home',type:'exported_from',from:'version:source',to:'version:export',directed:true,reason:'由源稿导出为 PNG；账本登记，不代表已验证导出过程',confidence:'账本记录',weight:2},{id:'export:brand',type:'exported_from',from:'version:ref',to:'version:source',directed:true,reason:'已登记的跨作品导出关系',confidence:'账本记录',weight:2})}
        return {project,projectKey:key,workId:id,workTitle:id?work(id).title:'',revision:7,nodes,edges,total,offset,limit,note:'导出箭头仅来自已登记关系；内容状态为本次核对结果。',skipped:''};
    };
}
