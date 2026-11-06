/** Shell-facing wire types and validation, with no DOM, Vue or platform imports. */
export * from './contract.gen.js'
import {isNativeIntakeItem, type NativeIntakeItem} from './contract.gen.js'

/** The shell must use this policy before injecting and on every navigation/message. */
export function canInjectNativeBridge(nasOrigin: string, frameURL: string, isMainFrame: boolean): boolean {
 if(!isMainFrame) return false
 try {
  const nas = new URL(nasOrigin), frame = new URL(frameURL)
  return /^https?:$/.test(nas.protocol) && nas.origin === nasOrigin && frame.origin === nas.origin && !frame.username && !frame.password
 } catch {return false}
}

/** A URL download may never send WebView cookies to another origin, including on redirects. */
export function isNativeDownloadURL(nasOrigin: string, url: string): boolean {
 return canInjectNativeBridge(nasOrigin, url, true)
}

/** One store per NAS profile/session. Clear on sign-out or profile removal. */
export function createIntakeStore() {
 const entries = new Map<string, readonly NativeIntakeItem[]>()
 const used = new Set<string>()
 return {
  put(id: string, items: readonly NativeIntakeItem[]): void {
   if(!/^[A-Za-z0-9_-]{1,128}$/.test(id) || used.has(id) || !items.every(isNativeIntakeItem)) throw new TypeError('Invalid or duplicate intake')
   used.add(id)
   entries.set(id, items.map(item => ({...item})))
  },
  take(id: string): readonly NativeIntakeItem[] {
   const items = entries.get(id) ?? []
   entries.delete(id) // Delete before returning; replay after a reload also gets nothing.
   return items
  },
  clear(): void {entries.clear(); used.clear()},
 }
}
