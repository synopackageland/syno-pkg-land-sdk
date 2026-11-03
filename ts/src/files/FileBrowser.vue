<script setup lang="ts">
import {computed,h,markRaw,onBeforeUnmount,onMounted,ref} from 'vue'
import {Breadcrumb,Button,Callout,DataGrid,FileTypeIcon,IconButton,TreeView,FilePicker,type BreadcrumbItem,type DataGridSort,type TreeNode} from '@synopackageland/ui'
import {listingCache,listingKey,type Listing} from '../nas/listing-cache'
import {ChevronLeft,ChevronRight,RotateCw} from '@lucide/vue'
import type {NasEntry,NasPage,NasProvider} from '../nas/contract'
import {nasCopy,nasErrorCopy} from '../nas/locale'
import {formatSize} from './size'
import {format,uiLocale} from '../i18n'
import {useNasRequest} from '../nas/use-request'
// fill: the browser takes its container's height (a dialog) and leaves the
// selected path to its owner.
const props=withDefaults(defineProps<{provider:NasProvider;locale:string;name?:string;mode?:'folder'|'file';transfers?:boolean;fill?:boolean}>(),{mode:'file',transfers:false,fill:false})
const emit=defineEmits<{selected:[path:string|undefined]}>()
const copy=computed(()=>nasCopy(props.locale))
// busy: an upload or a download. Listing a folder never blocks the browser:
// it shows what it listed before, lists again, and a newer choice replaces it.
const request=useNasRequest(),{busy,error}=request
const loading=ref(false),stale=ref(false),cache=computed(()=>listingCache(props.provider))
const entries=ref<NasEntry[]>([]),roots=ref<TreeNode[]>([]),trail=ref<NasEntry[]>([]),chosen=ref<NasEntry>(),highlighted=ref<string>(),next=ref<number>()
const expanded=ref<string[]>([]),treeSelection=ref<string>()
const sort=ref<DataGridSort>({key:'name',direction:'asc'}),generation=ref(0)
const uploads=ref<globalThis.File[]>([]),uploadDone=ref(false)
let life=new AbortController()
async function download(){
 const selected=chosen.value
 if(!selected||selected.isDir||!props.provider.download)return
 await request.run(async signal=>{await props.provider.info(selected.path,signal);await props.provider.download!(selected.path);return true})
}
async function upload(){
 const parent=trail.value.at(-1)?.path,file=uploads.value[0]
 if(!parent||!file||!props.provider.upload)return
 uploadDone.value=false
 const done=await request.run(async signal=>{await props.provider.upload!(parent,file,signal);return true})
 if(done){uploads.value=[];await browse(trail.value);uploadDone.value=true}
}

