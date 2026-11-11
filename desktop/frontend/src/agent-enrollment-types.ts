export type AgentEvidence = {path:string;kind:string;status:string};
export type AgentCandidate = {id:string;name:string;kind:string;status:string;evidence:AgentEvidence[]};
export type AgentPolicy = {version:number;enabled:boolean;grants:{agent:string;ruleSHA:string}[];ignored:string[]};
export type AgentEvent = {intent:{agent:string;path?:string;backup?:string};status:string;reason?:string};
export type AgentReceipt = {revision:number;nextDue:string;scan:{startedAt:string;gaps:{path:string;code:string}[]};records:AgentCandidate[];policy?:AgentPolicy;enrollments?:AgentEvent[]};
export type AgentState = {supported:boolean;automaticAgents:string[];backgroundRunning:boolean;lastError?:string;receipt:AgentReceipt};
export type AgentReview = {revision:number;preview:{agent:string;path:string;status:string;reason?:string;instruction:string;digest:string}};
export type AgentAPI = <T>(method:string,...args:unknown[])=>Promise<T>;

// Observation, delivery and actual artifact verification are separate facts.
// This screen deliberately has no "verified" state without artifact evidence.
export function agentPresentation(candidate:AgentCandidate, receipt:AgentReceipt) {
    const event=receipt.enrollments?.find(e=>e.intent.agent===candidate.id);
    const ignored=receipt.policy?.ignored?.includes(candidate.id)??false;
    if(candidate.status==='not_observed') return {label:'本次未发现',detail:'保留历史登记，未判断为卸载。',ignored};
    if(event?.status==='writing') return {label:'正在核对接入结果',detail:'有一笔接入尚未确认，检查后核对。',ignored};
    if(event && ['needs_review','rule_changed_needs_review','not_written'].includes(event.status)) return {label:'需要核对',detail:'规则可能变化或未写入，查看后再决定是否接入。',ignored};
    if(event?.status==='rule_written_pending_load') return {label:'上次写入成功 · 加载待确认',detail:'已记录来源规则写入；尚未验证会话加载或产物来源。',ignored};
    if(event?.status==='rule_present_pending_load') return {label:'发现来源规则 · 加载待确认',detail:'只核对到规则存在，不代表已确认是谁写入或会话已加载。',ignored};
    if(candidate.status==='rule_present') return {label:'发现来源规则',detail:'还需核对工具的实际入口，尚未验证产物来源。',ignored};
    return {label:'待接入',detail:'让这个 AI 在新增和修改文件时标记来源。',ignored};
}

export function enrollmentReason(reason?:string) {
    const labels:Record<string,string>={
        adapter_requires_manual_setup:'这个工具的接入入口尚未核实，暂时不能自动写入。',
        existing_rule_needs_review:'已有 WorkShelf 规则不完整或被修改，请先核对原文件。',
        profile_missing:'请先启动一次这个 AI，建立它自己的配置目录。',
        codex_instruction_budget:'规则文件已达到读取预算，请先整理原规则。',
    };
    return labels[reason||'']||'当前规则入口不能安全追加，请核对入口和文件状态后再试。';
}
