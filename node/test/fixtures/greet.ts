// An App as the runtime starts it, for run.test.ts.
import { App } from '../../src/index.js'

await new App()
  .method('hello.greet', (ctx) => {
    console.log('greeting', ctx.user)
    return `Hello, ${ctx.user}`
  })
  .run()
