// 只在开发服务器 + ?mock=1 时使用的假数据，给 UX 检查截图用。正式包里不会加载。
import {organizeMock} from "./organize-mock";
import {createAgentEnrollmentMock} from "./agent-enrollment-mock";
import {createWorkGraphMock} from './work-graph-mock';
import {createProjectMemoryMock} from './project-memory-mock';
import {createProjectAnalysisMock} from './project-analysis-mock';
import {createProjectAnalysisBatchMock} from './project-analysis-batch-mock';
import {createAnalysisArchiveMock} from './project-analysis-archive-mock';
import {createProjectPlacementMock} from './project-placement-mock';
const H = "/Users/demo";
const now = Date.now();
const iso = (minAgo: number) => new Date(now - minAgo * 60000).toISOString();
const q = new URLSearchParams(location.search);
const scenario = q.get("mock") || "1"; // 1 正常 / empty 空状态 / error 报错 / slow 慢
const agentEnrollmentMock = createAgentEnrollmentMock(scenario);
const workGraphMock = createWorkGraphMock(scenario);
const projectMemoryMock = createProjectMemoryMock(scenario);
const projectAnalysisMock = createProjectAnalysisMock(scenario);
const projectAnalysisBatchMock = createProjectAnalysisBatchMock(scenario);
const analysisArchiveMock = createAnalysisArchiveMock(scenario);
const projectPlacementMock = createProjectPlacementMock(scenario);
let agentStatusReads = 0;

const longName = "2026年第四季度品牌升级项目-给客户的最终交付说明（第三版，已按法务意见修改）.md";
const items = [
  {name: "首页改版-交付说明.md", dir: H + "/Work/Agentour/成品/2026-11-05-首页改版", agent: "claude", conf: "入库登记", wt: "设计·网页设计", project: "Agentour", task: "2026-11-05-首页改版", status: "待批准", place: "待批准", min: 4},
  {name: "hero-banner@2x.png", dir: H + "/Work/Agentour/任务/2026-11-05-首页改版", agent: "cursor", conf: "自报", wt: "设计·网页设计", project: "Agentour", task: "2026-11-05-首页改版", status: "", place: "已归位", min: 12},
  {name: longName, dir: H + "/Projects/onesyn-website/docs/客户沟通/2026年第四季度/最终交付材料/给法务看的版本", agent: "codex", conf: "侧边记录", wt: "", project: "onesyn-website", task: "", status: "", place: "原处未归位", min: 35, inPlace: true},
  {name: "季度复盘.pdf", dir: H + "/Desktop", agent: "", conf: "来源不明", wt: "", project: "", task: "", status: "", place: "放错位置", min: 70},
  {name: "main.go", dir: H + "/Projects/figma-cli/cmd", agent: "grok", conf: "自报", wt: "开发·后端", project: "figma-cli", task: "", status: "", place: "原处未归位", min: 95, inPlace: true, modified: true},
  {name: "周报-第45周.docx", dir: H + "/Work/运营中台/成品/2026-11-04-周报", agent: "workbuddy", conf: "入库登记", wt: "运营·周报", project: "运营中台", task: "2026-11-04-周报", status: "已交", place: "已归位", min: 60 * 20},
  {name: "agentour-onesyn-login-handoff-r4.zip", dir: H + "/Projects", agent: "", conf: "来源不明", wt: "", project: "Agentour", task: "", status: "", place: "原处未归位", min: 60 * 26, inPlace: true},
  {name: "需求池.xlsx", dir: H + "/Downloads", agent: "gemini", conf: "推断", wt: "", project: "", task: "", status: "", place: "放错位置", min: 60 * 30},
].map(x => ({
  path: x.dir + "/" + x.name, name: x.name, dir: x.dir, modTime: iso(x.min), modUnix: Math.floor((now - x.min * 60000) / 1000),
  size: 1234 * (x.min + 7), agent: x.agent, confidence: x.conf, modified: !!(x as any).modified, workType: x.wt, project: x.project,
  task: x.task, status: x.status, placement: x.place, note: x.name.endsWith(".md") ? "按客户第二轮意见改了首屏文案和配色" : "",
  projectId: (x as any).inPlace ? "p-" + x.project : "", inPlace: !!(x as any).inPlace,
}));

const inplace = [
  {id: "p-Agentour", name: "Agentour", path: H + "/Projects/Agentour", members: 23, aliases: [], origin: "git@github.com:demo/agentour.git", category: "code", min: 30},
  {id: "p-onesyn-website", name: "onesyn-website", path: H + "/Projects/onesyn-website", members: 2, aliases: [], category: "code", min: 35},
  {id: "p-figma-cli", name: "figma-cli", path: H + "/Projects/figma-cli", members: 0, aliases: [], category: "code", min: 95},
  {id: "p-long", name: "落地页项目-给品牌部的长期维护版本（含历史素材）", path: H + "/Documents/落地页项目/给品牌部/长期维护版本/含历史素材与交付记录", members: 4, aliases: [H + "/Projects/company-drive"], category: "code", min: 60 * 50},
].map(p => ({...p, source: "原地", status: "还在原处，未归位", workTypes: [], tasks: 0, outputs: 0, pending: 0, modTime: iso(p.min), modUnix: 0}));

