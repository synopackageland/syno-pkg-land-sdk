import {readFileSync} from 'node:fs'
import {resolve} from 'node:path'
import {expect,test} from 'vitest'

const pkg=JSON.parse(readFileSync(resolve(__dirname,'../package.json'),'utf8'))
const installed=JSON.parse(readFileSync(resolve(__dirname,'../node_modules/@synopackageland/ui/package.json'),'utf8'))

// The phone shell and AppTabBar require the current UI component contract.
test('the Vue entry supports the UI native phone shell and is tested against stable UI',()=>{
 expect(pkg.peerDependencies['@synopackageland/ui']).toBe('>=0.20.5 <0.23.0')
 expect(pkg.devDependencies['@synopackageland/ui']).toBe('0.22.0')
 expect(installed.version).toBe('0.22.0')
})
