import {readFileSync,readdirSync} from 'node:fs'
import {join,relative,resolve} from 'node:path'
import {parse} from 'vue/compiler-sfc'
import {expect,test} from 'vitest'

// A <template> without v-if, v-else(-if), v-for or v-slot is not a Vue fragment:
// it renders as the browser's inert <template>, which shows none of its children.
// jsdom keeps those children queryable, so only this source check catches it.
interface Node{type:number;tag?:string;tagType?:number;loc:{start:{line:number}};children?:Node[];branches?:Node[]}
const ELEMENT=1,PLAIN=0
const vueFiles=(dir:string):string[]=>readdirSync(dir,{withFileTypes:true}).flatMap(e=>e.isDirectory()?vueFiles(join(dir,e.name)):e.name.endsWith('.vue')?[join(dir,e.name)]:[])
function nativeTemplates(node:Node):number[]{
 const own=node.type===ELEMENT&&node.tag==='template'&&node.tagType===PLAIN?[node.loc.start.line]:[]
 return [...own,...[...node.children??[],...node.branches??[]].flatMap(nativeTemplates)]
}
test('no component renders a native <template>',()=>{
 const src=resolve(__dirname,'../src')
 const found=vueFiles(src).flatMap(file=>{
  const {descriptor}=parse(readFileSync(file,'utf8'),{filename:file})
  return descriptor.template?nativeTemplates(descriptor.template.ast as unknown as Node).map(line=>`${relative(src,file)}:${line}`):[]
 })
 expect(found).toEqual([])
})
