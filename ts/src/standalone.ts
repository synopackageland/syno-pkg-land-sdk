// A host and a context for a page that owns the browser page outside DSM: a
// development page, a preview or a test. Only what a browser page can do is
// supported; the DSM window's desktop actions answer unsupported.
import type {Context, HostAdapter, HostPlatformSnapshot} from './context'

const unsupported = {status: 'unsupported'} as const

/** A browser page's host; platform fields override the snapshot, say to stand in for a DSM window in a test. */
export function createStandaloneHost(platform: Partial<HostPlatformSnapshot> = {}): HostAdapter {
 const snapshot: HostPlatformSnapshot = {web: true, host: 'standalone-web', dsmStandalone: null, dsmLanguage: null, appLocale: null, systemVersion: null, fullVersion: null, theme: null, ...platform}
 const disposers = new Set<() => void>()
 return {
  platform: () => ({...snapshot}),
  setTitle(title) {
   if(typeof document === 'undefined') return {status: 'unavailable'}
   document.title = title
   return {status: 'ok', target: 'document'}
  },
  activateWindow: () => unsupported,
  requestClose: () => unsupported,
  openApp: () => unsupported,
  openURL(url) {
   let parsed: URL
   try {parsed = new URL(url, typeof location === 'undefined' ? undefined : location.href)} catch {return {status: 'invalid-input'}}
   if(parsed.protocol !== 'https:' && parsed.protocol !== 'http:') return {status: 'invalid-input'}
   if(typeof window === 'undefined') return {status: 'unavailable'}
   return {status: 'requested', windowHandleAvailable: window.open(parsed.href, '_blank', 'noopener') !== null}
  },
  openFileManager: () => unsupported,
  revealFile: () => unsupported,
  openFile: () => unsupported,
  showFileDetails: () => unsupported,
  openAppSetting: () => unsupported,
  subscribe(event, listener) {
   if(event !== 'window-resize') return unsupported
   if(typeof window === 'undefined') return {status: 'unavailable'}
   window.addEventListener('resize', listener)
   const dispose = () => {window.removeEventListener('resize', listener); disposers.delete(dispose)}
   disposers.add(dispose)
   return {status: 'subscribed', dispose}
  },
  dispose() {for(const dispose of [...disposers]) dispose()},
 }
}

/** A full-page Context around the App's own call: English, a standalone host and no reopen events unless given. */
export function createStandaloneContext(fields: Pick<Context, 'call'> & Partial<Omit<Context, 'call'>>): Context {
 return {locale: 'en', fullPage: true, host: createStandaloneHost(), reopen: () => () => {}, ...fields}
}
