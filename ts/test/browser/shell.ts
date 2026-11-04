// Browser fixture for shell-layout.mjs: AppShell with the default page header
// and scrolling. Settings pages sit inside the template's own page wrappers
// (.integration-app > [data-page]) with no CSS to pass the height on.
import '@synopackageland/ui/themes/dsm7-embedded.css'
import {createApp,defineComponent,h,ref} from 'vue'
import {Button,FormItem,Input,SectionTabs,SettingsPage,SettingsSection} from '@synopackageland/ui'
import AppShell from '../../src/shell/AppShell.vue'
import PageActions from '../../src/shell/PageActions.vue'
import type {AppShellPage} from '../../src/shell/app-pages'

const sections=(prefix:string,count:number)=>Array.from({length:count},(_,i)=>h(SettingsSection,{title:`${prefix} section ${i+1}`,collapsible:false},()=>[
 h(FormItem,{label:'Name'},()=>h(Input,{modelValue:`Value ${i+1}`})),
 h(FormItem,{label:'Folder'},()=>h(Input,{modelValue:'/volume1/data'})),
]))
const footer=()=>[h(Button,{variant:'secondary'},()=>'Reset'),h(Button,{variant:'primary'},()=>'Apply')]
const tab=ref('general')
const Tabbed=defineComponent({render:()=>h('div',{class:'integration-app'},[h('div',{'data-page':'settings'},[
 h(SectionTabs,{modelValue:tab.value,'onUpdate:modelValue':(v:string)=>{tab.value=v},items:[{value:'general',label:'General'},{value:'storage',label:'Storage'},{value:'about',label:'About'}],fill:true},{
  'panel-general':()=>h(SettingsPage,null,{default:()=>sections('General',8),footer}),
  'panel-storage':()=>h(SettingsPage,null,{default:()=>sections('Storage',3),footer}),
  'panel-about':()=>h('div',{class:'about'},Array.from({length:40},(_,i)=>h('p',`About line ${i+1}`))),
 }),
])])})
const Plain=defineComponent({render:()=>h('div',{class:'integration-app'},[h('div',{'data-page':'plain'},[h(SettingsPage,null,{default:()=>sections('Plain',8),footer})])])})
const Home=defineComponent({render:()=>[h(PageActions,null,()=>h(Button,{variant:'secondary'},()=>'Rescan')),h('div',{class:'home'},Array.from({length:60},(_,i)=>h('p',`Line ${i+1} of a long page`)))]})
const Titled=defineComponent({render:()=>h('div',Array.from({length:60},(_,i)=>h('p',`Titled line ${i+1}`)))})
const pages:AppShellPage[]=[
 {id:'home',title:'Home',component:Home},
 {id:'settings',title:'Settings',component:Tabbed},
 {id:'plain',title:'Plain settings',component:Plain},
 {id:'titled',title:'Titled page',header:true,component:Titled},
]
const params=new URLSearchParams(location.search)
const page=ref(params.get('page')??'home')
createApp({render:()=>h(AppShell,{appRoot:true,locale:'en',modelValue:page.value,'onUpdate:modelValue':(v:string)=>{page.value=v},pages,brandName:'Shell fixture',sidebarStorageKey:'shell-fixture'},{
 actions:()=>h(Button,{variant:'secondary'},()=>'Refresh'),
})}).mount('#app')
;(window as unknown as {shell:unknown}).shell={page,tab}
