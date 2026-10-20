import {readFileSync} from 'node:fs'
import {resolve} from 'node:path'
import {expect,test} from 'vitest'
import {generate} from '../scripts/gen-bridge-i18n.mjs'
import {bridgeErrorCopy,uiLocales} from '../src/index'

const spec=resolve(__dirname,'../../spec/bridge/v1/errors.json')

test('the committed bridge catalogs are what the generator makes from spec/bridge/v1/errors.json',()=>{
 const catalogs=generate(spec)
 expect(Object.keys(catalogs).sort()).toEqual([...uiLocales].sort())
 for(const locale of uiLocales)expect(readFileSync(resolve(__dirname,`../i18n/bridge/${locale}.json`),'utf8'),locale).toBe(catalogs[locale])
})

test('bridge codes read in the DSM language, and other codes have no bridge text',()=>{
 expect(bridgeErrorCopy({code:'app_unavailable'},'cht')).toBe('App 目前無法使用，請稍後再試。')
 expect(bridgeErrorCopy({code:'limit_exceeded'},'en')).toBe('The file is larger than allowed.')
 expect(bridgeErrorCopy({code:'read_failed'},'ger')).toBe('Die Datei kann nicht gelesen werden. Bitte versuchen Sie es später erneut.')
 expect(bridgeErrorCopy({code:'identity_unavailable'},'ja-JP')).toBe('このユーザー アカウントを識別できません。後でもう一度お試しください。')
 expect(bridgeErrorCopy({code:'request_timeout'},'cht')).toBe('請求處理太久已停止，請再試一次或縮小範圍。')
 expect(bridgeErrorCopy({code:'APP_STOPPED'},'cht')).toBe('App 已停止。請在套件中心啟動後再試一次。')
 expect(bridgeErrorCopy({code:'APP_STARTING'},'chs')).toBe('App 正在启动，请稍后重试。')
 expect(bridgeErrorCopy({code:'credential_not_ready'},'cht')).toBe('DSM 沒有提供此頁面的登入權杖。請以密碼重新登入 DSM，再從 DSM 桌面開啟 App。')
 expect(bridgeErrorCopy({code:'session_required'},'en')).toBeUndefined()
 expect(bridgeErrorCopy(new Error('x'),'en')).toBeUndefined()
 expect(bridgeErrorCopy(undefined,'en')).toBeUndefined()
})
