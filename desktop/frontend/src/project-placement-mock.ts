// Development-only fixture. Production imports the native service instead.
export function createProjectPlacementMock(scenario:string){
    let operation:any;
    const total=scenario==='placement-large'?2267:3;
    const rows=Array.from({length:total},(_,i)=>({id:'file-'+i,source:'/Users/demo/项目/文件-'+i+'.txt',target:'/Users/demo/项目/'+(i%2?'需求文档':'参考资料')+'/文件-'+i+'.txt',folder:i%2?'需求文档':'参考资料',action:'move',reason:'依据本项目已确认的工种模板'}));
    return async(method:string,args:unknown[])=>{
        if(method==='PreviewProjectPlacement')return {token:'forward',project:'品牌更新',target:'/Users/demo/项目',folders:['需求文档','参考资料'],rows,untouched:2,undo:false};
        if(method==='ReleaseProjectPlacementPreview')return;
        if(method==='ConfirmProjectPlacement'){
            operation={id:args[2],intentSHA:'intent-sha',project:'品牌更新',revision:2,status:'confirmed',running:false,needsReconciliation:false,stopRequested:false,rows:rows.filter(r=>(args[3] as string[]).includes(r.id)).map(r=>({...r,stage:'pending'})),undoGroups:[]};return structuredClone(operation);
        }
        if(method==='ExecuteProjectPlacement'){
            operation.status='completed';operation.revision++;operation.rows.forEach((r:any)=>r.stage='moved');return structuredClone(operation);
        }
        if(method==='ProjectPlacementOperation'){if(!operation)throw new Error('未找到这次整理记录');return structuredClone(operation)}
        if(method==='PreviewProjectPlacementUndo')return {token:'undo',project:'恢复原来的位置',target:'原来的文件夹',folders:['/Users/demo/项目'],rows:operation.rows.map((r:any)=>({...r,source:r.target,target:r.source,folder:'/Users/demo/项目'})),undo:true,untouched:0};
        if(method==='ConfirmProjectPlacementUndo'){operation.undoGroups.push({id:args[2],sha:'group-sha',status:'confirmed',restored:0,total:operation.rows.length});return structuredClone(operation)}
        if(method==='ExecuteProjectPlacementUndo'){operation.rows.forEach((r:any)=>r.stage='undone');operation.undoGroups[0].status='completed';operation.undoGroups[0].restored=operation.rows.length;operation.revision++;return structuredClone(operation)}
        if(method==='StopProjectPlacement'){operation.status='stopped';operation.stopRequested=true;return structuredClone(operation)}
        throw new Error('Unsupported placement fixture method: '+method);
    };
}
