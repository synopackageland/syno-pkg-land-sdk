// @vitest-environment jsdom
import {expect, test, vi} from 'vitest'
import {createApp, h, nextTick} from 'vue'
import type {Context} from '../context'
import EntryGuard from './EntryGuard.vue'

const settle = async () => {for(let i = 0; i < 5; i++) {await nextTick(); await new Promise(r => setTimeout(r))}}

test('content renders before the check settles, then only a failure opens a dialog', async () => {
 let resolve!: (value: unknown) => void
 const ctx = {call: () => new Promise<unknown>(r => {resolve = r})} as Pick<Context, 'call'>
 const root = document.createElement('div')
 document.body.append(root)
 const app = createApp({render: () => h(EntryGuard, {ctx, mode: 'admin-only'}, () => h('p', 'Visible App page'))})
 app.mount(root)
 try {
  expect(root.textContent).toContain('Visible App page')
  expect(document.querySelector('[role=dialog]')).toBeNull()
  resolve({user: 'test-admin', admin: true})
  await settle()
  expect(document.querySelector('[role=dialog]')).toBeNull()
 } finally {app.unmount(); root.remove()}
})

test('failed background check shows a retry dialog without removing the page; unmount cleans listeners', async () => {
 let reopen: (() => void) | undefined
 const stop = vi.fn()
 let admin = false
 const ctx = {
  call: vi.fn(async () => ({user: 'test-user', admin})),
  reopen: (fn: () => void) => {reopen = fn; return stop},
 } as unknown as Pick<Context, 'call' | 'reopen'>
 const root = document.createElement('div')
 document.body.append(root)
 const app = createApp({render: () => h(EntryGuard, {ctx, mode: 'admin-only'}, () => h('p', 'Visible App page'))})
 app.mount(root)
 try {
  await settle()
  expect(root.textContent).toContain('Visible App page')
  expect(document.querySelector('[role=dialog]')?.textContent).toContain('Try again')
  admin = true
  reopen?.()
  await settle()
  expect(document.querySelector('[role=dialog]')).toBeNull()
 } finally {app.unmount(); root.remove()}
 expect(stop).toHaveBeenCalledTimes(1)
 const before = vi.mocked(ctx.call).mock.calls.length
 document.dispatchEvent(new Event('visibilitychange'))
 await settle()
 expect(vi.mocked(ctx.call).mock.calls.length).toBe(before)
})
