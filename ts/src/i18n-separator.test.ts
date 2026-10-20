import {expect,test} from 'vitest'
import {labelSeparator} from './i18n'
test('Chinese and Japanese labels take a full-width colon',()=>{
 expect(labelSeparator('zh-TW')).toBe('：')
 expect(labelSeparator('cht')).toBe('：')
 expect(labelSeparator('ja')).toBe('：')
 expect(labelSeparator('en')).toBe(': ')
 expect(labelSeparator('ko')).toBe(': ')
})
