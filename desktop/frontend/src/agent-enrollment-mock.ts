// Only imported through the development-only mock bridge. No user files.
import type {AgentState,AgentReceipt} from './agent-enrollment-types';

export function createAgentEnrollmentMock(scenario:string) {
    let receipt:AgentReceipt={revision:scenario==='empty'?0:1,nextDue:new Date(Date.now()+86400000).toISOString(),scan:{startedAt:new Date().toISOString(),gaps:[]},records:scenario==='empty'?[]:[
        {id:'codex',name:'Codex',kind:'tool_candidate',status:'needs_rule',evidence:[{kind:'directory',status:'observed',path:'/Users/demo/.codex'}]},
        {id:'claude',name:'Claude',kind:'tool_candidate',status:'rule_present',evidence:[{kind:'rule',status:'rule_present',path:'/Users/demo/.claude/CLAUDE.md'}]},
        {id:'cursor',name:'Cursor',kind:'tool_candidate',status:'needs_rule',evidence:[{kind:'application',status:'observed',path:'/Applications/Cursor.app'}]},
        {id:'custom-rule',name:'自定义工具规则',kind:'rule_candidate',status:'review_needed',evidence:[{kind:'rule',status:'observed',path:'/Users/demo/.custom-agent/给团队长期使用的自定义工作规范与文件来源记录/AGENTS.md'}]},
    ]};
    const clone=()=>JSON.parse(JSON.stringify(receipt));
    const enroll=(agent:string)=>{
        receipt.enrollments=(receipt.enrollments||[]).filter(e=>e.intent.agent!==agent);
        receipt.enrollments.push({intent:{agent,path:`/Users/demo/.${agent}/AGENTS.md`},status:'rule_written_pending_load'});
        receipt.revision+=2;
    };
    return (method:string,args:unknown[]):unknown=>{
        switch(method) {
            case 'AgentEnrollmentStatus':return {supported:true,automaticAgents:['codex','claude','gemini'],backgroundRunning:true,receipt:clone()} as AgentState;
            case 'CheckAgentDiscovery':receipt.revision++;receipt.scan.startedAt=new Date().toISOString();return {ran:true,receipt:clone()};
            case 'PreviewAgentEnrollment':{
                const agent=String(args[0]),present=agent==='claude'||receipt.enrollments?.some(e=>e.intent.agent===agent);
                return {revision:receipt.revision,preview:{agent,path:`/Users/demo/.${agent}/AGENTS.md`,status:present?'already_present':'create',instruction:'由 '+agent+' 新增或修改的文件需要标记来源、项目、任务、工种和时间。',digest:`fixture:${receipt.revision}:${agent}`}};
            }
            case 'ApplyAgentEnrollment':
                if(scenario==='stale'||args[0]!==receipt.revision||args[2]!==`fixture:${receipt.revision}:${args[1]}`)throw Error('Agent 接入设置已变化，请重新读取');
                enroll(String(args[1]));return clone();
            case 'SetAgentEnrollmentPolicy':{
                if(args[0]!==receipt.revision)throw Error('Agent 接入设置已变化，请重新读取');
                receipt.policy={version:(receipt.policy?.version||0)+1,enabled:!!args[1],grants:(args[2] as string[]).map(agent=>({agent,ruleSHA:'fixture'})),ignored:args[3] as string[]};receipt.revision++;
                if(receipt.policy.enabled)for(const g of receipt.policy.grants)if(receipt.records.some(c=>c.id===g.agent)&&!receipt.policy.ignored.includes(g.agent))enroll(g.agent);
                return clone();
            }
        }
        throw Error('Unknown agent fixture method');
    };
}
