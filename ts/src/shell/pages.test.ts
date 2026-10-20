import {expect,test} from 'vitest'
import {activePage,shellPages} from './pages'

test('a selection among the built-in pages stays while it exists',()=>{
 const pages=shellPages({background:true})
 expect(activePage('background',pages)).toBe('background')
 expect(activePage('background',shellPages({}))).toBe('status')
 expect(activePage(undefined,pages)).toBe('status')
})

test("an App's own page ids keep a selection that still exists",()=>{
 const pages=[{id:'home'},{id:'vpn'},{id:'scan',hidden:true}] as const
 expect(activePage('vpn',pages)).toBe('vpn')
 expect(activePage('scan',pages)).toBe('scan')
 expect(activePage('gone',['home','vpn'])).toBe('home')
})

test('a selection that is gone falls back to the first page the sidebar shows',()=>{
 expect(activePage('gone',[{id:'scan',hidden:true},{id:'home'},'background'])).toBe('home')
 expect(activePage(undefined,['status',{id:'vpn'}])).toBe('status')
})
