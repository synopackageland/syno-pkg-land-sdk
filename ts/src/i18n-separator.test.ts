import {expect,test} from 'vitest'
import {labelSeparator} from './i18n'
test('Chinese and Japanese labels take a full-width colon',()=>{
 expect(labelSeparator('zh-TW')).toBe('：')
 expect(labelSeparator('cht')).toBe('：')
 expect(labelSeparator('ja')).toBe('：')
 expect(labelSeparator('en')).toBe(': ')
 expect(labelSeparator('ko')).toBe(': ')
})
import {joinSentences} from './i18n'
test('sentences without closing periods join into one paragraph in each language',()=>{
 expect(joinSentences(['DSM does not allow this','It needs system privileges'],'en')).toBe('DSM does not allow this. It needs system privileges')
 expect(joinSentences(['DSM 不允許','需要系統權限。'],'zh-TW')).toBe('DSM 不允許。需要系統權限')
 expect(joinSentences(['Ready?',undefined,false,'Go'],'ja')).toBe('Ready?Go')
 expect(joinSentences(['Bereit','Los'],'ger')).toBe('Bereit. Los')
})
