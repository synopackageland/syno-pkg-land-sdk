import {readdirSync,readFileSync} from 'node:fs'
import {expect,test} from 'vitest'
// the help text tells the user to press the button by its own label.
test('every locale names the Authorize button in its instructions',()=>{
 const dir=new URL('../../i18n/authorization/',import.meta.url)
 for(const file of readdirSync(dir)){
  const copy=JSON.parse(readFileSync(new URL(file,dir),'utf8'))
  const button=copy['authorization.authorize']
  for(const key of ['authorization.next.install','authorization.next.update'])expect(copy[key],`${file} ${key}`).toContain(button)
 }
})
// a page without DSM's SynoToken is also an Application Portal page,
// so the credential text names no restored session; every locale says to sign
// in again with the password and open the App from the DSM desktop.
test('the credential text fits a restored session and a portal page alike',()=>{
 const dir=new URL('../../i18n/authorization/',import.meta.url)
 const body=(locale:string)=>JSON.parse(readFileSync(new URL(`${locale}.json`,dir),'utf8'))['authorization.credential.body']
 expect(body('en')).toBe("DSM's sign-in information could not be obtained. Sign in to DSM again with your password, then open {package} from the DSM desktop.")
 expect(body('zh-TW')).toBe('無法取得 DSM 的驗證資訊。請以密碼重新登入 DSM，再從 DSM 桌面開啟 {package}。')
 expect(body('zh-CN')).toBe('无法获取 DSM 的验证信息。请使用密码重新登录 DSM，再从 DSM 桌面打开 {package}。')
 for(const file of readdirSync(dir)){
  const text=JSON.parse(readFileSync(new URL(file,dir),'utf8'))['authorization.credential.body']
  expect(text,file).toContain('{package}')
  expect(text,file).not.toMatch(/restor|還原|恢复|復元|복원|wiederhergestellt|restaur|ripristin/i)
 }
})
