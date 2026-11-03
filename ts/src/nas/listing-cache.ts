import type {NasEntry,NasProvider} from './contract'

// Folder listings a provider already returned, kept for the provider's life:
// a picker shows them at once and lists again in the background. Only a
// listing's first pages are kept, under its sort; Refresh drops them all.
export interface Listing {entries:NasEntry[]; next?:number}
const caches=new WeakMap<NasProvider,Map<string,Listing>>()
export function listingCache(provider:NasProvider):Map<string,Listing>{
 let cache=caches.get(provider)
 if(!cache){cache=new Map();caches.set(provider,cache)}
 return cache
}
export const listingKey=(path:string,sortBy='name',direction='ASC')=>`${sortBy}\n${direction}\n${path}`