const columns=computed(()=>[
 {key:'name',label:copy.value.nameColumn,sortable:true,tooltip:(row:Record<string,unknown>)=>row.path as string},
 {key:'kind',label:copy.value.kindColumn,width:96},
 {key:'bytes',label:copy.value.sizeColumn,align:'right' as const,sortable:true,width:96},
])
// Tree nodes live in a ref; a raw icon keeps the component out of reactivity.
const treeIcon=markRaw((_:unknown,{attrs}:{attrs:Record<string,unknown>})=>h(FileTypeIcon,{...attrs,kind:'folder'}))
function node(entry:NasEntry):TreeNode{return {id:entry.path,label:entry.name,icon:treeIcon,data:entry}}
function select(entry?:NasEntry){chosen.value=entry;emit('selected',entry?.path)}
// The grid highlights any row; folder mode selects the open folder until a folder row is highlighted.
function choose(entry:NasEntry){
 highlighted.value=entry.path
 if(entry.isDir)prefetch(entry.path)
 select(props.mode==='folder'?entry.isDir?entry:trail.value.at(-1):entry.isDir?undefined:entry)
}
function activate(entry:NasEntry){if(entry.isDir)open(entry);else choose(entry)}
const entryAt=(path:unknown)=>entries.value.find(e=>e.path===path)!
function findTrail(nodes:TreeNode[],id:string,parents:NasEntry[]=[]):NasEntry[]|undefined{
 for(const n of nodes){const path=[...parents,n.data as NasEntry];if(n.id===id)return path;const found=findTrail(n.children??[],id,path);if(found)return found}
}
function findNode(nodes:TreeNode[],id:string):TreeNode|undefined{
 for(const n of nodes){if(n.id===id)return n;const found=findNode(n.children??[],id);if(found)return found}
}
// A new node on the path: the tree deep-watches the selected node, so mutating it reads as a selection.
function withChildren(nodes:TreeNode[],id:string,children:TreeNode[]):TreeNode[]{
 return nodes.map(n=>n.id===id?{...n,children}:n.children?{...n,children:withChildren(n.children,id,children)}:n)
}
// The tree follows the open folder: its ancestors load and expand, and it is selected.
let revealing=0
async function reveal(target:NasEntry[]){
 const token=++revealing
 for(const ancestor of target.slice(0,-1)){
  const parent=findNode(roots.value,ancestor.path)
  if(!parent)break
  if(parent.children===undefined){
   const children=await loadChildren(parent)
   if(token!==revealing||!children)return
   roots.value=withChildren(roots.value,parent.id,children)
  }
  if(!expanded.value.includes(parent.id))expanded.value=[...expanded.value,parent.id]
 }
 treeSelection.value=target.at(-1)?.path
}
const folders=(entries:NasEntry[])=>entries.filter(e=>e.isDir).map(node)
async function loadChildren(parent:TreeNode):Promise<TreeNode[]|undefined>{
 const known=cache.value.get(listingKey(parent.id))
 if(known&&known.next===undefined)return folders(known.entries)
 const version=generation.value,signal=life.signal
 try{
  const entries:NasEntry[]=[]
  let offset:number|undefined=0
  while(offset!==undefined){
   const page=await props.provider.list(parent.id,{offset,sortBy:'name',sortDirection:'ASC'},signal)
   if(signal.aborted||version!==generation.value)return undefined
   entries.push(...page.entries);offset=page.nextOffset
  }
  cache.value.set(listingKey(parent.id),{entries})
  return folders(entries)
 }catch(e){if(!signal.aborted&&version===generation.value){error.value=e;select()};return undefined}
}
// A highlighted folder is listed ahead, so opening it shows its content at once.
// Opening it while that listing is out waits for it instead of asking again.
const prefetching=new Map<string,Promise<NasPage>>()
function prefetch(path:string){
 const {sortBy,direction}=order(),key=listingKey(path,sortBy,direction),version=generation.value,signal=life.signal
 if(cache.value.has(key)||prefetching.has(key))return
 const page=props.provider.list(path,{offset:0,sortBy,sortDirection:direction},signal)
 prefetching.set(key,page)
 page.then(page=>{if(!signal.aborted&&version===generation.value&&!cache.value.has(key))cache.value.set(key,{entries:page.entries,next:page.nextOffset})},()=>{})
  .finally(()=>{if(prefetching.get(key)===page)prefetching.delete(key)})
}
const order=()=>({sortBy:sort.value.key as 'name'|'size',direction:sort.value.direction==='asc'?'ASC' as const:'DESC' as const})
// Shares listed again keep the folders the tree already opened.
function rootsFrom(entries:NasEntry[]){
 const known=new Map(roots.value.map(n=>[n.id,n]))
 roots.value=folders(entries).map(n=>known.get(n.id)??n)
}
let browsing:AbortController|undefined,browsed=0
function shown(target:NasEntry[],listing:Listing){
 entries.value=listing.entries;next.value=listing.next
 const path=target.at(-1)?.path
 if(!path)rootsFrom(listing.entries)
 else if(props.mode==='folder')select(target.at(-1))
 void reveal(target)
}
async function browse(target:NasEntry[],more=false){
 browsing?.abort()
 const controller=new AbortController(),version=++browsed,stop=()=>controller.abort()
 browsing=controller;life.signal.addEventListener('abort',stop,{once:true})
 const path=target.at(-1)?.path,{sortBy,direction}=order(),key=listingKey(path??'',sortBy,direction)
 const offset=more?next.value??0:0,known=more?undefined:cache.value.get(key)
 highlighted.value=undefined;trail.value=target;error.value=undefined
 if(!more){select();stale.value=!!known;if(known)shown(target,known);else{entries.value=[];next.value=undefined}}
 loading.value=true
 try{
  const ahead=more?undefined:prefetching.get(key)
  const page=await (ahead??(path?props.provider.list(path,{offset,sortBy,sortDirection:direction},controller.signal):props.provider.shares(offset,controller.signal)))
  if(version!==browsed||controller.signal.aborted)return
  const listing={entries:more?[...entries.value,...page.entries]:page.entries,next:page.nextOffset}
  cache.value.set(key,listing)
  if(more){entries.value=listing.entries;next.value=listing.next}else shown(target,listing)
  stale.value=false
 }catch(e){if(version===browsed&&!controller.signal.aborted){error.value=e;select()}}
 finally{life.signal.removeEventListener('abort',stop);if(version===browsed){loading.value=false;browsing=undefined}}
}
const crumbs=computed<BreadcrumbItem[]>(()=>[{id:'',display:copy.value.roots},...trail.value.map(e=>({id:e.path,display:e.name}))])
// The last level is the open folder: clicking it does nothing.
function crumb(item:BreadcrumbItem){const depth=crumbs.value.findIndex(c=>c.id===item.id);if(depth<trail.value.length)go(trail.value.slice(0,depth))}
function open(entry:NasEntry){go([...trail.value,entry])}
// Visited folders: back and forward move in them, and a new folder drops the ones ahead.
const history=ref<NasEntry[][]>([[]]),at=ref(0)
function go(target:NasEntry[]){
 if(busy.value)return
 if(target.at(-1)?.path!==trail.value.at(-1)?.path){history.value=[...history.value.slice(0,at.value+1),target];at.value++}
 void browse(target)
}
function travel(step:number){if(busy.value)return;at.value+=step;void browse(history.value[at.value]!)}
function sortChanged(value:DataGridSort|null){sort.value={key:value?.key==='bytes'?'size':'name',direction:value?.direction??'asc'};void browse(trail.value)}
function refresh(){
 if(busy.value)return
 cache.value.clear();prefetching.clear()
 generation.value++;life.abort();life=new AbortController();roots.value=[];expanded.value=[];treeSelection.value=undefined
 history.value=[[]];at.value=0
 void browse([])
}
onMounted(()=>void browse([]))
onBeforeUnmount(()=>{generation.value++;life.abort()})
</script>
<template>
 <section class="file-browser" :class="{'file-browser-fill':fill}" :aria-busy="busy||loading" :aria-label="copy.title">
  <div class="file-toolbar file-location">
   <IconButton :label="copy.back" size="sm" :disabled="busy||at===0" @click="travel(-1)"><ChevronLeft :size="16" aria-hidden="true" /></IconButton>
   <IconButton :label="copy.forward" size="sm" :disabled="busy||at===history.length-1" @click="travel(1)"><ChevronRight :size="16" aria-hidden="true" /></IconButton>
   <IconButton :label="copy.refresh" size="sm" :disabled="busy" @click="refresh"><RotateCw :size="14" aria-hidden="true" :class="{'file-refreshing':loading&&stale}" /></IconButton>
   <nav class="file-path" :aria-label="copy.location"><Breadcrumb :items="crumbs" :collapsed-label="copy.fullPath" @click-item="crumb" /></nav>
  </div>
  <div v-if="transfers&&provider.upload&&trail.length" class="file-toolbar">
   <FilePicker v-model="uploads" variant="compact" :disabled="busy" :max-files="1" :max-size="16*1024**3" :title="copy.upload" @rejected="error={code:'upload_size_required'}" />
   <Button variant="primary" :disabled="busy||!uploads.length" @click="upload">{{ copy.upload }}</Button>
  </div>
  <p v-if="uploadDone" role="status">{{ copy.uploaded }}</p>
  <Button v-if="transfers&&provider.download&&chosen&&!chosen.isDir&&!error" variant="secondary" :disabled="busy" @click="download">{{ copy.download }}</Button>
  <Callout v-if="error" tone="danger"><span role="alert">{{ nasErrorCopy(error,copy,locale,name) }}</span></Callout>
  <div class="file-columns">
   <aside class="file-tree" :aria-label="copy.roots">
    <TreeView v-if="roots.length" :key="generation" selection-style="background" v-model="treeSelection" v-model:nodes="roots" v-model:expanded="expanded" :load-children="loadChildren" :expand-label="(n,expanded)=>format(expanded?copy.collapse:copy.expand,{name:n.label})" @select="n=>go(findTrail(roots,n.id)??[n.data as NasEntry])" />
   </aside>
   <div class="file-content">
    <DataGrid v-if="!error" :columns="columns" :rows="entries.map(e=>({...e,kind:e.isDir?copy.directoryKind:copy.fileKind}))" row-key="path" :footer="false" :resizable="false" :local-sort="false" :local-pagination="false" :loading="loading&&!stale" loading-text="" :sort="{key:sort.key==='size'?'bytes':sort.key,direction:sort.direction}" :labels="{empty:copy.emptyFolder}" single-select :selected-keys="highlighted?[highlighted]:[]" @update:sort="sortChanged" @update:selected-keys="keys=>{if(!busy&&keys.length)choose(entryAt(keys[0]))}" @row-activate="row=>{if(!busy)activate(entryAt(row.path))}" @row-dblclick="row=>{if(!busy)activate(entryAt(row.path))}">
     <template #cell-name="{row}">
      <span class="file-name"><FileTypeIcon :kind="row.isDir?'folder':'file'" :name="row.name as string" /><span class="file-name-text">{{ row.name }}</span></span>
     </template>
     <template #empty><div class="file-empty"><FileTypeIcon kind="folder" :size="[40,40]" /><span>{{ copy.emptyFolder }}</span></div></template>
     <template #cell-bytes="{row}"><span class="file-bytes">{{ row.isDir||row.bytes===undefined?'—':formatSize(row.bytes as number,uiLocale(locale)) }}</span></template>
    </DataGrid>
    <Button v-if="next!==undefined&&!error" variant="secondary" :disabled="busy||loading" @click="browse(trail,true)">{{ copy.loadMore }}</Button>
   </div>
  </div>
  <p v-if="chosen&&!error&&!fill" class="file-selection" role="status">{{ format(copy.selected,{name:chosen.path}) }}</p>
 </section>