const rootProjects = [
  {source: "总文件夹", name: "Agentour", path: H + "/Work/Agentour", workTypes: ["设计·网页设计", "开发·前端"], tasks: 6, outputs: 9, pending: 2, modTime: iso(4), modUnix: 0},
  {source: "总文件夹", name: "运营中台", path: H + "/Work/运营中台", workTypes: ["运营·周报"], tasks: 3, outputs: 3, pending: 0, modTime: iso(60 * 20), modUnix: 0},
];

const out = (n: string, st: string) => ({name: n, path: H + "/Work/Agentour/成品/x/" + n, isDir: false, size: 2048, modTime: iso(5), status: st, agent: "claude"});
const detail = {
  name: "Agentour", path: H + "/Work/Agentour", readme: "# Agentour\n\n工种：设计·网页设计、开发·前端\n负责人：demo\n", workTypes: ["设计·网页设计", "开发·前端"],
  columns: [
    {workType: "设计·网页设计", template: "T03", tasks: [
      {name: "2026-11-05-首页改版", path: H + "/Work/Agentour/任务/2026-11-05-首页改版", workType: "设计·网页设计", template: "T03", status: "待批准", agent: "claude", note: "首屏和导航按新品牌规范重做", files: 14, outputs: [out("首页改版-交付说明.md", "待批准"), out("hero-banner@2x.png", "待批准")], modTime: iso(4)},
      {name: "2026-11-01-图标统一", path: H + "/Work/Agentour/任务/2026-11-01-图标统一", workType: "设计·网页设计", template: "", status: "已交", agent: "cursor", note: "", files: 31, outputs: [out("icons.zip", "已交")], modTime: iso(60 * 90)},
    ]},
    {workType: "开发·前端", template: "", tasks: [
      {name: "2026-11-03-登录页接入-给客户联调用的临时版本（不要直接上线）", path: H + "/Work/Agentour/任务/x", workType: "开发·前端", template: "", status: "进行中", agent: "codex", note: "", files: 8, outputs: [], modTime: iso(60 * 40)},
    ]},
    {workType: "未标工种", template: "", tasks: []},
  ],
  materials: [{name: "品牌规范-v3.pdf", path: H + "/Work/Agentour/资料/品牌规范-v3.pdf", isDir: false, size: 900000, modTime: iso(9000), status: "资料", agent: ""}],
  code: [{name: "agentour-web", path: H + "/Work/Agentour/代码/agentour-web", isDir: true, size: 0, modTime: iso(300), status: "", agent: ""}],
  loose: [], sensitive: 2,
};

const place = {
  project: {id: "p-Agentour", name: "Agentour", path: H + "/Projects/Agentour", category: "code", markers: [".git", "AGENTS.md", "CLAUDE.md"], origin: "git@github.com:demo/agentour.git", branch: "main",
    members: [
      {path: H + "/Projects/Agentour", kind: "主版本", isDir: true},
      {path: H + "/Downloads/Agentour 2", kind: "副本/交付包", isDir: true},
      {path: H + "/Projects/agentour-onesyn-login-handoff-20261026-css-ip-role-embedded-r5", kind: "副本/交付包", isDir: true},
      {path: H + "/Projects/agentour-onesyn-login-handoff-20261026-css-ip-role-embedded-r5.zip", kind: "压缩包", isDir: false},
      {path: H + "/Projects/Agentour组件库套母库实例-迁移流程.md", kind: "相关文件", isDir: false},
    ], aliases: [], modTime: iso(30), status: "还在原处，未归位", reasons: ["名字以项目名开头（副本、交付包、压缩包）"]},
  readme: "# Agentour\n\n多租户智能体门户。\n\n## 开发\n\npnpm i && pnpm dev\n", readmeName: "README.md",
  recent: items.filter(i => i.project === "Agentour" || i.inPlace),
};

const cands = [
  {path: H + "/.claude/CLAUDE.md", kind: "CLAUDE.md", agent: "claude", size: 1200, installed: true, scope: "全局", recommended: true},
  {path: H + "/.codex/AGENTS.md", kind: "AGENTS.md", agent: "codex", size: 800, installed: true, scope: "全局", recommended: true},
  {path: H + "/.grokbot/AGENTS.md", kind: "AGENTS.md", agent: "grok", size: 4000, installed: false, scope: "全局", recommended: true},
  {path: H + "/Projects/onesyn-website/AGENTS.md", kind: "AGENTS.md", agent: "", size: 3000, installed: false, scope: "项目", recommended: true},
  {path: H + "/Documents/落地页项目/给品牌部/长期维护版本/含历史素材与交付记录/AGENTS.md", kind: "AGENTS.md", agent: "", size: 3000, installed: false, scope: "项目", recommended: true},
];

// 1x1 以外的小图：用 canvas 画一个色块当「图片缩略图」。
function fakeImage(seed: string): string {
    const c = document.createElement("canvas");
    c.width = 160; c.height = 120;
    const g = c.getContext("2d")!;
    const h = [...seed].reduce((a, ch) => a + ch.charCodeAt(0), 0) % 360;
    const grad = g.createLinearGradient(0, 0, 160, 120);
    grad.addColorStop(0, `hsl(${h},70%,60%)`); grad.addColorStop(1, `hsl(${(h + 60) % 360},70%,40%)`);
    g.fillStyle = grad; g.fillRect(0, 0, 160, 120);
    g.fillStyle = "rgba(255,255,255,.85)"; g.fillRect(16, 40, 128, 14); g.fillRect(16, 62, 90, 10);
    return c.toDataURL();
}

