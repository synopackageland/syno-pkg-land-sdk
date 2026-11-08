// @vitest-environment jsdom
import {expect,test} from 'vitest'
import {createApp,h,nextTick,ref} from 'vue'
import {AppRoot} from '@synopackageland/ui'
import {profilePurpose,profilePurposeCatalogs} from './profile-purpose'
import {uiLocales} from '../i18n'
import ExecutorConsent from './ExecutorConsent.vue'

test('every standard purpose has nine complete translations without closing periods',()=>{
 for(const locale of uiLocales){
  expect(Object.keys(profilePurposeCatalogs[locale])).toEqual(Object.keys(profilePurposeCatalogs.en))
  for(const [key,text] of Object.entries(profilePurposeCatalogs[locale])){
   expect(text.trim()).not.toBe('');expect(text).not.toMatch(/[.。]$/)
   if(locale!=='en')expect(text).not.toBe(profilePurposeCatalogs.en[key as keyof typeof profilePurposeCatalogs.en])
  }
 }
})
test('App translations fall back to English and custom text never inherits a name-based purpose',()=>{
 expect(profilePurpose({purpose:'Read other data',purpose_i18n:{en:'Read custom data','zh-TW':'讀取自訂資料'}},'cht')).toBe('讀取自訂資料')
 expect(profilePurpose({purpose:'Read other data',purpose_i18n:{en:'Read custom data'}},'fr')).toBe('Read custom data')
 expect(profilePurpose({purpose:'Read other data',purpose_i18n:{'zh-TW':'讀取資料'}},'fr')).toBe('Read other data')
 expect(profilePurpose({purpose:'Read only my custom data'},'ja')).toBe('Read only my custom data')
 expect(profilePurpose({purpose:profilePurposeCatalogs.en['update-read']},'cht')).toBe(profilePurposeCatalogs['zh-TW']['update-read'])
})
test('the consent list follows locale changes and renders translations as text',async()=>{
 const locale=ref('zh-TW'),root=document.createElement('div')
 const app=createApp({render:()=>h(AppRoot,{},()=>h(ExecutorConsent,{admin:true,locale:locale.value,webapiProfiles:{custom:{purpose:'English purpose',purpose_i18n:{'zh-TW':'自訂說明',en:'<b>English purpose</b>'},apis:{'SYNO.Core.Upgrade':{progress:[1]}}}}}))})
 try{app.mount(root);await nextTick();expect(root.querySelector('[data-webapi-profiles]')?.textContent).toContain('自訂說明');locale.value='fr';await nextTick();expect(root.querySelector('[data-webapi-profiles]')?.textContent).toContain('<b>English purpose</b>');expect(root.querySelector('[data-webapi-profiles] b')).toBeNull()}finally{app.unmount()}
})
