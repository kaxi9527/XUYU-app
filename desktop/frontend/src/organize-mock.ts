// Development-only UI fixtures, loaded through the existing ?mock= gate.
import type {Profile} from "./organize-settings";
import type {PreviewFile} from "./organize-structure";
const root = "/Users/demo/Work";
const sources = ["/Users/demo/Desktop/品牌改版", "/Users/demo/Downloads/品牌改版-交付稿.png"];
const folders = ["需求", "参考素材", "源文件", "过程稿", "交付稿"];
let contextReads = 0;
let previewRows:PreviewFile[]=[];
let cancelPreview:(()=>void)|undefined;
let stopExecution:(()=>void)|undefined;
let executionStopped=false;
const files = [
    ["需求说明.md", "需求", true], ["参考图.png", "参考素材", true],
    ["首页.fig", "源文件", true], ["image.png", "参考素材", true],
] as const;

export async function organizeMock(method: string, args: unknown[], scenario: string): Promise<unknown> {
    if(method==='OrganizeStop'){if(!stopExecution)return false;executionStopped=true;stopExecution();return true}
    if(method==='OrganizeCancelPreview'){cancelPreview?.();return true}
    if(method === "OrganizeChooseSources") return scenario==='picker-cancel' ? [] : sources;
    if(method === "OrganizeSuggest") {
        if(scenario==='suggest-error')throw new Error('来源暂时无法读取，请重新选择');
        return {project:'品牌改版',inferred:scenario==='unknown'?[]:[{label:'设计·UI原型',reason:'包含 Figma 原型源文件',confidence:'较有把握',preConfirm:true,alreadyInReadme:false}]};
    }
    if(method === "OrganizeSaveTemplate") return null;
    if(method === "OrganizeBrowse") {
        const path=String(args[0]||"/Users/demo");
        return {path,parent:path==="/Users/demo"?"":"/Users/demo",hiddenCount:1,truncated:false,entries:[
            ...(args[1]?[{name:".codex",path:path+"/.codex",directory:true,hidden:true,link:false,size:0}]:[]),
            {name:"品牌改版",path:path+"/品牌改版",directory:true,hidden:false,link:false,size:0},
            {name:"需求.md",path:path+"/需求.md",directory:false,hidden:false,link:false,size:120},
        ]};
    }
    if (method === 'CreateProject') {
        const input=args[0] as {name:string;root:string};
        if(!input.name)throw new Error('请填写项目名称');
        return {name:input.name,path:input.root+'/'+input.name};
    }
    if (method === "OrganizeContext") {
        contextReads++;
        const discovering=['find-related','scan-partial','scan-overflow','scan-running'].includes(scenario)&&contextReads>1;
        const found=discovering?[...sources,...(scenario==='scan-overflow'?Array.from({length:70},(_,i)=>`/Users/demo/Documents/品牌改版-${i}.png`):['/Users/demo/Documents/品牌改版-需求.md','/Users/demo/Downloads/品牌改版-素材.zip'])]:sources;
        return {
        project: args[0] ? "品牌改版" : "", root, sources: args[0] ? found : [], warnings: discovering&&scenario==='scan-partial'?['文稿目录暂时无法访问，本次查找可能不完整。']:[],
        projects: scenario === "empty" ? [] : [{key: "place:design", name: "品牌改版"}],
        inferred: args[0] ? [{label: "设计·UI原型", confidence: "较有把握", reason: "包含 Figma 原型源文件", preConfirm: true, alreadyInReadme: false}] : [],
        workTypes: [
            {label: "设计·UI原型", layout: {name: "设计", folders, roles:{brief:"需求",reference:"参考素材",source:"源文件",draft:"过程稿",delivery:"交付稿"}}},
            {label: "开发·功能开发", layout: {name: "开发", folders: ["需求文档", "参考资料", "代码工程", "设计与测试", "交付说明"]}},
            {label: "视频剪辑·剪辑成片", layout: {name: "视频", folders: ["需求与脚本", "原始素材", "剪辑工程", "字幕", "音频", "预览稿", "成片"]}},
        ],
        };
    }
    if (method === "OrganizePreview") {
        const req = args[0] as {project: string; workTypes:string[]; folders: Record<string, string>; profile: Profile};
        if(scenario==='settings-multi'&&(req.workTypes.join('|')!=='设计·UI原型|开发·功能开发'||req.profile.folders.join('|')!=='资料|工作文件|交付'||req.profile.rules.length!==0))throw new Error('设置验收：预览没有收到多工种和明确接受的新模板');
        if(scenario==='merge-cancel')return new Promise((_,reject)=>{const timer=setTimeout(()=>reject(new Error('比较超时')),10000);cancelPreview=()=>{clearTimeout(timer);reject(new Error('已取消'))}});
        if(scenario.startsWith('merge-execution')||scenario==='merge-duplicates-only'){
            const target=`${root}/${req.project}`,bulk=scenario==='merge-execution-bulk',only=scenario==='merge-duplicates-only';
            const row=(id:string,from:string,name:string,extra:Partial<PreviewFile>={}):PreviewFile=>({id,source:`/Users/demo/${from}/素材/${name}`,folder:'参考素材/素材',target:`${target}/参考素材/素材/${name}`,selected:true,blocked:false,directory:false,crossDevice:false,reason:'合并相同目录并保留层级',...extra});
            const primary=row('primary','Desktop','logo.png');
            previewRows=only?[]:[primary,row('version-a','Desktop','cover.png',{versionGroup:'cover',operation:'version'}),row('version-b','Downloads','cover.png',{versionGroup:'cover',operation:'version',target:`${target}/参考素材/素材/cover（来自下载）.png`}),row('nested','Downloads','icon.png',{folder:'参考素材/素材/图标',target:`${target}/参考素材/素材/图标/icon.png`})];
            for(let i=0;i<(bulk?2400:1);i++)previewRows.push(row('duplicate-'+i,'Downloads/'+i,'logo.png',{operation:'duplicate',blocked:true,selected:false,representative:only?primary.target:primary.source}));
            if(bulk)for(let i=0;i<2400;i++)previewRows.push(row('version-'+i,'Documents',`cover-v${i}.png`,{versionGroup:'cover',operation:'version'}));
            return {token:'fixture-preview',project:req.project,target,folders:req.profile.folders,merge:true,mergedFolders:1,warnings:[],rows:previewRows};
        }
        const rows:PreviewFile[] = files.map(([name, defaultFolder, selected], i) => {
            const source = sources[0] + "/" + name;
            const rule=req.profile.rules.find(r=>r.kind==='name'?name.includes(r.value):r.kind==='path'?source.includes(r.value):r.value.split(',').some(x=>name.endsWith('.'+x.trim().replace(/^\./,''))));
            const folder = req.folders[source] || rule?.folder || defaultFolder;
            return {id: String(i), source, folder, target: `${root}/${req.project}/${folder}/${name}`, selected: !!req.folders[source] || !!rule || selected, blocked: false, directory: false, crossDevice: false, reason: rule?'匹配自定义规则':selected ? "按所选工种和文件用途归类" : "图片用途待确认"};
        });
        rows.push({id: "4", source: sources[1], folder: "交付稿", target: `${root}/${req.project}/交付稿/品牌改版-交付稿.png`, selected: true, blocked: false, directory: false, crossDevice: false, reason: "文件名包含交付用途"});
        const logs=scenario==='logs'?Array.from({length:45},(_,i)=>({id:'log-'+i,source:sources[0]+`/desktop-test-final-${i}.log`,folder:req.profile.roles.draft||'过程稿',target:`${root}/${req.project}/过程稿/desktop-test-final-${i}.log`,selected:false,blocked:false,directory:false,crossDevice:false,keepOriginal:true,reason:'运行日志默认保留原处，可自行选择是否归档'})):[];
        const bulk:PreviewFile[]=['group-review','bulk-auto','single-review','result-bulk-failed'].includes(scenario)?Array.from({length:scenario==='single-review'?1:2267},(_,i)=>{
            const source=sources[0]+`/资料-${i}.png`,folder=req.folders[source]||(['bulk-auto','result-bulk-failed'].includes(scenario)?'参考素材':'');
            return {id:'bulk-'+i,source,folder,target:folder?`${root}/${req.project}/${folder}/资料-${i}.png`:undefined,selected:!!folder,blocked:false,directory:false,crossDevice:false,reason:'自定义模板尚未指定这类文件的归档目录'};
        }):[];
        const existing:PreviewFile[]=scenario==='existing'?['screenshots','文档归档','组件规范'].map((folder,i)=>({id:'existing-'+i,source:`${root}/${req.project}/${folder}`,target:`${root}/${req.project}/${folder}`,folder,selected:false,blocked:true,directory:true,crossDevice:false,existing:true,reason:'已在项目中，保留现有目录和内部文件，不重复整理'})):[];
        if(scenario==='result-folder')rows.push({id:'whole-folder',source:sources[0]+'/组件库',target:`${root}/${req.project}/源文件/组件库`,folder:'源文件',selected:true,blocked:false,directory:true,crossDevice:false,reason:'文件夹整体归档'});
        previewRows=scenario==='empty'?[]:[...rows,...logs,...bulk,...existing];
        return {token: "fixture-preview", project: req.project, target: root + "/" + req.project, folders:req.profile.folders, warnings: [], rows: previewRows};
    }
    if (method === "OrganizeApply") {
        executionStopped=false;
        if(scenario.startsWith('organize-stop'))await new Promise<void>(resolve=>{const timer=setTimeout(()=>{stopExecution=undefined;resolve()},30000);stopExecution=()=>{clearTimeout(timer);stopExecution=undefined;resolve()}});
        if (scenario === "stale") throw new Error("源文件在预览后已变化，需重新预览");
        const selected=new Set(args[1] as string[]);
        const items=previewRows.filter(row=>selected.has(row.id)).map((row,index)=>{
            const error=scenario==='result-failed'||scenario==='result-bulk-failed'&&index>=5||scenario==='result-partial'&&index===0?'目标位置已被占用，请重新预览':undefined;
            return {source:row.source,target:row.target!,action:executionStopped&&(index>0||scenario==='organize-stop-before')?'未开始':error?'失败':scenario==='result-copy'||scenario==='result-mixed'&&index===0?'复制':'搬走',error};
        });
        const selectedSources=new Set(previewRows.filter(row=>selected.has(row.id)).map(row=>row.source));
        const duplicates=previewRows.filter(row=>row.operation==='duplicate'&&(row.representative===row.target||selectedSources.has(row.representative||'')));
        items.push(...duplicates.map(row=>({source:row.source,target:row.target!,action:'重复保留',error:undefined})));
        return {moved:items.filter(row=>row.action==='搬走').length,copied:items.filter(row=>row.action==='复制').length,failed:items.filter(row=>row.error).length,stopped:items.filter(row=>row.action==='未开始').length,duplicates:duplicates.length,undoPath:items.some(row=>row.action==='搬走')?root+'/_总控/整理撤销/undo-organize-fixture.json':undefined,items:scenario==='result-missing'?[]:items,warnings:[]};
    }
    throw new Error("未知整理夹具方法：" + method);
}
