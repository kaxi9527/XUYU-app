export type MemoryAPI=<T=unknown>(method:string,...args:unknown[])=>Promise<T>;
export type MemoryScope={roots:string[];files?:string[]};
export type MemorySource={id:string;adapter:string;path:string;enabled:boolean;scopeChanged:boolean;lastReadAt:string;line:number;skipped:number;more:boolean;problem:string;origins:number;directory?:boolean;records?:number;readRecords?:number;unreadable?:number};
export type MemoryStatus={projectID:string;revision:number;enabled:boolean;automatic:boolean;backgroundRunning:boolean;backgroundChecking:boolean;backgroundError:string;realtimeWatching?:boolean;realtimeError?:string;nextCheckAt:string;currentScope:MemoryScope;currentScopeSHA:string;scopeChanged:boolean;tracked:number;missing:number;untracked:number;lastObservedAt:string;observationComplete:boolean;coverage:{path:string;status:string;reason:string}[];projectionPending:boolean;sources?:MemorySource[];origins?:number};
export type MemoryFile={id:string;fact:{path:string;kind:string;size:number;modifiedAt:string;hashState:string;sha256?:string};status:string;lastEvent:string};
type PlacementPaths={before:MemoryFile['fact'];after:MemoryFile['fact']};
export type MemoryEvent={id:string;kind:string;observedAt:string;source:string;actor:string;reason:string;before?:MemoryFile;after?:MemoryFile;placement?:{evidence:PlacementPaths};undo?:{evidence:PlacementPaths};reconciliation?:{evidence:{before:MemoryFile['fact'];source?:MemoryFile['fact'];target?:MemoryFile['fact']}};note?:{kind:string;text:string;agent?:string;model?:string;evidence?:string};origin?:{sourcePath:string;binding:string;claim:{agent:string;session?:string;task?:string;path:string;beforePath?:string;line:number;recordSHA:string;basis:string}};auto?:{status:string;receiptPath:string;requestedBy?:string;objection?:{adapter:string;session:string;sourcePath:string;line:number;recordSHA:string;provider?:string;model?:string};modelDecisions?:{provider:string;model:string;reason?:string;inputSHA:string}[];items:{before:string;after:string;agent?:string;session?:string;task?:string;sourcePath?:string;line?:number;recordSHA?:string;placementReason?:string}[]};enabled?:boolean;automatic?:boolean};
export type MemoryRecord={operationID:string;revision:number;events?:MemoryEvent[]};
export type MemoryReceipt={record:MemoryRecord;projectionPending:boolean;projectionError?:string};
export type MemoryAction={kind:'enable'|'pause'|'scan'|'automatic_on'|'automatic_off'|'source_on'|'source_off';projectKey:string;revision:number;operationID:string;scopeSHA:string;sourceAdapter?:string;sourcePath?:string;sourceDirectory?:boolean};
export type MemoryPage={project:{id:string;name:string;path:string};revision:number;items:MemoryEvent[];next:string};
export type MemoryFiles={revision:number;total:number;items:MemoryFile[]};
export const memoryEventLabel=(e:MemoryEvent)=>({first_observed:'首次观察',content_changed:'内容变化',metadata_changed:'信息更新',location_changed:'位置变化',no_longer_seen:'原位置未找到',seen_again:'再次找到',tracking_removed:'停止追踪',tracking_started:'开始记录',scope_changed:'追踪范围更新',tracking_state:e.enabled?'恢复记录':'暂停记录',automatic_changed:e.automatic?'开启自动检查':'关闭自动检查',context_note:e.note?.kind==='user_decision'?'用户决定':'Agent 建议',placement_recorded:'整理文件',placement_reconciled:'核对整理结果',placement_undone:'撤销整理',automatic_project_organized:'自动整理项目',automatic_project_restored:'反对后恢复'}[e.kind]||'文件记录');
export function memoryEventPaths(e:MemoryEvent) {
    if(e.origin)return {before:e.origin.claim.beforePath,after:e.origin.claim.path};
    const execution=e.undo?.evidence||e.placement?.evidence;
    if(execution)return {before:execution.before.path,after:execution.after.path};
    if(e.reconciliation)return {before:e.reconciliation.evidence.before.path,after:(e.reconciliation.evidence.target||e.reconciliation.evidence.source)?.path};
    return {before:e.before?.fact.path,after:e.after?.fact.path};
}
export const memoryEventActor=(e:MemoryEvent)=>e.reconciliation?'你确认了现场；原操作来源未确认':e.source==='local_execution'&&e.actor==='workshelf'?'序屿执行':e.source==='local_user'?'你的操作':e.source==='agent_declaration'?'Agent 声明，尚未核验':'来源未确认';
