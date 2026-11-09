import {test} from 'node:test'
import assert from 'node:assert/strict'
import {mkdtempSync,mkdirSync,writeFileSync,rmSync} from 'node:fs'
import {tmpdir} from 'node:os'
import {join} from 'node:path'
import {SharedFolderConfig} from '../src/index.js'

test('native folder binding forwards only the declared key and private package paths',async()=>{
 const root=mkdtempSync(join(tmpdir(),'shared-folder-'))
 try {
  mkdirSync(join(root,'install'))
  writeFileSync(join(root,'install/control'),`#!/bin/sh
[ "$1" = folder-path ] && [ "$3" = '${root}' ] && [ "$5" = ExampleApp ] && [ "$7" = data ] && [ "$SYNOPKG_PKGVAR" = '${root}/private' ] || exit 1
printf '%s' '{"path":"/volume2/Media/Projects/Deep"}'
`,{mode:0o755})
  const config=new SharedFolderConfig({packageId:'ExampleApp',payloadRoot:root,dataDir:join(root,'private')})
  assert.equal(await config.require('data'),'/volume2/Media/Projects/Deep')
  await assert.rejects(config.require('../escape'),TypeError)
  writeFileSync(join(root,'install/control'),`#!/bin/sh\necho '{"error":{"code":"shared_folder_permission","message":"File Station > Media/Projects/Deep > Properties > Permission"}}'\nexit 2\n`,{mode:0o755})
  await assert.rejects(config.require('data'),(err:any)=>err.code==='shared_folder_permission'&&err.message.includes('File Station'))
  writeFileSync(join(root,'install/control'),'#!/bin/sh\nprintf \'{"path":"relative"}\'\n',{mode:0o755})
  await assert.rejects(config.require('data'),(err:any)=>err.code==='shared_folder_unavailable')
 } finally {rmSync(root,{recursive:true,force:true})}
})
