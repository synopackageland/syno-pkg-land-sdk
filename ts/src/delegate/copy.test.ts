import {expect,test} from 'vitest'
import {delegateCopy} from './copy'
import {saveCopy} from './save-copy'
import zhCN from '../../i18n/delegate/zh-CN.json'
import saveJa from '../../i18n/save/ja.json'
test('delegate and save copy follow the DSM language across all nine catalogs',()=>{
 expect(delegateCopy('chs')).toMatchObject(zhCN)
 expect(delegateCopy('zh_CN')).toMatchObject(zhCN)
 expect(saveCopy('jpn')).toEqual(saveJa)
 expect(delegateCopy('unknown').cancel).toBe('Cancel job')
 expect(saveCopy().folder).toBe('Save folder')
})
