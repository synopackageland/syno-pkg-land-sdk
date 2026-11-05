// Browser fixture for portal-header.mjs: AppShell in a stand-in DSM standalone
// main window. ?name= the App name, ?user= the account (empty: no button),
// ?locale= the shell locale. The account menu renders into the body on its
// first open, as DSM does.
import '@synopackageland/ui/themes/dsm7-embedded.css'
import {createApp,h,ref} from 'vue'
import {Card} from '@synopackageland/ui'
import {Home,Settings} from '@lucide/vue'
import AppShell from '../../src/shell/AppShell.vue'

const params=new URLSearchParams(location.search)
const name=params.get('name')??'Household Photo Library Manager',user=params.get('user')??'administrator'
const icon="data:image/svg+xml,"+encodeURIComponent("<svg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 24 24'><rect width='24' height='24' rx='6' fill='#057feb'/><path d='M6 16l4-5 3 4 2-2 3 3z' fill='#fff'/></svg>")
document.getElementById('header')!.style.backgroundImage=`url("${icon}")`
document.getElementById('name')!.textContent=name
if(user){
 const toolbar=document.getElementById('toolbar')!
 toolbar.innerHTML='<span class="x-btn sds-standalone-main-window-header__toolbar__btn syno-ux-button syno-ux-button-dropdown"><em class="x-btn-arrow x-unselectable"><button type="button" class="x-btn-text"></button></em></span>'
 const button=toolbar.querySelector('button')!
 button.textContent=user
 let menu: HTMLElement|undefined
 button.addEventListener('click',()=>{
  if(!menu){
   menu=document.createElement('div')
   menu.className='x-menu x-menu-floating x-layer sds-standalone-main-window-header__toolbar__menu syno-ux-menu'
   menu.innerHTML='<ul class="x-menu-list"><li class="x-menu-list-item sds-standalone-help">Help</li></ul>'
   menu.style.visibility='hidden'
   document.body.append(menu)
  }
  const shown=menu.style.visibility!=='hidden'
  const rect=button.closest('.x-btn')!.getBoundingClientRect()
  menu.style.top=`${rect.bottom+4}px`;menu.style.right=`${Math.max(4,innerWidth-rect.right)}px`
  menu.style.visibility=shown?'hidden':'visible'
 })
}
const page=ref('home')
const pages=[{id:'home',title:'Overview',icon:Home},{id:'settings',title:'Settings',icon:Settings}]
createApp({render:()=>h(AppShell,{
 appRoot:true,locale:params.get('locale')??'en',modelValue:page.value,'onUpdate:modelValue':(value:string)=>{page.value=value},
 pages,ctx:{fullPage:true,displayName:name,user:{name:user||'administrator',isAdmin:true}},
},{default:()=>h('div',{'data-ready':''},[h(Card,{style:'padding:20px'},()=>'Content')])})}).mount('#app')
