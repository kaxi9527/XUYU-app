import type {Action, ActionResult, API, Receipt} from './version-types';

// One pending mutation per project. Only IDs, user input and the preview token
// are retained locally; file bodies and imported provenance are never cached.
export class VersionJournal {
    private key:string;
    constructor(private api:API, projectPath:string, private projectKey:string) {
        this.key='workshelf:version-operation:v1:'+encodeURIComponent(projectPath);
    }
    pending():Action|null {
        const raw=localStorage.getItem(this.key);
        if(!raw)return null;
        const value=JSON.parse(raw) as Action;
        if(!value.operationID||value.projectKey!==this.projectKey||!['register','usable','source','regroup','export'].includes(value.kind))throw new Error('本地待核对记录不完整，已保留记录。');
        return value;
    }
    async start(input:Omit<Action,'operationID'|'projectKey'>):Promise<Receipt> {
        if(this.pending())throw new Error('请先核对上次操作的结果。');
        const action:Action={...input,projectKey:this.projectKey,operationID:crypto.randomUUID()};
        // A failed local write must prevent the remote mutation.
        localStorage.setItem(this.key,JSON.stringify(action));
        return this.submit(action);
    }
    async recover(retry=false):Promise<Receipt|null> {
        const action=this.pending();if(!action)return null;
        const result=await this.api<{found:boolean;receipt?:Receipt}>('VersionOperation',action.projectKey,action.operationID);
        if(result.found&&result.receipt){this.clear(action);return result.receipt}
        if(retry)return this.submit(action);
        return null;
    }
    private clear(action:Action) {
        // Never remove a newer record created by another window.
        if(this.pending()?.operationID===action.operationID)localStorage.removeItem(this.key);
    }
    private async submit(action:Action):Promise<Receipt> {
        const result=await this.api<ActionResult>('ApplyVersionAction',action);
        if(result.receipt?.operationID===action.operationID){this.clear(action);return result.receipt}
        if(result.uncertain===false&&result.error&&!result.receipt)this.clear(action);
        throw new Error(result.error||'未收到明确结果，请核对操作回执。');
    }
}
