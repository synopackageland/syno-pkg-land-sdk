// @vitest-environment jsdom
// / the regression test: an SDK panel whose call a stopped or starting package
// refused showed its generic error. Each error text says so instead, as an
// action to try again, when it is given the locale.
import {expect,test} from 'vitest'
import {saveCopy,saveError} from './save-copy'
import {errorCopy,delegateCopy} from './copy'
import {nasCopy,nasErrorCopy} from '../nas/locale'
import {backgroundCopy,backgroundErrorCopy} from '../background/i18n'
import {lifecycleCopy,lifecycleRefusal} from './lifecycle'

const texts:Array<[string,(error:unknown,locale?:string)=>string]>=[
 ['saveError',(e,l)=>saveError(e,saveCopy(l),'save',l)],
 ['saveError select',(e,l)=>saveError(e,saveCopy(l),'select',l)],
 ['DelegatePanel',(e,l)=>lifecycleRefusal(e,l)??errorCopy(e,delegateCopy(l))],
 ['nasErrorCopy',(e,l)=>nasErrorCopy(e,nasCopy(l??'en'),l)],
 ['backgroundErrorCopy',(e,l)=>backgroundErrorCopy(e,backgroundCopy(l??'en'),l)],
]
test('a refusal of a stopped or starting package is worded as an action to try again',()=>{
 for(const [name,text] of texts)for(const locale of ['en','zh-TW','de'])for(const [code,l] of [['APP_STOPPED','stopped'],['APP_STARTING','starting'],['EXECUTOR_STARTING','starting']] as const)
  expect(text({code},locale),`${name} ${locale} ${code}`).toBe(lifecycleCopy(l,locale,{for:'action'}))
})
test('without the locale, or for another code, the text is as before',()=>{
 expect(saveError({code:'APP_STOPPED'},saveCopy('zh-TW'))).toBe(saveCopy('zh-TW').error)
 expect(nasErrorCopy({code:'denied'},nasCopy('en'),'en')).toBe(nasCopy('en').errorDenied)
})
