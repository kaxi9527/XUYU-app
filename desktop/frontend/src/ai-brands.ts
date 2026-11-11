import * as catalog from './ai-icon-catalog.json';

export const aiIconCount=catalog.icons.length;
const icons=new Set(catalog.icons);
const aliases:Record<string,string>={chatgpt:'openai',gpt:'openai',anthropic:'anthropic',claudecode:'claudecode',claudecli:'claudecode',geminicli:'gemini',googlegemini:'gemini',githubcopilot:'githubcopilot',copilot:'githubcopilot',codeium:'windsurf',moonshot:'moonshot',通义:'qwen',通义千问:'qwen',qwencode:'qwen',豆包:'doubao',智谱:'zai',glm:'zai',文心一言:'wenxin',腾讯元宝:'yuanbao'};
export const popularAI=['codex','claudecode','cursor','gemini','githubcopilot','windsurf','trae','codebuddy','workbuddy','qoder','cline','opencode','antigravity','openai','deepseek','qwen','kimi','grok','doubao','zai'];
export function aiBrand(name:string){
 const normalized=name.toLowerCase().replace(/[\s_.\-/]+/g,'').replace(/订阅$/,'');
 let slug=({chatgptcodex:'codex',geminicodeassist:'gemini'} as Record<string,string>)[normalized]||aliases[normalized]||normalized;
 if(!icons.has(slug)){
  const model=normalized.match(/^(chatgpt|gpt|codex|claude|gemini|deepseek|qwen|kimi|grok|glm)(?:\d|$)/)?.[1];
  if(model)slug=aliases[model]||model;
 }
 return {name:name||'未知来源',slug:icons.has(slug)?slug:'',letter:Array.from(name.trim())[0]?.toUpperCase()||'?'};
}
export function aiBrandMark(name:string,esc:(v:unknown)=>string){
 const b=aiBrand(name);
 return b.slug?`<span class="ai-logo icon" aria-hidden="true" style="--logo:url('/vendor/lobe-icons-${catalog.version}/${b.slug}.svg');--brand:currentColor"></span>`:`<span class="ai-logo letter" aria-hidden="true">${esc(b.letter)}</span>`;
}
