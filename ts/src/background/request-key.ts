// getRandomValues works on DSM HTTP as well as HTTPS. These keys deduplicate
// requests; they do not authenticate a caller or authorize a background job.
export function requestKey(): string {
 return Array.from(crypto.getRandomValues(new Uint8Array(16)),byte=>byte.toString(16).padStart(2,'0')).join('')
}
