const values = new WeakMap<RequestSecret, string>()
const REDACTED = '[redacted]'

/** One request's external-service credential. Ordinary JSON and logs redact it. */
export class RequestSecret {
 constructor(value: string) {values.set(this, value); Object.freeze(this)}
 toString() {return REDACTED}
 toJSON() {return REDACTED}
 [Symbol.toPrimitive]() {return REDACTED}
 clear() {values.delete(this)}
}
export const requestSecret = (value: string) => new RequestSecret(value)

// Only the guarded client calls this encoder. Values are consumed before any
// network attempt; an explicit retry must obtain fresh credentials.
export function encodeSecretRequest(value: unknown, credentials: string[] = []): string {
 const used = new Set<RequestSecret>()
 try {
  return JSON.stringify(value, function(key, item: unknown) {
   const original: unknown = key === '' ? value : this[key]
   if(original instanceof RequestSecret) {
    const raw=values.get(original)
    if(raw===undefined)throw {code:'secret_consumed',message:'enter the credential again'}
    used.add(original)
    credentials.push(raw)
    return raw
   }
   return item
  })
 } finally {for(const secret of used)secret.clear()}
}

/** Detached diagnostic copy; never log raw bodies or fall back to the input. */
export function redactSecrets(value: unknown, credentials: readonly string[] = [], fields: readonly string[] = []): unknown {
 const names = ['password','passwd','passphrase','otp','token','secret','authorization','cookie','synotoken','sid']
 const sensitive = (key: string) => names.includes(key.toLowerCase()) || key.toLowerCase()==='x-syno-token' || fields.includes(key)
 const known = [...credentials].filter(Boolean).sort((a,b)=>b.length-a.length)
 const text = (value: string) => known.reduce((safe,secret)=>safe.split(secret).join(REDACTED), value)
 function walk(item: unknown): unknown {
  if(typeof item==='string')return text(item)
  if(Array.isArray(item))return item.map(walk)
  if(item && typeof item==='object')return Object.fromEntries(Object.entries(item).map(([key,value])=>[text(key),sensitive(key)?REDACTED:walk(value)]))
  return item
 }
 try {return walk(JSON.parse(JSON.stringify(value)))}
 catch {return REDACTED}
}
