type FolderAccess = {name:string; status:string};
type API = <T=unknown>(method:string,...args:unknown[])=>Promise<T>;
const key = 'workshelf.file-access-checked.v1';

export function accessWasChecked():boolean {
  try { return localStorage.getItem(key) === 'yes'; } catch { return false; }
}

export async function checkAccess(api:API):Promise<FolderAccess[]> {
  const folders = await api<FolderAccess[]>('CheckFileAccess');
  try { localStorage.setItem(key, 'yes'); } catch { /* The current check still works. */ }
  window.dispatchEvent(new CustomEvent('workshelf:access-status', {detail:folders}));
  return folders;
}
