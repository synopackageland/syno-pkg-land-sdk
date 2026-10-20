import {expect,test} from 'vitest'
import {saveCopy,saveError} from './save-copy'
test('formal executor refusals explain destination and identity recovery in every locale',()=>{
 for(const locale of ['en','zh-TW','zh-CN','ja','ko','de','fr','es','it']){
  const copy=saveCopy(locale)
  expect(saveError({code:'destination-exists'},copy)).toBe(copy.conflict)
  expect(saveError({code:'DELEGATION_DENIED'},copy)).toBe(copy.denied)
  expect(saveError({code:'IDENTITY_CHANGED'},copy)).toBe(copy.denied)
  expect(saveError({code:'NO_SPACE'},copy)).toBe(copy.space)
  expect(saveError({code:'EXECUTOR_TIMEOUT'},copy)).toBe(copy.error)
  // bugs 1-2: these keep the grant, so they never ask to choose again.
  expect(saveError({code:'PERMISSION_DENIED'},copy)).toBe(copy.permission)
  expect(saveError({code:'not-found'},copy)).toBe(copy.missing)
  // an Application Privileges rule refuses the save, not the grant.
  expect(saveError({code:'APP_DENIED'},copy)).toBe(copy.appPrivilege)
  expect(copy.appPrivilege).toBeTruthy()
  expect(copy.permission).toBeTruthy()
  expect(copy.missing).toBeTruthy()
 }
})
// choosing a folder that DSM refuses creates no grant, so the copy
// never says the folder need not be chosen again.
test('a refused folder choice has its own copy in every locale',()=>{
 const en=saveCopy('en')
 for(const locale of ['en','zh-TW','zh-CN','ja','ko','de','fr','es','it']){
  const copy=saveCopy(locale)
  expect(saveError({code:'APP_DENIED'},copy,'select')).toBe(copy.appPrivilegeSelect)
  expect(saveError({code:'PERMISSION_DENIED'},copy,'select')).toBe(copy.permissionSelect)
  expect(saveError({code:'DENIED'},copy,'select')).toBe(copy.denied)
  for(const key of ['appPrivilegeSelect','permissionSelect'] as const){
   expect(copy[key]).toBeTruthy()
   expect(locale==='en'||copy[key]!==en[key],`${locale} ${key} translated`).toBe(true)
  }
 }
 expect(en.appPrivilegeSelect).not.toContain('do not need to choose')
 expect(en.permissionSelect).not.toContain('do not need to choose')
})
