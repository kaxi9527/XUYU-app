import type {MemoryAction} from './project-memory-types';

export class MemoryJournal {
    private key:string;
    constructor(private project:string,private storage:Storage=localStorage){this.key='workshelf.memory-action.v1:'+encodeURIComponent(project)}
    load():MemoryAction|undefined {
        const raw=this.storage.getItem(this.key);if(!raw)return;
        const a=JSON.parse(raw) as MemoryAction;
        if(!a||a.projectKey!==this.project||!['enable','pause','scan','automatic_on','automatic_off','source_on','source_off'].includes(a.kind)||!Number.isSafeInteger(a.revision)||a.revision<0||typeof a.operationID!=='string'||!a.operationID||a.operationID.length>128||typeof a.scopeSHA!=='string'||!a.scopeSHA)throw new Error('上次操作记录无法读取，请保留记录并重新打开应用。');
        if(a.kind.startsWith('source_')&&(!['codex','claude','workshelf'].includes(a.sourceAdapter||'')||typeof a.sourcePath!=='string'||!a.sourcePath.startsWith('/')||a.sourcePath.length>4096))throw new Error('上次来源授权无法读取，请保留记录。');
        if(a.sourceDirectory!==undefined&&typeof a.sourceDirectory!=='boolean')throw new Error('上次来源范围无法读取，请保留记录。');
        return a;
    }
    save(a:MemoryAction){const old=this.load();if(old&&JSON.stringify(old)!==JSON.stringify(a))throw new Error('请先核对上次操作。');this.storage.setItem(this.key,JSON.stringify(a));if(JSON.stringify(this.load())!==JSON.stringify(a))throw new Error('未能保存操作记录，本次没有执行。')}
    clear(a:MemoryAction){if(this.load()?.operationID===a.operationID)this.storage.removeItem(this.key)}
}
