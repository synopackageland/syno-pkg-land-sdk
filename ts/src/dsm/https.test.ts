import {expect, test, vi} from 'vitest'
import {readDsmHttpsURL} from './https'

test.each([undefined, {name:'viewer', isAdmin:false}])('non-admin sessions never call PortInfo', async user => {
 const webApi = vi.fn()
 expect(await readDsmHttpsURL({user, webApi}, 'http://example.test:5000/')).toBeUndefined()
 expect(webApi).not.toHaveBeenCalled()
})

test('an admin link uses the declared HTTPS port and drops all page credentials and routing', async () => {
 const webApi = vi.fn(async () => ({port_info:[{port_id:'dms_https', protocol:'tcp', dst_port:['7443']}]}))
 expect(await readDsmHttpsURL({user:{name:'operator',isAdmin:true},webApi},
  'http://example.test:5000/webman/?SynoToken=private#route')).toBe('https://example.test:7443/webman/')
})

test.each([
 {port_info:[]}, {port_info:[{port_id:'dms_http',protocol:'tcp',dst_port:['5000']}]},
 {port_info:[{port_id:'dms_https',protocol:'udp',dst_port:['7443']}]},
 {port_info:[{port_id:'dms_https',protocol:'tcp',dst_port:['1-65535']}]},
 {port_info:[{port_id:'dms_https',protocol:'tcp',dst_port:['7443','8443']}]},
])('missing or ambiguous HTTPS declarations produce no guessed link', async ({port_info}) => {
 expect(await readDsmHttpsURL({user:{name:'operator',isAdmin:true},webApi:async()=>({port_info})},
  'http://example.test:5000/')).toBeUndefined()
})

test.each(['javascript:alert(1)', 'http://user:secret@example.test/', 'not a URL'])(
 'invalid page URLs never start discovery: %s', async page => {
  const webApi=vi.fn()
  expect(await readDsmHttpsURL({user:{name:'operator',isAdmin:true},webApi},page)).toBeUndefined()
  expect(webApi).not.toHaveBeenCalled()
 })