</template>
<style scoped>
.file-browser { min-width: 0; color: var(--ui-text); }
.file-toolbar { display: flex; flex-wrap: wrap; gap: var(--ui-space-2, 8px); margin-block: var(--ui-space-3, 12px); }
.file-location { flex-wrap: nowrap; align-items: center; gap: var(--ui-space-1, 4px); }
.file-path { flex: 1; min-width: 0; margin-inline-start: var(--ui-space-2, 8px); }
.file-columns { display: grid; grid-template-columns: minmax(140px, 1fr) minmax(0, 3fr); gap: var(--ui-space-3, 12px); height: clamp(240px, 60vh, 400px); }
/* Rows cut their names; neither pane scrolls sideways. */
.file-tree { position: relative; min-width: 0; overflow-x: hidden; overflow-y: auto; border-inline-end: 1px solid var(--ui-border); }
.file-content { display: flex; flex-direction: column; min-width: 0; min-height: 0; overflow: hidden; }
.file-empty { display: flex; flex-direction: column; align-items: center; gap: var(--ui-space-2, 8px); padding-block: var(--ui-space-6, 24px); color: var(--ui-text-muted); }
.file-name { display: flex; align-items: center; gap: var(--ui-space-2, 8px); min-width: 0; }
.file-name > :first-child { flex: none; }
.file-name-text { min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.file-bytes { font-variant-numeric: tabular-nums; }
.file-selection { overflow-wrap: anywhere; }
.file-refreshing { animation: file-refresh 1s linear infinite; }
@keyframes file-refresh { to { transform: rotate(360deg); } }
@media (prefers-reduced-motion: reduce) { .file-refreshing { animation: none; } }
/* In a dialog the toolbar is one tight row and the panes take the rest. */
.file-browser-fill { display: flex; flex-direction: column; height: 100%; min-height: 0; }
.file-browser-fill > .file-location { flex: none; margin-block: 0 var(--ui-space-2, 8px); }
.file-browser-fill > .file-columns { flex: 1 1 0; min-height: 200px; height: auto; }
@media (max-width: 600px) {
 .file-columns { grid-template-columns: minmax(0, 1fr); height: auto; }
 .file-content :deep(th:nth-child(2)), .file-content :deep(td:nth-child(2)), .file-content :deep(th:nth-child(3)), .file-content :deep(td:nth-child(3)) { display: none; }
 .file-tree { height: clamp(96px, 25vh, 180px); border-inline-end: 0; border-bottom: 1px solid var(--ui-border); }
 .file-content { height: clamp(200px, 45vh, 360px); }
 .file-browser-fill > .file-columns { grid-template-rows: minmax(72px, 1fr) minmax(0, 2fr); }
 .file-browser-fill .file-tree, .file-browser-fill .file-content { height: auto; min-height: 0; }
}
</style>
