// @vitest-environment jsdom
import {afterEach, expect, test, vi} from 'vitest'
import {createSession} from './session'
import type {Context} from '../context'
afterEach(()=>vi.useRealTimers())
function context(call: Context['call']){return {call} as Context}
test('discovers disabled identity without changing existing transport',async()=>{
 const call=vi.fn().mockRejectedValue({code:'unknown_method'})
 const session=createSession(context(call));await session.start();expect(session.enabled.value).toBe(false);expect(session.ready.value).toBe(true);session.dispose()
})
test('an Application Privileges refusal keeps the DSM shell instead of offering sign-in',async()=>{
 const call=vi.fn().mockRejectedValue({code:'app_privilege_denied'})
 const session=createSession(context(call));await session.start();expect(session.enabled.value).toBe(false);expect(session.notice.value).toBe('');session.dispose()
})
test('bootstraps, expires and returns to login after a business rejection',async()=>{
 vi.useFakeTimers();const expiresAt=new Date(Date.now()+60000).toISOString()
 const call=vi.fn().mockResolvedValueOnce({enabled:true}).mockResolvedValueOnce({user:'alice',uid:'1205',expiresAt})
 const session=createSession(context(call));await session.start();expect(session.subject.value?.user).toBe('alice')
 call.mockRejectedValueOnce({code:'session_required'})
 await expect(session.call('greeting.get')).rejects.toEqual({code:'session_required'});expect(session.subject.value).toBeUndefined()
 call.mockResolvedValueOnce({user:'alice',uid:'1205',expiresAt});await session.login('alice','password','123456');expect(session.subject.value?.user).toBe('alice')
 await vi.advanceTimersByTimeAsync(60000);expect(session.subject.value).toBeUndefined();session.dispose()
})
test('disposal ignores pending session responses and cancels timers',async()=>{
 let resolve: (value:unknown)=>void=()=>{};const call=vi.fn().mockResolvedValueOnce({enabled:true}).mockImplementationOnce(()=>new Promise(r=>{resolve=r}))
 const session=createSession(context(call));const pending=session.start();await Promise.resolve();session.dispose();resolve({user:'alice',expiresAt:new Date(Date.now()+60000).toISOString()});await pending;expect(session.subject.value).toBeUndefined()
})
test('OTP errors remain actionable and logout clears the identity',async()=>{
 const call=vi.fn().mockResolvedValueOnce({enabled:true}).mockRejectedValueOnce({code:'session_required'})
 const session=createSession(context(call));await session.start();call.mockRejectedValueOnce({code:'otp_required'})
 await expect(session.login('alice','p','')).rejects.toEqual({code:'otp_required'});expect(session.subject.value).toBeUndefined()
 call.mockResolvedValueOnce({user:'alice',expiresAt:new Date(Date.now()+60000).toISOString()});await session.login('alice','p','123456');call.mockResolvedValueOnce({loggedOut:true});await session.logout();expect(session.subject.value).toBeUndefined();session.dispose()
})
test('session_required from identity.status keeps the DSM shell without retrying',async()=>{
 vi.useFakeTimers();const call=vi.fn().mockRejectedValue({code:'session_required'})
 const session=createSession(context(call));await session.start();expect(session.enabled.value).toBe(false);expect(session.unavailable.value).toBe(false);expect(session.notice.value).toBe('')
 await vi.advanceTimersByTimeAsync(120000);expect(call).toHaveBeenCalledTimes(1);session.dispose()
})
test('a failed identity.status fails closed and re-probes with bounded backoff',async()=>{
 vi.useFakeTimers();const call=vi.fn().mockRejectedValue({code:'timeout'})
 const session=createSession(context(call),{retryDelays:[1000,2000]});await session.start()
 expect(session.ready.value).toBe(true);expect(session.enabled.value).toBe(false);expect(session.unavailable.value).toBe(true);expect(session.notice.value).toBe('unavailable')
 await vi.advanceTimersByTimeAsync(1000);expect(call).toHaveBeenCalledTimes(2)
 await vi.advanceTimersByTimeAsync(2000);expect(call).toHaveBeenCalledTimes(3)
 await vi.advanceTimersByTimeAsync(120000);expect(call).toHaveBeenCalledTimes(3);expect(session.enabled.value).toBe(false)
 session.dispose()
})
test('only a later successful {enabled:true} turns the App login on',async()=>{
 vi.useFakeTimers();const call=vi.fn().mockRejectedValueOnce({code:'http_error',status:502}).mockResolvedValueOnce({enabled:true}).mockRejectedValueOnce({code:'session_required'})
 const session=createSession(context(call),{retryDelays:[1000]});await session.start();expect(session.enabled.value).toBe(false)
 await vi.advanceTimersByTimeAsync(1000);expect(session.enabled.value).toBe(true);expect(session.unavailable.value).toBe(false);expect(session.notice.value).toBe('')
 session.dispose()
})
test('disposal cancels a pending re-probe',async()=>{
 vi.useFakeTimers();const call=vi.fn().mockRejectedValue({code:'timeout'})
 const session=createSession(context(call),{retryDelays:[1000]});await session.start();session.dispose()
 await vi.advanceTimersByTimeAsync(5000);expect(call).toHaveBeenCalledTimes(1)
})
test('identity:false skips the probe entirely',async()=>{
 const call=vi.fn()
 const session=createSession(context(call),{identity:false});await session.start()
 expect(call).not.toHaveBeenCalled();expect(session.ready.value).toBe(true);expect(session.enabled.value).toBe(false)
 await session.retry();expect(call).not.toHaveBeenCalled();session.dispose()
})