function thumb(path: string) {
    const ext = (path.match(/\.[^./]+$/) || [""])[0].toLowerCase();
    if ([".png", ".jpg", ".pdf", ".docx"].includes(ext)) return {kind: "image", data: fakeImage(path), ext, label: ext.slice(1).toUpperCase(), group: ext === ".pdf" ? "pdf" : ext === ".docx" ? "doc" : "image"};
    if ([".md", ".go"].includes(ext)) return {kind: "text", lines: ext === ".go" ? ["package main", "", "import (", "\t\"fmt\"", ")", "", "func main() {"] : ["# 首页改版交付说明", "", "- 首屏标题改为「一站式智能体门户」", "- 主色从 #2F6BFF 调整为品牌蓝", "- 导航增加「解决方案」入口"], ext, label: ext.slice(1).toUpperCase(), group: ext === ".go" ? "code" : "text"};
    const group = ext === ".zip" ? "archive" : ext === ".xlsx" ? "sheet" : "file";
    return {kind: "icon", ext, label: ext.slice(1).toUpperCase() || "文件", group};
}

let settings = {configPath: H + "/.config/workshelf/config.json", configExists: false, root: H + "/Work", scanPaths: [H + "/Desktop", H + "/Downloads", H + "/Documents", H + "/Projects", H], defaults: [H + "/Desktop", H + "/Downloads", H + "/Documents", H + "/Projects", H], homeDepth: 1, registryDir: H + "/Work/_总控/原地登记"};

const sleep = (ms: number) => new Promise(r => setTimeout(r, ms));
let discoveryScanPolls=0;
let fileSearches=0;
export const fileActionCalls:{method:string;args:unknown[]}[]=[];
const deletePreviews=new Map<string,{paths:string[];hiddenCount:number}>();
let deletePreviewSequence=0;
export const mergeActionCalls:{method:string;args:unknown[]}[]=[];
let mergeContextReads=0;

