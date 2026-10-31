import {readFileSync} from 'node:fs'
import {resolve} from 'node:path'
import {expect,test} from 'vitest'

const pkg=JSON.parse(readFileSync(resolve(__dirname,'../package.json'),'utf8'))
const installed=JSON.parse(readFileSync(resolve(__dirname,'../node_modules/@synopackageland/ui/package.json'),'utf8'))

// Button kinds, content-sized Dialogs and the PageContent section gap come from UI 0.10.1.
test('the Vue entry targets UI 0.10.x through 0.18.x and is tested against 0.18.2',()=>{
 expect(pkg.peerDependencies['@synopackageland/ui']).toBe('>=0.10.1 <0.19.0')
 expect(pkg.devDependencies['@synopackageland/ui']).toBe('0.18.2')
 expect(installed.version).toBe('0.18.2')
})
