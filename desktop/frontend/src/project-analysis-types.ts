export type SubscriptionRecipient={schema:number;proofID:string;agent:string;accountID:string;accountLabel:string;modelID:string;endpoint:string;wireModel:string};
export type Recipient={providerName:string;modelName:string;modelID:string;protocol:string;baseURL:string;subscription?:SubscriptionRecipient};
export type ReportedUsage={openRouterDebit?:{amount:string;byok?:boolean}};

// Keep provider credits distinct from a bill or a price guarantee. Missing
// receipts (including historical ones) are never displayed as zero cost.
export function debitDescription(usage?:ReportedUsage,status?:string):string {
    if(status==='queued')return '尚未发送';
    if(status==='running')return '等待扣费回执';
    const debit=usage?.openRouterDebit;
    if(!debit)return '金额未返回，请到服务商查看账单';
    return `OpenRouter 回报扣费：${debit.amount} credits。${debit.byok===true?'使用自带密钥，上游费用另计。':debit.byok===undefined?'上游计费方式未返回。':''}这不是完整账单或金额上限。`;
}

// Display only the evidence recorded with this request. Historical logical
// subscription URLs must never be presented as verified sending addresses.
export function recipientRows(r:Recipient):[string,string][] {
    const rows:[string,string][]=[['接收方',r.providerName],['模型',r.modelName||r.modelID]];
    if(r.protocol==='subscription'){
        if(!r.subscription)return [...rows,['发送地址','这条旧记录未保存实际地址']];
        rows.push(['账号',r.subscription.accountLabel],['发送地址',r.subscription.endpoint]);
        const selected=r.modelID.slice(r.modelID.indexOf('/')+1);
        if(r.subscription.wireModel!==selected)rows.push(['实际模型',r.subscription.wireModel]);
    }else rows.push(['发送地址',r.baseURL]);
    return rows;
}
export type Quota={requests:number;inputBytes:number;outputTokens:number};
export type Budget={limit:Quota;reserved:Quota;recordsRemaining:number;costStatus:string};
export type Packet={sha256:string;body:{projectID:string;projectName:string;memoryRevision:number;observedAt:string;observationComplete:boolean;folders:{id:string;path:string}[];files:{id:string;position:{sourceID:string;relative:string};kind:string;status:string}[];notes:{id:string;kind:string;text:string}[];excluded:Record<string,number>}};
export type Advice={groups:{folderID:string;fileIDs:string[];reason:string}[];uncertainIDs:string[]};
export type AnalysisPreview={token:string;expiresAt:string;recipient:Recipient;packet:Packet;system:string;input:string;maxOutputTokens:number;budget:Budget};
export type AnalysisItem={archivedAt?:string;id:string;status:string;startedAt:string;finishedAt:string;recipient:Recipient;noteSaved:boolean;problem:string};
export type AnalysisRecord={id:string;status:string;startedAt:string;deadlineAt:string;problem?:string;noteSaved:boolean;advice?:Advice;usage?:ReportedUsage;spec:{recipient:Recipient;request:{system:string;input:string;maxOutputTokens:number}}};
export type AnalysisHistory={revision:number;archived?:number;budgetProblem?:string;items:AnalysisItem[];total:number;budget:Budget};
export type AnalysisScopePage={sha:string;projectName:string;id:string;breadcrumbs:{id:string;name:string}[];rows:{id:string;name:string;kind:'directory'|'group';files:number;canPreview:boolean}[];offset:number;total:number;totalFiles:number;files:number;canPreview:boolean;excluded:Record<string,number>;observedAt:string;observationComplete:boolean};
export const analysisStatus=(r:Pick<AnalysisRecord,'status'|'deadlineAt'>)=>r.status==='running'&&Date.parse(r.deadlineAt)<=Date.now()?'unknown':r.status;
export const analysisLabels:Record<string,string>={running:'正在分析',succeeded:'建议已生成',failed:'未能完成',cancelled:'已停止',stale:'项目记录已变化',unknown:'结果尚未确定'};

// Only request identity is stored in the WebView. The actual payload and receipt
// remain in the project's durable backend ledger; recovery never resends.
export class AnalysisJournal {
    private name:string;
    constructor(key:string,private storage:Storage=localStorage){this.name='workshelf.analysis-request.v1:'+encodeURIComponent(key)}
    load():string|undefined {const id=this.storage.getItem(this.name);if(id===null)return;if(!/^[a-zA-Z0-9_-]{1,128}$/.test(id))throw new Error('上次分析编号无法读取，请保留记录后重新打开应用。');return id}
    save(id:string){const old=this.load();if(old&&old!==id)throw new Error('请先核对上次分析。');if(!/^[a-zA-Z0-9_-]{1,128}$/.test(id))throw new Error('分析编号无效');this.storage.setItem(this.name,id);if(this.load()!==id)throw new Error('未能保存分析编号，本次没有发送。')}
    clear(id:string){if(this.load()===id)this.storage.removeItem(this.name)}
}
