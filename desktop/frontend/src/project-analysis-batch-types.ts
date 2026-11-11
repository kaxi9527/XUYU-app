import type {Recipient,Quota,Budget,ReportedUsage} from './project-analysis-types';

export type BatchSpec={previewID:string;planSHA:string;scopeSHA:string;scopeID:string;scopeName:string;ledgerRevision:number;previousQuota:Quota;quota:Quota;quotaChangeAccepted:boolean;requests:{recipient:Recipient;request:{system:string;input:string;maxOutputTokens:number}}[]};
export type BatchPreview={token:string;expiresAt:string;spec:BatchSpec;totalFiles:number;budget:Budget;required:Quota};
export type BatchItem={archivedAt?:string;id:string;specSHA:string;scopeName:string;status:'active'|'paused'|'completed'|'stopped';problem:string;createdAt:string;expiresAt:string;completed:number;total:number;running:number;totalFiles:number;analyzedFiles:number;noteSaved:boolean;canResume:boolean;recipient:Recipient};
export type BatchDetail={batch:{id:string;spec:BatchSpec;items:{status:string;advice?:unknown;usage?:ReportedUsage}[]};item:BatchItem;summary:{groups:{folderID:string;fileIDs:string[];reasons:string[]}[];uncertainIDs:string[];unanalyzedIDs:string[];analyzedFiles:number;totalFiles:number}};
export type BatchHistory={revision:number;archived?:number;items:BatchItem[];total:number};
export const batchLabels:Record<string,string>={active:'正在分析',paused:'已暂停',completed:'分析完成',stopped:'分析已结束'};
export const batchProblems:Record<string,string>={cancelled:'已结束，后续文件没有发送。',connection_changed:'模型连接已变化，后续文件没有发送。',project_changed:'项目记录或目录规则已变化，后续文件没有发送。',unreliable_advice:'有一组建议无法核实，已停止后续分析。',request_failed:'有一组调用失败，已停止后续分析。',unknown_result:'有一组请求的结果尚未确定，没有自动重试。',expired:'本次授权已过期，后续文件没有发送。'};
