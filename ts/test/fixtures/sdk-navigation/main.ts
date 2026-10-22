import {createApp,h,ref} from 'vue'
import '@synopackageland/ui/themes/dsm7-embedded.css'
import AppShell from '../../../src/shell/AppShell.vue'

const query=new URLSearchParams(location.search)
const page=ref('home')
const navigation=query.get('navigation')==='top'?'top':undefined
const fullPage=query.get('window')!=='true'
createApp({render:()=>h(AppShell,{
 appRoot:true,locale:'en',modelValue:page.value,'onUpdate:modelValue':(value:string)=>{page.value=value},
 pages:[{id:'home',title:'Home'},{id:'logs',title:'Logs'}],navigation,
 ctx:{fullPage,displayName:'Navigation demo',user:{name:'alice',isAdmin:false}},
},{default:()=>h('p',`Current page: ${page.value}`)})}).mount('#app')
