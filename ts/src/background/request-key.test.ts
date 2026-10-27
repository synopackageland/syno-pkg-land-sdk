import {afterEach,expect,test,vi} from 'vitest'
import {requestKey} from './request-key'

afterEach(()=>vi.unstubAllGlobals())
test('HTTP DSM keys use getRandomValues when randomUUID and subtle are absent',()=>{
 const getRandomValues=crypto.getRandomValues.bind(crypto)
 vi.stubGlobal('crypto',{getRandomValues,randomUUID:undefined,subtle:undefined})
 const keys=Array.from({length:100},()=>requestKey())
 expect(keys.every(key=>/^[a-f0-9]{32}$/.test(key))).toBe(true)
 expect(new Set(keys).size).toBe(keys.length)
})