let backgroundState={supported:true,enabled:false,startAtLogin:false,loginAvailable:true,loginProblem:'',message:'仅检查已授权的项目；每 5 分钟检查，重新运行后补扫。'};
let autoState:{policy:{enabled:boolean;observeSessions?:boolean;objectionEnabledAt?:string;roots:string[];sources?:{adapter:string;path:string}[]};projectRoot?:string;model?:{enabled:boolean;scopeValid:boolean;provider?:string;model?:string};objectionModel?:{enabled:boolean;scopeValid:boolean;provider?:string;model?:string};suggestedRoots:string[];workTypes:string[];candidates:unknown[];unassigned:unknown[];operations:any[];sessionFileCount?:number}={policy:{enabled:scenario==='auto-unknown'||scenario==='auto-unassigned',observeSessions:scenario==='auto-preview',roots:[]},projectRoot:H+'/Work',suggestedRoots:[H+'/Desktop',H+'/Downloads'],workTypes:['设计·UI原型','开发·功能开发'],candidates:scenario==='auto-unknown'?[{project:'文档项目',files:[H+'/Desktop/说明.md'],reason:'尚未识别工种'}]:scenario==='auto-preview'?[{project:'品牌改版',workType:'设计·UI原型',files:Array.from({length:12},(_,i)=>H+'/Desktop/品牌改版/设计稿-'+(i+1)+'.png')},{project:'网站开发',workType:'开发·功能开发',targetPath:H+'/Projects/网站开发',files:[H+'/Downloads/网站说明.md',H+'/Documents/接口说明.pdf']}]:[],unassigned:scenario==='auto-unassigned'?[{id:'fixture-session',agent:'codex',session:'session-1',count:2,examples:[H+'/Desktop/图一.png',H+'/Desktop/图二.png']}]:scenario==='auto-preview'?[{id:'preview-session',agent:'codex',session:'session-2',count:1,examples:[H+'/Desktop/草稿.md']}]:[],operations:[],sessionFileCount:scenario==='auto-preview'?15:0};
export async function mockCall(method: string, args: unknown[]): Promise<any> {
    if(method==='AutoOrganizeStatus')return structuredClone(autoState);
    if(method==='PreviewAutoModel')return {provider:'模拟服务商',model:'模拟模型',endpoint:'https://example.invalid',recipientSHA:'fixture-recipient',scopeSHA:'fixture-scope',uncappedOutput:scenario==='codex-consent'};
    if(method==='SetAutoModel'){autoState.model={enabled:!!args[0],scopeValid:true,provider:'模拟服务商',model:'模拟模型'};return structuredClone(autoState)}
    if(method==='SetAutoOrganize'){autoState.policy={enabled:!!args[0],roots:args[1] as string[],sources:args[2] as {adapter:string;path:string}[]};return structuredClone(autoState)}
    if(method==='SetAutoObjections'){autoState.policy.objectionEnabledAt=args[0]?new Date().toISOString():undefined;return structuredClone(autoState)}
    if(method==='PreviewAutoObjectionModel')return {provider:'模拟服务商',model:'模拟模型',endpoint:'https://example.invalid',recipientSHA:'fixture-recipient',scopeSHA:'fixture-scope',uncappedOutput:scenario==='codex-consent'};
    if(method==='SetAutoObjectionModel'){autoState.objectionModel={enabled:!!args[0],scopeValid:true,provider:'模拟服务商',model:'模拟模型'};return structuredClone(autoState)}
    if(method==='RunAutoOrganize'){if(autoState.policy.enabled&&!autoState.operations.length)autoState.operations.push({id:'a'.repeat(32),project:'品牌改版',status:'organized',target:H+'/Work/品牌改版',undoPath:'/fixture/undo.json',summary:{moved:2,copied:0,failed:0}});return null}
    if(method==='ConfirmAutoProjectTrade'){autoState.candidates=[];autoState.operations.push({id:'b'.repeat(32),project:String(args[0]),status:'organized',target:H+'/Work/'+String(args[0]),undoPath:'/fixture/undo.json',summary:{moved:1,copied:0,failed:0}});return structuredClone(autoState)}
    if(method==='ConfirmAutoSession'){autoState.unassigned=[];autoState.operations.push({id:'c'.repeat(32),project:String(args[1]),status:'organized',target:H+'/Work/'+String(args[1]),undoPath:'/fixture/undo.json',summary:{moved:2,copied:0,failed:0}});return structuredClone(autoState)}
    if(method==='RejectAutoOrganize'){const operation=autoState.operations.find(o=>o.id===args[0]);if(operation){operation.status='restored';operation.restore={restored:2,failed:0,skipped:0}}return structuredClone(operation)}
    if(method==='BackgroundStatus')return {...backgroundState,...(scenario==='background-uninstalled'?{loginAvailable:false,loginProblem:'安装到应用程序文件夹后，可开启登录启动。'}:{})};
    if(method==='SetBackgroundSettings'){
        if(scenario==='background-failed')throw new Error('登录启动项已变化，未作修改');
        backgroundState={...backgroundState,enabled:!!args[0],startAtLogin:!!args[1]};return {...backgroundState};
    }
    const modelResult=modelCenterMock(method,args,scenario);if(modelResult!==undefined)return modelResult;
    if(scenario==='agent-retry'&&method==='AgentEnrollmentStatus'&&++agentStatusReads===1)throw new Error('接入记录暂时无法读取');
    if(scenario==='agent-delay'&&method==='PreviewAgentEnrollment')await sleep(800);
    if(scenario==='graph-race'&&method==='FileGraph')await sleep(args[0]==='Agentour'?800:100);
    if(scenario==='graph-source-error'&&method==='AgentEnrollmentStatus')throw new Error('接入回执暂时无法读取');
    if(['PreviewMerge','ApplyMerge','OrganizeContext','OrganizeSuggest','OrganizePreview','OrganizeApply'].includes(method))mergeActionCalls.push({method,args});
    await sleep(scenario === "slow" ? 60000 : 120);
    if(scenario==='merge-rules-retry'&&method==='OrganizeContext'&&++mergeContextReads===1)throw new Error('项目暂时没有读出来，请重新读取');
    if(['Reveal','PreviewDelete','ApplyDeletePlan','ApplyDeleteConfirmed'].includes(method))fileActionCalls.push({method,args});
    if(scenario==='file-race'&&method==='Search')await sleep(args[0]==='首页'?800:100);
    if(scenario==='file-load-error'&&method==='Search'&&++fileSearches===1)throw new Error('暂时无法读取文件，请重试');
    if(scenario==='file-navigation-delay'&&['Recent','Project','PlaceProject'].includes(method))await sleep(800);
    if (scenario === "error" && method !== "Info") throw new Error("读取失败：总文件夹 /Users/demo/Work 打不开（权限不够）");
    const empty = scenario === "empty";
    if(method==='RegistryRefresh'&&scenario==='scan-error')throw new Error('文稿目录暂时无法访问，稍后可以重试。');
    if(method==='RegistryRefresh'&&scenario==='scan-delayed')await sleep(800);
    if(method==='RegistryRefresh'&&scenario==='scan-running')return {exists:true,running:true};
    if(method==='RegistryStatus'&&scenario==='scan-running')return {exists:true,running:++discoveryScanPolls<2};
    if (method.startsWith("Organize")||method==='CreateProject') return organizeMock(method, args, scenario);
    if(method.includes('ProjectPlacement'))return projectPlacementMock(method,args);
    if(method.startsWith('ProjectMemory')||method==='ApplyProjectMemoryAction')return projectMemoryMock(method,args);
    if(method.includes('ProjectAnalysis')){const archived=analysisArchiveMock(method,args);if(archived!==undefined)return archived;}
    if(method.includes('AnalysisBatch')||method==='ProjectAnalysisScopes'&&scenario.startsWith('analysis-batch'))return projectAnalysisBatchMock(method,args);
    if(method.includes('ProjectAnalysis'))return projectAnalysisMock(method,args);
    if(['WorkGraph','ProjectWorks','WorkVersions','VersionDetail','VersionFile'].includes(method))return workGraphMock(method,args);
    switch (method) {
        case "Info": return {version: "0.9.7-local", os: "darwin", arch: "arm64", home:H, root: H + "/Work", config: settings.configPath, hasRoot: true, timezone: "America/Los_Angeles"};
        case "Recent": return empty ? [] : scenario==='file-hidden'?[...items,{...items[0],name:'config.toml',path:H+'/Work/.codex/config.toml',dir:H+'/Work/.codex'}]:items;
        case "Search": return empty ? [] : items.filter(i => (i.path + i.agent + i.project).toLowerCase().includes(String(args[0]).toLowerCase()));
        case "Projects": return empty ? [] : scenario==='link-project'?[...rootProjects,{...rootProjects[0],name:'链接项目',path:H+'/Work/链接项目',linked:true,linkTarget:H+'/Documents/原项目'}]:[...rootProjects, ...inplace];
        case "Project": return scenario==='link-project'?{...detail,name:'链接项目',linked:true,linkTarget:H+'/Documents/原项目'}:detail;
        case "PlaceProject": return place;
        case "RegistryStatus": return {exists: !empty, path: settings.registryDir + "/原地项目.json", generatedAt: iso(20), projects: empty ? 0 : inplace.length, hidden: 57, running: false};
        case "RegistryRefresh": await sleep(800); return {exists: true, path: settings.registryDir + "/原地项目.json", generatedAt: iso(0), projects: inplace.length, hidden: 57, running: false};
        case "Settings": return settings;
        case "CheckFileAccess": return [{name:'桌面',status:scenario==='access-denied'?'无法访问':'可访问'},{name:'文稿',status:'可访问'},{name:'下载',status:'可访问'},{name:'Projects',status:'可访问'},{name:'主目录',status:'可访问'}];
        case "SaveSettings": return {...settings, scanPaths: args[0], configExists: true};
        case "SetRoot": {
            const root = String(args[0]).replace(/^~/, H);
            settings = {...settings, root, registryDir: root + "/_总控/原地登记", configExists: true};
            return settings;
        }
        case "PreviewHomeItems":
        case "PreviewHomeProject":
        case "PreviewHome":
            return {dryRun: true, root: settings.root, preview: [
                {source: H + "/Desktop/散落.png", target: settings.root + "/海报/资料/散落文件/散落.png", action: "搬走"},
                {source: H + "/Projects/官网", target: settings.root + "/官网", action: "搬走", whole: true},
            ], moved: 0, copied: 0, skipped: 0, failed: 0};
        case "ApplyHomeItems":
        case "ApplyHomeProject":
        case "ApplyHome":
            return {dryRun: false, root: settings.root, moved: 1, copied: 0, skipped: 0, failed: 0,
                logId: "20261105-194500", undoPath: settings.root + "/_总控/整理撤销/undo-20261105-194500.json",
                items: [{source: H + "/Desktop/散落.png", target: settings.root + "/海报/资料/散落文件/散落.png", action: "搬走", undoable: true}]};
        case "UndoHome":
            if(scenario==='organize-stop-undo')return {restored:1,unchanged:4,quarantined:0,skipped:0,failed:0};
            if(scenario==='undo-error')throw new Error('撤销记录暂时无法读取，请稍后重试');
            if(scenario==='undo-partial')return {restored:2,quarantined:0,skipped:1,failed:1,notes:['一个文件在整理后已变化']};
            return {logId: "20261105-194500", restored: String(args[0]).includes("organize-fixture") ? 4 : 1, quarantined: 0, skipped: 0, failed: 0};
        case "PreviewDelete": {
            const paths = (args[0] as string[]) || [];
            const hiddenCount=scenario==='delete-hidden-descendants'?2:paths.filter(path=>path.split('/').some(part=>part.startsWith('.')&&part!=='.'&&part!=='..')).length;
            const token=`delete-${++deletePreviewSequence}`;
            deletePreviews.set(token,{paths:[...paths],hiddenCount});
            return {token,hiddenCount,dryRun: true, root: settings.root, totalFiles: paths.length, totalBytes: paths.length * 1024,
                trashed: 0, staged: 0, skipped: 0, failed: 0,
                methodHint: "优先挪进系统废纸篓；废纸篓用不了就挪进总控「删除暂存」。",
                preview: paths.map(path => ({path, name: path.split("/").pop()!, isDir: scenario==='delete-hidden-descendants', fileCount: 1, byteSize: 1024,
                    action: "进废纸篓", label: (scenario==='delete-hidden-descendants'?"将删除文件夹 ":"将删除文件 ") + path.split("/").pop()}))};
        }
        case "ApplyDelete":
        case "ApplyDeleteConfirmed":
            throw new Error('请先预览删除，再使用本次预览确认');
        case "ApplyDeletePlan": {
            const approved=deletePreviews.get(String(args[0]));
            if(!approved)throw new Error('删除预览已失效，请重新预览');
            if(approved.hiddenCount&&!args[1])throw new Error('请确认隐藏文件删除提示');
            if(scenario==='delete-preview-stale')throw new Error('文件已变化，未执行删除，请重新预览');
            const paths=approved.paths;
            return {dryRun: false, root: settings.root, trashed: paths.length, staged: 0, skipped: 0, failed: 0,
                totalFiles: paths.length, totalBytes: 0, logId: "20261105-200000",
                undoPath: settings.root + "/_总控/整理撤销/undo-delete-20261105-200000.json",
                items: paths.map(path => ({path, dest: H + "/.Trash/" + path.split("/").pop(), action: "进废纸篓", undoable: true}))};
        }
        case "UndoDelete":
            if(scenario==='delete-undo-failed')throw new Error('原位置暂时无法访问，文件仍在废纸篓中');
            if(scenario==='delete-undo-partial')return {logId:"20261105-200000",restored:0,skipped:1,failed:0,notes:['原位置已有文件，未覆盖；删除的内容仍保留在废纸篓。']};
            return {logId: "20261105-200000", restored: 1, skipped: 0, failed: 0};
        case "InstallStatus":
            return {os: "darwin", runningPath: H + "/Work/WorkShelf/代码/WorkShelf/desktop/bin/yy-tmp/WorkShelf.app",
                preferredPath: H + "/Applications/WorkShelf.app", inApplications: scenario !== "uninstalled", needsInstall: scenario === "uninstalled",
                preferredExists: scenario !== "uninstalled", message: "请装到应用程序文件夹，授权点一次就会记住。"};
        case "InstallToApplications":
            return {os: "darwin", runningPath: H + "/Applications/WorkShelf.app", preferredPath: H + "/Applications/WorkShelf.app",
                inApplications: true, needsInstall: false, preferredExists: true, message: "已从固定位置运行"};
        case "MergeSuggest":
            if(scenario.startsWith('merge-rules'))return [
                {key:'官网',name:'官网',members:2,level:'possible',reasons:['名字去后缀后相同，尚不足以确认同一项目；请核对来源'],paths:[H+'/Work/官网',H+'/Projects/官网 副本'],sources:['总文件夹','原地']},
                {key:'品牌改版',name:'品牌改版',members:2,level:'recommended',reasons:['已核对至少 2 个非通用同名文件内容一致'],paths:[H+'/Desktop/品牌改版',H+'/Downloads/品牌改版 副本'],sources:['原地','原地']},
                {key:'工具',name:'工具',members:2,level:'conflict',reasons:['名字相近，但属于不同 Git 仓库；不推荐合并'],paths:[H+'/Projects/工具',H+'/Downloads/工具 副本'],sources:['原地','原地']},
            ];
            return empty || scenario === "projects-ui" ? [] : [{key: "官网", name: "官网", members: 2, reasons: ["名字去后缀后相同", "一个在总文件夹、一个还在原处"],
                paths: [H + "/Work/官网", H + "/Projects/官网 副本"], sources: ["总文件夹", "原地"], ids: ["p1"]}];
        case "PreviewMerge":
            return {dryRun: true, root: settings.root, preview: [
                {source: H + "/Projects/官网 副本", target: settings.root + "/官网/资料/散落文件/官网 副本", action: "搬走"}],
                moved: 0, copied: 0, skipped: 0, failed: 0};
        case "ApplyMerge":
            return {dryRun: false, root: settings.root, moved: 1, copied: 0, skipped: 0, failed: 0,
                undoPath: settings.root + "/_总控/整理撤销/undo-20261105-200100.json",
                items: [{source: H + "/Projects/官网 副本", target: settings.root + "/官网/资料/散落文件/官网 副本", action: "搬走", undoable: true}]};
        
        case "CleanupSuggest": return empty ? [] : [
            {path: H + "/Downloads/Agentour 2", name: "Agentour 2", isDir: true, category: "重复与副本", reason: "名字像副本/交付包，登记已归到项目下", evidence: "原地登记：副本/交付包", confidence: "推荐", preCheck: true, byteSize: 12000000, fileCount: 40, project: "Agentour"},
            {path: H + "/Projects/agentour-onesyn-login-handoff-r4.zip", name: "agentour-onesyn-login-handoff-r4.zip", isDir: false, category: "多余压缩包", reason: "登记里已有主版本，这个压缩包多半是交付残留", evidence: "原地登记：压缩包", confidence: "推荐", preCheck: true, byteSize: 8000000, fileCount: 1, project: "Agentour"},
            {path: H + "/Desktop/空的旧文件夹", name: "空的旧文件夹", isDir: true, category: "空文件夹", reason: "空文件夹，里面没有文件", evidence: "扫了 ~/Desktop 顶层", confidence: "推荐", preCheck: true, byteSize: 0, fileCount: 0},
            {path: H + "/Projects/官网-feature", name: "官网-feature", isDir: true, category: "旧会话与 worktree", reason: "可能是 git worktree / 另一个 clone，删前请确认不再用", evidence: "原地登记：worktree", confidence: "待你定", preCheck: false, byteSize: 2000000, fileCount: 120, project: "官网"},
            {path: H + "/Projects/demo/node_modules", name: "node_modules", isDir: true, category: "依赖与缓存", reason: "名字像依赖/缓存目录，删了可能要重新安装", evidence: "轻量目录名匹配", confidence: "待你定", preCheck: false, byteSize: 90000000, fileCount: 5000},
        ];
        case "CleanupProbeProject": return empty ? [] : [
            {path: String(args[0]) + "/old.bak", name: "old.bak", isDir: false, category: "可能用不到的文件", reason: "临时/备份文件名", evidence: "轻量文件名匹配", confidence: "待你定", preCheck: false, byteSize: 12, fileCount: 1},
        ];
        case "ProjectGraph": {
            if (empty) return {nodes: [], edges: []};
            const P = (k: string, name: string, extra: any = {}) => ({id: "project:" + k, kind: "project", name, path: k, ...extra});
            const nodes = [
                P(H+"/Work/Agentour", "Agentour", {source: "总文件夹", workTypes: ["设计·网页设计","开发·前端"], memberCount: 3, attachCount: 2}),
                P(H+"/Projects/Agentour 副本", "Agentour 副本", {source: "原地", projectId: "p1", extra: "手工副本"}),
                P(H+"/Projects/Agentour-wt-login", "Agentour-wt-login", {source: "原地", extra: "worktree"}),
                P(H+"/Projects/agentour-onesyn-login-handoff-r4", "agentour-onesyn-login-handoff-r4", {source: "原地", extra: "交付包"}),
                P(H+"/Work/海报", "海报", {source: "总文件夹", workTypes: ["设计·主图"]}),
                P(H+"/Desktop/海报-终稿 2", "海报-终稿 2", {source: "原地"}),
                P(H+"/Work/运营中台", "运营中台", {source: "总文件夹", workTypes: ["运营·周报"]}),
                P(H+"/Documents/落地页项目-给品牌部的长期维护版本（含历史素材）", "落地页项目-给品牌部的长期维护版本（含历史素材）", {source: "原地", projectId: "p-long", memberCount: 4}),
            ];
            const id = (i: number) => nodes[i].id;
            const E = (type: string, a: number, b: number, reason: string, confidence = "较有把握", weight = 1) => ({id: `${type}:${a}-${b}`, type, from: id(a), to: id(b), reason, weight, confidence});
            return {nodes, edges: [
                E("member", 0, 1, "登记组成员：手工副本", "较有把握", 0.95),
                E("member", 0, 2, "登记组成员：worktree"),
                E("member", 0, 3, "登记组成员：交付包", "较有把握", 0.7),
                E("member", 1, 2, "同属登记组「Agentour」的另一份（副本/worktree 之间）", "大致判断", 0.6),
                E("member", 1, 3, "同属登记组「Agentour」的另一份（副本/worktree 之间）", "大致判断", 0.6),
                E("member", 2, 3, "同属登记组「Agentour」的另一份（副本/worktree 之间）", "大致判断", 0.6),
                E("git", 0, 1, "同一 git 仓库（agentour.git）"),
                E("git", 0, 2, "同一 git 仓库（agentour.git）"),
                E("git", 1, 2, "同一 git 仓库（agentour.git）"),
                E("copykey", 0, 1, "合并建议：名字去后缀后相同；一个在总文件夹、一个还在原处", "大致判断", 0.9),
                E("copykey", 4, 5, "合并建议：名字去后缀后相同（海报）", "大致判断", 0.9),
                E("git", 6, 7, "同一 git 仓库（brand-site.git）"),
            ]};
        }
        case "FileGraph": {
            const project = String(args[0] || "Agentour");
            const projectKey=args[1]?'place:'+String(args[1]):'root:'+project;
            const expand = args[2] !== false && args[2] !== 0 && args[2] !== "0";
            if (empty) return {project, nodes: [], edges: [], note: "空", attachCount: 2, attachShown: 0, attachHidden: expand ? 0 : 2, skipped: expand ? "" : "另有 2 个在其他文件夹里的相关文件未展开（打开「含其他文件夹里的相关文件」可看）"};
            const T = H + "/Work/Agentour/任务/2026-11-05-首页改版/";
            const O = H + "/Work/Agentour/成品/2026-11-05-首页改版/";
            const Desk = H + "/Desktop/";
            const Dl = H + "/Downloads/";
            const F = (dir: string, name: string, size = 12000, extra = "") => ({id: "file:" + dir + name, kind: "file", name, path: dir + name, project: "Agentour", size, extra});
            const nodes:any[] = [
                F(T, "登录页.png"), F(T, "登录页 副本.png"), F(T, "登录页-v2.png"), F(T, "说明.md", 400),
                F(O, "首页改版-交付说明（给客户的最终版，已按第二轮意见改）.md", 2600), F(O, "登录页-终版.png"), F(O, "hero-banner@2x.png", 88000),
                F(H + "/Work/Agentour/资料/", "品牌色板.pdf", 52000),
            ];
            if (expand) {
                nodes.push(F(Desk, "登录页 副本.png", 12000, "项目外相关文件"));
                nodes.push(F(Dl, "Agentour-首页改版.zip", 880000, "项目外相关文件"));
            }
            const id = (i: number) => nodes[i].id;
            const E = (type: string, a: number, b: number, reason: string, confidence = "较有把握", weight = 1) => ({id: `${type}:${a}-${b}`, type, from: id(a), to: id(b), reason, weight, confidence});
            const st = "同任务目录「2026-11-05-首页改版」", so = "同成品目录「2026-11-05-首页改版」";
            const tri = (type: string, ix: number[], reason: string, c?: string, w?: number) => { const out: any[] = []; for (let i = 0; i < ix.length; i++) for (let j = i + 1; j < ix.length; j++) out.push(E(type, ix[i], ix[j], reason, c, w)); return out; };
            const edges = [
                ...tri("same_dir", [0, 1, 2, 3], st),
                ...tri("same_dir", [4, 5, 6], so),
                ...tri("dup", [0, 1, 5], "内容相同（sha256 abcdef123456…）"),
                ...tri("copyname", [0, 1, 2, 5], "文件名去版本后缀后相同（登录页）", "大致判断", 0.75),
                ...tri("activity", [3, 4, 6], "同一任务「首页改版」", "较有把握", 0.85),
                E("activity", 6, 7, "同一智能体「claude」留下的记录", "大致判断", 0.6),
            ];
            if (expand) {
                edges.push(E("dup", 0, 8, "在项目外的相关文件：内容相同（sha256 abcdef123456…）"));
                edges.push(E("copyname", 1, 8, "在项目外的相关文件：文件名去版本后缀后相同（登录页）", "大致判断", 0.75));
                edges.push(E("copyname", 0, 8, "在项目外的相关文件：文件名去版本后缀后相同（登录页）", "大致判断", 0.75));
            }
            nodes.push({id:'agent:codex',kind:'agent',name:'codex',agent:'codex',source:'来源记录',extra:'未核验作者'});
            edges.unshift({id:'source:codex',type:'source_record',from:'agent:codex',to:id(0),reason:'侧边标记记录过 codex；不证明当前内容作者或会话加载。',confidence:'来源记录',weight:0.8});
            return {
                project, projectKey, projectId: String(args[1] || ""),
                note: "从总文件夹项目切入" + (expand ? "；已展开 2 个项目外相关文件" : "") + "；1 个任务/成品文件夹不单独画点，同目录关系用文件之间的兄弟连线表示",
                skipped: expand ? "" : "另有 2 个在其他文件夹里的相关文件未展开（打开「含其他文件夹里的相关文件」可看）",
                attachCount: 2, attachShown: expand ? 2 : 0, attachHidden: expand ? 0 : 2,
                nodes, edges,
            };
        }
        case "InferWorkTypes":
            return empty ? [] : [
                {label: "设计·网页设计", source: "说明", confidence: "较有把握", reason: "写在项目说明里", template: "T03", alreadyInReadme: true, preConfirm: false},
                {label: "开发·前端", source: "说明", confidence: "较有把握", reason: "写在项目说明里", alreadyInReadme: true, preConfirm: false},
                {label: "开发·功能开发", source: "推断", confidence: "较有把握", reason: "项目里有 package.json / .git 等代码标志", template: "T09", alreadyInReadme: false, preConfirm: true},
                {label: "设计·生图出图", source: "推断", confidence: "待你定", reason: "有几张图片，但不确定是不是主出图工种", template: "T01", alreadyInReadme: false, preConfirm: false},
            ];
        case "ConfirmWorkTypes":
            return ["设计·网页设计", "开发·前端", ...((args[1] as string[]) || [])];
        case "SuggestAllocate":
            return empty ? [] : [
                {source: H + "/Work/Agentour/资料/散落文件/登录页-草稿.png", project: "Agentour", area: "任务", task: "按工种整理-网页设计", workType: "设计·网页设计", reason: "图片，项目有「设计·网页设计」，进该工种「任务」", confidence: "大致判断", preCheck: true, kind: "图片"},
                {source: H + "/Work/Agentour/资料/散落文件/util.ts", project: "Agentour", area: "代码", workType: "开发·功能开发", reason: "代码文件，进「代码」", confidence: "较有把握", preCheck: true, kind: "代码"},
                {source: H + "/Work/Agentour/杂记.xyz", project: "Agentour", area: "散落", reason: "类型拿不准，先放「资料/散落文件」，请你定", confidence: "待你定", preCheck: false, kind: "其他"},
                {source: H + "/Work/Agentour/任务/2026-11-05-首页改版/a.png", project: "Agentour", area: "任务", skip: true, skipReason: "已在「任务」里，跳过", reason: "已在「任务」里，跳过", confidence: "较有把握", preCheck: false, kind: "图片"},
            ];
        case "PreviewAllocate":
            return {dryRun: true, root: settings.root, preview: ((args[0] as any[]) || []).map(j => ({
                source: j.source, target: settings.root + "/Agentour/" + (j.area === "代码" ? "代码" : j.area === "散落" ? "资料/散落文件" : j.area + (j.task ? "/" + j.task : "")) + "/" + String(j.source).split("/").pop(),
                action: "搬走"})), moved: 0, copied: 0, skipped: 0, failed: 0};
        case "ApplyAllocate":
            return {dryRun: false, root: settings.root, moved: ((args[0] as any[]) || []).length, copied: 0, skipped: 0, failed: 0,
                undoPath: settings.root + "/_总控/整理撤销/undo-alloc-20261105-232000.json",
                items: ((args[0] as any[]) || []).map(j => ({source: j.source, target: j.source + ".moved", action: "搬走", undoable: true}))};
        case "Thumb": return thumb(String(args[0]));
        case "AgentEnrollmentStatus": case "CheckAgentDiscovery": case "PreviewAgentEnrollment": case "ApplyAgentEnrollment": case "SetAgentEnrollmentPolicy": return agentEnrollmentMock(method, args);
        case "AgentsScan": return empty ? [] : cands;
        case "AgentsBlock": return "<!-- BEGIN workshelf-identity v1 -->\n## WorkShelf：写文件要留身份标识\n…\n<!-- END workshelf-identity v1 -->";
        case "AgentsInstall": return (args[0] as any[]).map(t => ({path: t.path, action: args[1] ? "预演：会在末尾追加 1019 字节" : "追加", backup: args[1] ? "" : H + "/Work/_总控/接入备份/20261105-090000/01-x"}));
        case "AgentsUninstall": return (args[0] as string[]).map(p => ({path: p, action: "删掉 WorkShelf 段"}));
        case "Approve": return {files: ["a"], changed: []};
        case "Reveal": return null;
    }
    throw new Error("mock 没有 " + method);
}
import {modelCenterMock} from './model-center-mock';
