// Browser fixture for portal-phone.mjs: AppShell with a tab bar, a drill-down
// page and an account, in a stand-in for DSM's Application Portal page on a
// phone (?portal=1, the default) or on a page of its own (?portal=0).
// ?actions=1 gives the App header actions; ?user= the account (empty: none).
import '@synopackageland/ui/themes/dsm7-embedded.css'
import {createApp,h,ref} from 'vue'
import {AppTabBar,Card,PageHeader} from '@synopackageland/ui'
import {CalendarDays,Home,Settings,Share2} from '@lucide/vue'
import AppShell from '../../src/shell/AppShell.vue'

const params=new URLSearchParams(location.search)
const portal=params.get('portal')!=='0',user=params.get('user')??'administrator',actions=params.get('actions')==='1'
const name='Meeting Notes'
const icon="data:image/svg+xml,"+encodeURIComponent("<svg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 24 24'><rect width='24' height='24' rx='6' fill='#057feb'/><path d='M7 9h2v6H7zm4-3h2v12h-2zm4 4h2v4h-2z' fill='#fff'/></svg>")
let mount=document.getElementById('app')!
if(portal){
 document.getElementById('header')!.style.backgroundImage=`url("${icon}")`
 document.getElementById('name')!.textContent=name
 if(user){
  const toolbar=document.getElementById('toolbar')!
  toolbar.innerHTML='<span class="x-btn sds-standalone-main-window-header__toolbar__btn"><em class="x-btn-arrow"><button type="button" class="x-btn-text"></button></em></span>'
  toolbar.querySelector('button')!.textContent=user
 }
 // As ExtJS: the window spans the viewport and the body takes its height less the header's, inline.
 const main=document.getElementById('main')!,tl=document.getElementById('tl')!,body=document.getElementById('body')!
 const size=()=>{main.style.width=`${innerWidth}px`;body.style.width=`${innerWidth}px`;body.style.height=`${innerHeight-tl.offsetHeight}px`}
 size();addEventListener('resize',size)
}else{
 document.getElementById('main')!.remove()
 mount=document.createElement('div');mount.style.height='100%';document.body.append(mount)
}
const page=ref('home'),detail=ref(false)
const pages=[{id:'home',title:'Home',icon:Home},{id:'meetings',title:'Meetings',icon:CalendarDays},{id:'settings',title:'Settings',icon:Settings,header:true},{id:'about',title:'About'}]
const tabs=pages.slice(0,3).map(p=>({key:p.id,label:p.title,icon:p.icon}))
const signedOut=()=>{document.documentElement.dataset.signedOut='1';return {status:'requested' as const}}
const row=(title:string,i:number)=>h(Card,{style:'margin-bottom:12px;padding:16px'},()=>[h('strong',title),h('p',{style:'margin:6px 0 0;color:var(--ui-text-secondary)'},`Meeting ${i+1} · 30 minutes`)])
createApp({render:()=>h(AppShell,{
 appRoot:true,locale:'en',modelValue:page.value,'onUpdate:modelValue':(value:string)=>{page.value=value;detail.value=false},
 pages,
 ...(actions?{headerActions:[{key:'share',label:'Share',icon:Share2},{key:'export',label:'Export'}],onHeaderAction:(key:string)=>{document.documentElement.dataset.headerAction=key}}:{}),
 ctx:{fullPage:true,displayName:name,appIcon:icon,...(user?{user:{name:user,isAdmin:true}}:{}),host:{signOut:signedOut,platform:()=>({host:'dsm',dsmStandalone:true})} as never},
},{
 tabbar:({page:current}:{page:string})=>h(AppTabBar,{items:tabs,modelValue:current,'onUpdate:modelValue':(value:string)=>{page.value=value;detail.value=false}}),
 default:({page:current}:{page:string})=>h('div',{'data-ready':''},current==='meetings'&&detail.value
  ? [h(PageHeader,{title:'Weekly sync',back:true,onBack:()=>{detail.value=false}}),h('p','Transcript and notes'),...Array.from({length:12},(_,i)=>row(`Note ${i+1}`,i))]
  : [h('p',{style:'margin:0 0 16px'},current),...Array.from({length:40},(_,i)=>h('div',{onClick:()=>{if(current==='meetings')detail.value=true},'data-row':i},[row(`Meeting ${i+1}`,i)]))]),
})}).mount(mount)
