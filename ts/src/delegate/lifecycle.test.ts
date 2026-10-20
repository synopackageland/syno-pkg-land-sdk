import {expect,test} from 'vitest'
import {appLifecycle,lifecycleRetryDelay,LIFECYCLE_RETRY_MAX_MS,LIFECYCLE_STARTING_RETRY_MAX_MS} from './lifecycle'
import {delegateCopy} from './copy'

// a stopped or starting package waits for no approval.
test('a refusal or a status names a stopped or starting package',()=>{
 expect(appLifecycle({code:'APP_STOPPED'})).toBe('stopped')
 expect(appLifecycle({code:'APP_STARTING'})).toBe('starting')
 expect(appLifecycle({code:'EXECUTOR_STARTING'})).toBe('starting')
 // The DSM window's access: the executor status request was refused with the code.
 expect(appLifecycle({state:'blocked',code:'executor_unreachable',reason:'APP_STOPPED: the executor status request failed.'})).toBe('stopped')
 expect(appLifecycle({state:'blocked',code:'executor_starting',reason:"executor_starting: the executor's start step has not ended."})).toBe('starting')
 // The App's delegate.status (Go Service.Status) names the code as its reason.
 expect(appLifecycle({state:'blocked',reason:'APP_STARTING'})).toBe('starting')
})

test('anything else is not the package lifecycle',()=>{
 for(const value of [undefined,null,{},{code:'EXECUTOR_UNAVAILABLE'},{state:'needs-admin-approval'},{state:'blocked',reason:'executor-not-ready'},{state:'blocked',code:'executor_unreachable',reason:'request_timeout: the executor status request failed.'},{state:'blocked',reason:'APP_STOPPEDX'}])
  expect(appLifecycle(value),JSON.stringify(value)).toBeUndefined()
})

test('the check backs off to a ceiling',()=>{
 const delays=Array.from({length:8},(_,attempt)=>lifecycleRetryDelay(attempt))
 expect(delays[0]).toBe(2000)
 expect(delays.every((delay,i)=>i===0||delay>=delays[i-1]!)).toBe(true)
 expect(Math.max(...delays)).toBe(LIFECYCLE_RETRY_MAX_MS)
 expect(LIFECYCLE_RETRY_MAX_MS).toBeLessThanOrEqual(15000)
})

// a start step ends within a minute; checks that backed off to
// 15 s saw its end up to 15 s late. A starting package is checked at most 4 s apart.
test('a starting package is checked more often than a stopped one',()=>{
 const delays=Array.from({length:8},(_,attempt)=>lifecycleRetryDelay(attempt,'starting'))
 expect(delays[0]).toBe(2000)
 expect(Math.max(...delays)).toBe(LIFECYCLE_STARTING_RETRY_MAX_MS)
 expect(LIFECYCLE_STARTING_RETRY_MAX_MS).toBeLessThanOrEqual(4000)
 expect(lifecycleRetryDelay(7,'stopped')).toBe(LIFECYCLE_RETRY_MAX_MS)
})

// an App's own polling met these codes and kept a code table and text
// of its own. The headless entry exports the reading and the panels' text.
test('the package entry reads a refusal and words a stopped or starting package',async()=>{
 const {appLifecycle:read,lifecycleCopy}=await import('../index')
 expect(read({code:'APP_STOPPED'})).toBe('stopped')
 expect(lifecycleCopy('stopped','zh-TW')).toBe(delegateCopy('zh-TW').appStopped)
 expect(lifecycleCopy('starting','en')).toBe(delegateCopy('en').appStarting)
 expect(lifecycleCopy('starting')).toBe(delegateCopy('en').appStarting)
 for(const locale of ['en','zh-TW','zh-CN','ja','ko','de','fr','es','it'])for(const l of ['stopped','starting'] as const)expect(lifecycleCopy(l,locale)).toBeTruthy()
})

// a refused action (a download that was not added) is not sent again
// when the package runs, so "this page updates by itself" is not true of it.
test('an action refused while the package is stopped or starting says to try again',async()=>{
 const {lifecycleCopy}=await import('../index')
 expect(lifecycleCopy('stopped','zh-TW',{for:'action'})).toBe('此 App 已停止。請在套件中心啟動後再試一次。')
 expect(lifecycleCopy('starting','zh-TW',{for:'action'})).toBe('此 App 正在啟動，請稍候再試一次。')
 expect(lifecycleCopy('stopped','en',{for:'action'})).toBe('This app is stopped. Start it in Package Center, then try again.')
 expect(lifecycleCopy('stopped','zh-TW',{for:'page'})).toBe(delegateCopy('zh-TW').appStopped)
 for(const locale of ['en','zh-TW','zh-CN','ja','ko','de','fr','es','it'])for(const l of ['stopped','starting'] as const){
  const action=lifecycleCopy(l,locale,{for:'action'})
  expect(action,`${locale} ${l}`).toBeTruthy()
  expect(action,`${locale} ${l}`).not.toBe(lifecycleCopy(l,locale))
  if(locale!=='en')expect(action,`${locale} ${l}`).not.toBe(lifecycleCopy(l,'en',{for:'action'}))
 }
})
