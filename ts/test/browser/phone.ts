// A caller outside AppRoot uses the shell's phone scope and mobile tabbar.
import '@synopackageland/ui/themes/dsm7-embedded.css'
import {createApp,h,ref} from 'vue'
import {AppTabBar,Button,Card,PageHeader} from '@synopackageland/ui'
import {BookOpen,Home,RefreshCw,Settings} from '@lucide/vue'
import AppShell from '../../src/shell/AppShell.vue'

const page=ref('home')
// 'live' is hidden and not a tab: reached from Overview, it keeps Overview's tab lit.
const pages=[{id:'home',title:'Overview',icon:Home},{id:'books',title:'Books',icon:BookOpen},{id:'settings',title:'Settings',icon:Settings},{id:'live',title:'Live reading',hidden:true}]
const tabs=pages.slice(0,2).map(p=>({key:p.id,label:p.title,icon:p.icon}))
const params=new URLSearchParams(location.search)
const accessory=ref(params.has('accessory'))
createApp({render:()=>h(AppShell,{
 appRoot:true,locale:'en',modelValue:page.value,'onUpdate:modelValue':(value:string)=>{page.value=value},
 pages,brandName:'Library',ctx:{fullPage:params.get('window')!=='1'},
 title:'My library',headerActions:[{key:'refresh',label:'Refresh library',icon:RefreshCw},{key:'settings',label:'Settings'}],
 onHeaderAction:(key:string)=>{document.documentElement.dataset.headerAction=key},
},{
 accessory:({page,phone}:{page:string;phone:boolean})=>accessory.value?h('button',{'data-running-task':'',style:'display:block;width:100%;height:48px'},`Running task: ${page} (${phone?'phone':'desktop'})`):null,
 tabbar:({page:current}:{page:string})=>h(AppTabBar,{items:tabs,modelValue:current==='live'?'home':current,'onUpdate:modelValue':(value:string)=>{page.value=value}}),
 actions:()=>h(Button,{variant:'secondary'},()=>'Add book'),
 default:({page:current,phone}:{page:string;phone:boolean})=>current==='live'?h('div',{'data-phone':String(!!phone)},[
  h(PageHeader,{title:'Live reading',back:true,onBack:()=>{page.value='home'}}),
  h('p',{style:'margin:0'},'Reading along with 3 friends'),
 ]):h('div',{'data-phone':String(!!phone)},[
  h('p',{style:'margin:0 0 20px;color:var(--ui-text-secondary)'},'Your books, ready whenever you are'),
  ...['Recently added','Continue reading','Saved for later'].map((title,i)=>h(Card,{style:'margin-bottom:16px;padding:20px'},()=>[
   h('h2',{style:'margin:0 0 8px;font-size:16px'},title),
   h('p',{style:'margin:0;color:var(--ui-text-secondary)'},['Browse the latest additions to your collection','Pick up where you left off','Keep your next read close at hand'][i]),
  ])),
 ]),
})}).mount('#app')
;(window as unknown as {shellAccessory:unknown;shellPage:unknown}).shellAccessory=accessory
;(window as unknown as {shellPage:unknown}).shellPage=page
