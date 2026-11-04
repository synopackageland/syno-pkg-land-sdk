// Browser fixture for callout-recall.mjs: AppShell pages with no buttons of
// their own and no App actions, each with a dismissible callout. 'notes' also
// shows a PageInfo, whose button then lists the dismissed callout.
import '@synopackageland/ui/themes/dsm7-embedded.css'
import {createApp,defineComponent,h,ref} from 'vue'
import {Callout} from '@synopackageland/ui'
import AppShell from '../../src/shell/AppShell.vue'
import PageInfo from '../../src/shell/PageInfo.vue'
import type {AppShellPage} from '../../src/shell/app-pages'

const body=()=>Array.from({length:12},(_,i)=>h('p',`Line ${i+1} of a page without buttons`))
const callout=(key:string)=>h(Callout,{tone:'info',dismissible:true,dismissKey:key,title:'Scans run nightly'},()=>'Results appear here after the first scan.')
const Plain=defineComponent({render:()=>[callout('fixture-plain'),...body()]})
const Notes=defineComponent({render:()=>[h(PageInfo,{notes:['Sizes count only the data snapshots hold.']}),callout('fixture-notes'),...body()]})
const pages:AppShellPage[]=[
 {id:'plain',title:'No buttons',component:Plain},
 {id:'notes',title:'With notes',component:Notes},
]
const page=ref(new URLSearchParams(location.search).get('page')??'plain')
createApp({render:()=>h(AppShell,{appRoot:true,locale:'en',modelValue:page.value,'onUpdate:modelValue':(v:string)=>{page.value=v},pages,brandName:'Callout fixture',sidebarStorageKey:'callout-fixture'})}).mount('#app')
