// A caller outside AppRoot uses the shell's phone scope and mobile tabbar.
import '@synopackageland/ui/themes/dsm7-embedded.css'
import {createApp,h,ref} from 'vue'
import {AppTabBar,Button,Card} from '@synopackageland/ui'
import {BookOpen,Home,RefreshCw,Settings} from '@lucide/vue'
import AppShell from '../../src/shell/AppShell.vue'

const page=ref('home')
const pages=[{id:'home',title:'Overview',icon:Home},{id:'books',title:'Books',icon:BookOpen},{id:'settings',title:'Settings',icon:Settings}]
const tabs=pages.slice(0,2).map(p=>({key:p.id,label:p.title,icon:p.icon}))
const params=new URLSearchParams(location.search)
createApp({render:()=>h(AppShell,{
 appRoot:true,locale:'en',modelValue:page.value,'onUpdate:modelValue':(value:string)=>{page.value=value},
 pages,brandName:'Library',ctx:{fullPage:params.get('window')!=='1'},
 title:'My library',headerActions:[{key:'refresh',label:'Refresh library',icon:RefreshCw},{key:'settings',label:'Settings'}],
 onHeaderAction:(key:string)=>{document.documentElement.dataset.headerAction=key},
},{
 tabbar:({page:current}:{page:string})=>h(AppTabBar,{items:tabs,modelValue:current,'onUpdate:modelValue':(value:string)=>{page.value=value}}),
 actions:()=>h(Button,{variant:'secondary'},()=>'Add book'),
 default:({phone}:{phone:boolean})=>h('div',{'data-phone':String(!!phone)},[
  h('p',{style:'margin:0 0 20px;color:var(--ui-text-secondary)'},'Your books, ready whenever you are'),
  ...['Recently added','Continue reading','Saved for later'].map((title,i)=>h(Card,{style:'margin-bottom:16px;padding:20px'},()=>[
   h('h2',{style:'margin:0 0 8px;font-size:16px'},title),
   h('p',{style:'margin:0;color:var(--ui-text-secondary)'},['Browse the latest additions to your collection','Pick up where you left off','Keep your next read close at hand'][i]),
  ])),
 ]),
})}).mount('#app')
