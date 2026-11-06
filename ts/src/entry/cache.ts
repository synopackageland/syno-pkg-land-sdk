/** Display hints scoped to a package, account and sign-in. Never an authority cache. */
export function createSignInCache(namespace: string, storage: () => Storage | undefined = () => globalThis.localStorage) {
 if(!namespace) throw new Error('A sign-in cache needs a namespace')
 const prefix = `synopkgland.${namespace}.`
 // Same on HTTP, where WebCrypto is unavailable. A fingerprint distinguishes
 // sign-ins without persisting their credential; it is not proof of identity.
 function fingerprint(token: string) {
  let a = 0x811c9dc5, b = 0x9e3779b9
  for(let i = 0; i < token.length; i++) {
   a = Math.imul(a ^ token.charCodeAt(i), 0x01000193)
   b = Math.imul(b ^ token.charCodeAt(i), 0x85ebca6b)
  }
  return `${(a >>> 0).toString(16)}-${(b >>> 0).toString(16)}`
 }
 function remove(key: string) {try {storage()?.removeItem(prefix + key)} catch {/* optional storage */}}
 return {
  get<T>(key: string, token: string, validate: (value: unknown) => value is T, user?: string): T | undefined {
   try {
    const raw = storage()?.getItem(prefix + key)
    if(!raw) return undefined
    const entry = JSON.parse(raw)
    if(token && entry?.session === fingerprint(token) && entry.user === user && validate(entry.value)) return entry.value
   } catch {/* malformed or unavailable storage */}
   remove(key)
   return undefined
  },
  set(key: string, token: string, value: unknown, user?: string) {
   if(!token) {remove(key); return}
   try {storage()?.setItem(prefix + key, JSON.stringify({session: fingerprint(token), user, value}))} catch {/* optional storage */}
  },
  remove,
  clear() {
   try {
    const store = storage()
    if(!store) return
    for(let i = store.length - 1; i >= 0; i--) {
     const key = store.key(i)
     if(key?.startsWith(prefix)) store.removeItem(key)
    }
   } catch {/* optional storage */}
  },
 }
}
