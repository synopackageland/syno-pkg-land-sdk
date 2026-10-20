import {expect,test} from 'vitest'
import {readFileSync} from 'node:fs'
import {uiLocales} from '../i18n'
import {isAuthorizationDenied} from './failure'
import {appDenial,appRefusal} from './refusal'

test('only the two entry refusals identify how to regain App access',()=>{
 for(const code of ['app_privilege_denied','admin_required'] as const)expect(appDenial({code})).toBe(code)
 for(const code of ['APP_DENIED','PERMISSION_DENIED','cross_origin','origin_required','access_denied','toString'])expect(appDenial({code})).toBeUndefined()
 expect(appDenial(null)).toBeUndefined()
})
test('authorization checks retain every existing refusal classification',()=>{
 for(const code of ['app_privilege_denied','admin_required','APP_DENIED','PERMISSION_DENIED','cross_origin','origin_required'])expect(isAuthorizationDenied({code})).toBe(true)
 for(const status of [401,403])expect(isAuthorizationDenied({code:'bad_response',message:`HTTP ${status}`})).toBe(true)
 for(const code of ['network','unknown_method'])expect(isAuthorizationDenied({code})).toBe(false)
 expect(isAuthorizationDenied({code:'bad_response',message:'HTTP 4030'})).toBe(false)
})

// Entry refusals share one recovery message across every SDK surface.
test('App privilege guidance names the App and the DSM steps; admin access uses an administrator',()=>{
 const privilege=appRefusal({code:'app_privilege_denied'},'en','Download Hub')!
 expect(privilege.title).toBe('No access')
 expect(privilege.message).toBe('Ask an administrator to open Control Panel > Application Privileges, select Download Hub and click Edit, then allow your account or group.')
 expect(appRefusal({code:'app_privilege_denied'},'zh-TW','下載中心')?.message).toBe('請管理員到「控制台 > 應用程式權限」，選擇「下載中心」並按「編輯」，允許你的帳號或群組使用。')
 for(const name of [undefined,'','   ']){
  const message=appRefusal({code:'app_privilege_denied'},'en',name)!.message
  expect(message).toContain('select this app and click Edit')
  expect(message).not.toMatch(/undefined|\{package\}/)
 }
 const admin=appRefusal({code:'admin_required'},'en','Download Hub')!
 expect(admin.message).toBe('This app is only available to members of the administrators group. Open it with an administrator account.')
 expect(admin.message).not.toContain('Application Privileges')
 expect(appRefusal({code:'admin_required'},'zh-TW')?.message).toBe('此 App 僅供 administrators 群組的成員使用。請使用管理員帳號開啟。')
 for(const code of ['APP_DENIED','PERMISSION_DENIED','cross_origin','access_denied'])expect(appRefusal({code},'en')).toBeUndefined()
})
test('each locale contains actual recovery keys, including a body without a name',()=>{
 const keys=['app_privilege_denied.title','app_privilege_denied.body','app_privilege_denied.namedBody','admin_required.title','admin_required.body']
 for(const locale of uiLocales){
  const raw=JSON.parse(readFileSync(new URL(`../../i18n/authorization/${locale}.json`,import.meta.url),'utf8'))
  for(const key of keys)expect(raw[`authorization.${key}`],`${locale} ${key}`).toEqual(expect.any(String))
  for(const code of ['app_privilege_denied','admin_required'])expect(appRefusal({code},locale,'測試 App')?.message).not.toMatch(/\{package\}/)
 }
 expect(appRefusal({code:'app_privilege_denied'},'cht','下載中心')).toEqual(appRefusal({code:'app_privilege_denied'},'zh-TW','下載中心'))
})
// DSM 7.4 menu labels used by the localized recovery guidance.
const DSM_MENU_NAMES:Record<string,readonly [controlPanel:string,appPrivilege:string,edit:string]>={
 en:['Control Panel','Application Privileges','Edit'],
 'zh-TW':['控制台','應用程式權限','編輯'],
 'zh-CN':['控制面板','应用程序权限','编辑'],
 ja:['コントロール パネル','アプリケーションの権限','編集'],
 ko:['제어판','응용 프로그램 권한','편집'],
 de:['Systemsteuerung','Anwendungsberechtigungen','Bearbeiten'],
 fr:['Panneau de configuration','Privilèges d’application','Modifier'],
 es:['Panel de control','Privilegios de la aplicación','Editar'],
 it:['Pannello di controllo','Privilegi applicazione','Modifica'],
}
test('App privilege guidance names the DSM menu in every locale’s own language',()=>{
 for(const locale of uiLocales){
  const [controlPanel,appPrivilege,edit]=DSM_MENU_NAMES[locale]!
  for(const name of [undefined,'Download Hub']){
   const message=appRefusal({code:'app_privilege_denied'},locale,name)!.message
   const where=`${locale} ${name??'unnamed'}`
   expect(message,where).toContain(`${controlPanel} > ${appPrivilege}`)
   expect(message,where).toContain(edit)
  }
 }
})
