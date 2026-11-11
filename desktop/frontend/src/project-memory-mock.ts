import type {MemoryAction,MemoryStatus,MemoryEvent,MemoryFile,MemoryReceipt} from './project-memory-types';
export function createProjectMemoryMock(scenario:string){
    const states=new Map<string,MemoryStatus>(),events=new Map<string,MemoryEvent[]>(),receipts=new Map<string,MemoryReceipt>();
    let reads=0,lost=false;
    const files=(key:string):MemoryFile[]=>['首页.fig','交付稿/首页.png','.codex'].map((n,i)=>({id:'file-'+i,fact:{path:'/Users/demo/Work/'+key.slice(key.indexOf(':')+1)+'/'+n,kind:i===2?'directory':'file',size:1234,modifiedAt:'2026-11-09T08:00:00Z',hashState:'metadata_only'},status:'present',lastEvent:'2:'+(i+1)}));
    function state(key:string){let s=states.get(key);if(!s){const empty=scenario==='memory-empty';s={projectID:empty?'':'project-'+key,revision:empty?0:2,enabled:!empty,automatic:scenario==='memory-auto-error',backgroundRunning:true,backgroundChecking:false,backgroundError:scenario==='memory-auto-error'?'自动检查的记录容量已达上限；历史保留':'',nextCheckAt:'',currentScope:{roots:['/Users/demo/Work/'+key.slice(key.indexOf(':')+1)]},currentScopeSHA:'fixture-scope-'+key,scopeChanged:scenario==='memory-scope',tracked:empty?0:3,missing:0,untracked:0,lastObservedAt:empty?'':'2026-11-09T08:00:00Z',observationComplete:scenario!=='memory-gap',coverage:scenario==='memory-gap'?[{path:'/Users/demo/Work/Agentour/.codex',status:'excluded',reason:'隐藏配置目录只记录位置，不读取内部内容'}]:[],projectionPending:false};states.set(key,s);events.set(key,empty?[]:files(key).filter(f=>f.fact.kind==='file').map((f,i)=>({id:'2:'+(i+1),kind:i?'content_changed':'first_observed',observedAt:'2026-11-09T08:00:00Z',source:'filesystem_observation',actor:'unknown',reason:i?'两次内容摘要不同，未保存旧内容副本':'第一次观察到这个文件，不能证明创建时间',after:f})))}return s}
    return async(method:string,args:unknown[])=>{
        const key=method==='ApplyProjectMemoryAction'?(args[0] as MemoryAction).projectKey:String(args[0]);
        const s=state(key);
        s.realtimeWatching=s.enabled&&s.automatic&&scenario!=='memory-watch-fallback';
        s.realtimeError=s.automatic&&scenario==='memory-watch-fallback'?'实时监听暂不可用；继续定期检查已授权范围':'';
        if(scenario==='memory-execution'&&method==='ProjectMemoryEvents') {
            const before=files(key)[0].fact,after={...before,path:s.currentScope.roots[0]+'/源文件/首页.fig'};
            const base={observedAt:'2026-11-09T08:00:00Z',source:'local_execution',actor:'workshelf',reason:'依据已保存的整理回执记录位置；没有覆盖后续修改'};
            const items:MemoryEvent[]=[{...base,id:'undo-1',kind:'placement_undone',undo:{evidence:{before:after,after:before}}},{...base,id:'move-1',kind:'placement_recorded',placement:{evidence:{before,after}}},{...base,id:'review-1',kind:'placement_reconciled',source:'local_user',actor:'unknown',reconciliation:{evidence:{before,target:after}}}];
            return {project:{id:s.projectID,name:key.slice(key.indexOf(':')+1),path:s.currentScope.roots[0]},revision:s.revision,items,next:''};
        }
        if(scenario==='memory-delay'&&method==='ProjectMemory')await new Promise(r=>setTimeout(r,700));
        if(scenario==='memory-error'&&method==='ProjectMemory'&&++reads===1)throw new Error('项目记录暂时无法读取');
        if(method==='ProjectMemory')return structuredClone(s);
        if(method==='ProjectMemoryEvents')return {project:{id:s.projectID,name:key.slice(key.indexOf(':')+1),path:s.currentScope.roots[0]},revision:s.revision,items:structuredClone(events.get(key)||[]),next:''};
        if(method==='ProjectMemoryFiles')return {revision:s.revision,total:s.tracked,items:s.tracked?files(key):[]};
        if(method==='ProjectMemoryOperation'){const r=receipts.get(key+':'+args[1]);return {found:!!r,record:r?.record}}
        if(method==='ApplyProjectMemoryAction'){
            const a=args[0] as MemoryAction;const old=receipts.get(key+':'+a.operationID);if(old)return {receipt:old,uncertain:false};
            if(a.revision!==s.revision||a.scopeSHA!==s.currentScopeSHA)return {error:'项目记录已更新，请重新读取',uncertain:false};
            if(a.kind==='scan'&&!s.enabled)return {error:'记录已暂停',uncertain:false};
            s.revision++;s.projectID='project-'+key;
            if(a.kind==='enable'||a.kind==='pause'){s.enabled=a.kind==='enable';s.scopeChanged=false}
            if(a.kind==='automatic_on'||a.kind==='automatic_off'){s.automatic=a.kind==='automatic_on';s.backgroundError=''}
            if(a.kind==='source_on'||a.kind==='source_off'){
                s.sources??=[];let source=s.sources.find(x=>x.path===a.sourcePath&&x.adapter===a.sourceAdapter&&!!x.directory===!!a.sourceDirectory);
                if(!source){source={id:'source-fixture',adapter:a.sourceAdapter!,path:a.sourcePath!,enabled:true,scopeChanged:false,lastReadAt:'',line:0,skipped:0,more:false,problem:'',origins:0,directory:!!a.sourceDirectory};s.sources.push(source)}
                source.enabled=a.kind==='source_on';
            }
            const now=new Date().toISOString();let changed:MemoryEvent[];
            if(a.kind==='scan'){s.lastObservedAt=now;s.tracked=3;changed=files(key).filter(f=>f.fact.kind==='file').map((f,i)=>({id:s.revision+':'+i,kind:'first_observed',observedAt:now,source:'filesystem_observation',actor:'unknown',reason:'首次观察，不推断作者或创建时间',after:f}))}
            else changed=[{id:s.revision+':1',kind:a.kind.startsWith('automatic_')?'automatic_changed':s.revision===1?'tracking_started':'tracking_state',observedAt:now,source:'local_user',actor:'local_user',reason:'你调整了此项目的文件记录设置，历史保留',enabled:s.enabled,automatic:s.automatic}];
            if(a.kind.startsWith('source_'))changed[0].kind='source_grant_changed';
            if(a.kind==='scan')for(const source of s.sources||[]){if(source.enabled){source.lastReadAt=now;source.line=3;source.origins=1;if(source.directory){source.records=2;source.readRecords=2}changed.unshift({id:s.revision+':source',kind:'file_source_recorded',observedAt:now,source:'agent_declaration',actor:'unverified',reason:'来源记录与文件快照关联，作者身份未验证',origin:{sourcePath:source.directory?source.path+'/session.jsonl':source.path,binding:'snapshot_match',claim:{agent:source.adapter,session:'session-example',task:'首页设计',path:files(key)[0].fact.path,line:3,recordSHA:'a'.repeat(64),basis:'tool_result'}}})}}
            events.set(key,[...changed,...events.get(key)||[]]);const receipt={record:{operationID:a.operationID,revision:s.revision,events:changed},projectionPending:false};receipts.set(key+':'+a.operationID,receipt);
            if((scenario==='memory-lost'||scenario==='memory-auto-lost'&&a.kind.startsWith('automatic_')||scenario==='memory-source-lost'&&a.kind.startsWith('source_'))&&!lost){lost=true;throw new Error('连接中断，结果需要核对')}
            return {receipt,uncertain:false};
        }
        if(method==='ProjectMemoryGraph'){
            const list=events.get(key)||[];const nodes:any[]=[],edges:any[]=[];
            if(list.length){nodes.push({id:'mp',kind:'memory_project',name:key.slice(key.indexOf(':')+1)});for(const e of list){const id='event-'+e.id;nodes.push({id,kind:'memory_event',name:e.kind==='content_changed'?'内容变化':'首次观察',extra:e.reason+' · 来源未确认'});edges.push({id:'edge-'+id,type:'memory_event',from:'mp',to:id,reason:e.reason,confidence:'账本记录',weight:1})}}
            return {project:key.slice(key.indexOf(':')+1),projectKey:key,revision:s.revision,next:'',nodes,edges,note:'图只读已有文件记录，不推断操作者或因果关系。'};
        }
        throw new Error('unsupported memory fixture method');
    };
}
