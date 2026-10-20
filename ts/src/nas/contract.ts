export interface NasEntry {name:string; path:string; isDir:boolean; bytes?:number}
export interface NasPage {entries:NasEntry[]; total:number; offset:number; nextOffset?:number}
export interface ListOptions {offset?:number;limit?:number;sortBy?:'name'|'size'|'type'|'mtime';sortDirection?:'ASC'|'DESC'}
export interface NasProvider {
 download?(path:string):void
 upload?(parent:string,file:File,signal:AbortSignal):Promise<void>
 shares(offset:number,signal:AbortSignal):Promise<NasPage>
 list(path:string,options:ListOptions,signal:AbortSignal):Promise<NasPage>
 info(path:string,signal:AbortSignal):Promise<NasEntry>
 createFolder(parent:string,name:string,signal:AbortSignal):Promise<void>
 dispose():void
}
