// A minimal App: one method that reads the caller, one that writes a file in
// the package's data directory (SYNOPKGLAND_DATA_DIR, set by the runtime).
import fs from 'node:fs'
import path from 'node:path'
import { App, AppError } from '@synopackageland/app'

const app = new App()
app.method('hello.greet', (ctx) => ({ message: `Hello, ${ctx.user}` }))
app.method('hello.save', (ctx, params) => {
  if (typeof params?.note !== 'string') throw new AppError('bad_request', 'note must be a string')
  const notes = path.join(process.env.SYNOPKGLAND_DATA_DIR, 'notes')
  fs.mkdirSync(notes, { recursive: true, mode: 0o700 })
  const content = Buffer.from(params.note)
  fs.writeFileSync(path.join(notes, `${ctx.requestId}.txt`), content, { flag: 'wx' })
  return { bytes: content.length }
})
await app.run()
