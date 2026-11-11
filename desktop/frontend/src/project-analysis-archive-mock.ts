import type {AnalysisRecord,Packet} from './project-analysis-types';
import type {BatchItem,BatchSpec} from './project-analysis-batch-types';

// Archive rendering fixtures; the Go acceptance service independently tests
// actual ledger compaction, identity preservation and network call counts.
export function createAnalysisArchiveMock(scenario:string){
    let changed=false;
    const recipient={providerName:'我的模型服务',modelName:'项目整理模型',modelID:'file-model',protocol:'chat',baseURL:'https://models.example.invalid/v1'};
    const quota={requests:10,inputBytes:655360,outputTokens:40960};
    const packet:Packet={sha256:'archive-context',body:{projectID:'archive-project',projectName:'品牌更新',memoryRevision:4,observedAt:'2026-10-01T10:00:00Z',observationComplete:true,folders:[{id:'requirements',path:'需求文档'}],files:[{id:'f1',position:{sourceID:'r0',relative:'需求/项目需求.md'},kind:'file',status:'present'}],notes:[{id:'n1',kind:'user_decision',text:'保留原稿和历史版本。'}],excluded:{hidden:2}}};
    const request={system:'仅按项目目录给出建议。',input:JSON.stringify(packet),maxOutputTokens:4096};
    const at=(n:number)=>new Date(Date.UTC(2026,9,30-n,10)).toISOString();
    function single(n:number):AnalysisRecord{return{id:'archive-single-'+n,status:'succeeded',startedAt:at(n),deadlineAt:at(n),noteSaved:true,advice:{groups:[{folderID:'requirements',fileIDs:['f1'],reason:'沿用该次分析时确认的工种目录。'}],uncertainIDs:[]},spec:{recipient,request}}}
    function batch(n:number):BatchItem{return{id:'archive-batch-'+n,specSHA:'archive-spec-'+n,scopeName:'品牌更新 · 第 '+(45-n)+' 次',status:'completed',problem:'',createdAt:at(n),expiresAt:at(n),completed:1,total:1,running:0,totalFiles:1,analyzedFiles:1,noteSaved:true,canResume:false,recipient,archivedAt:n>=5?at(n-1):undefined}}
    return(method:string,args:unknown[]):unknown|undefined=>{
        if(!scenario.includes('archive'))return undefined;
        if(method==='ProjectAnalysisHistoryPage'||method==='ProjectAnalysisBatchHistoryPage'){
            if(scenario.endsWith('error'))throw new Error('归档索引无法核实，原记录未修改。请检查本地记录后刷新。');
            const offset=Number(args[1]||0),expected=Number(args[2]||0);
            if(scenario.endsWith('stale')&&offset>0&&!changed){changed=true;throw new Error('分析记录已更新，请刷新记录后从第一页继续。')}
            const revision=changed?8:7;if(offset&&expected!==revision)throw new Error('分析记录已更新，请刷新记录后从第一页继续。');
            const rows=Array.from({length:45},(_,n)=>method.includes('Batch')?batch(n):{...single(n),recipient,archivedAt:n>=5?at(n-1):undefined});
            return{items:rows.slice(offset,offset+20),total:45,archived:40,revision,budget:{limit:quota,reserved:{requests:1,inputBytes:2048,outputTokens:4096},recordsRemaining:95,costStatus:'unknown'}};
        }
        const id=String(args[1]||'');if(!id.startsWith('archive-'))return undefined;
        if(scenario.endsWith('corrupt'))throw new Error('这条归档内容与记录不一致，无法读取；原记录未修改。');
        const n=Number(id.split('-').at(-1));
        if(method==='ProjectAnalysisResult')return single(n);
        if(method==='ProjectAnalysisBatchResult')return batch(n);
        if(method==='ProjectAnalysisBatchDetail'){
            const spec:BatchSpec={previewID:'archive-preview',planSHA:'archive-plan',scopeSHA:'archive-scope',scopeID:'all',scopeName:batch(n).scopeName,ledgerRevision:4,previousQuota:quota,quota,quotaChangeAccepted:false,requests:[{recipient,request}]};
            return{batch:{id,spec,items:[{status:'succeeded'}]},item:batch(n),summary:{groups:[{folderID:'requirements',fileIDs:['f1'],reasons:['沿用该次分析时确认的工种目录。']}],uncertainIDs:[],unanalyzedIDs:[],analyzedFiles:1,totalFiles:1}};
        }
        return undefined;
    };
}
