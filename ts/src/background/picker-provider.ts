import type {Context} from '../context'
import {createNasProvider} from '../nas/provider'
import type {NasProvider} from '../nas/contract'

// Jobs without files use the same session-only metadata picker. No grant is stored.
export function createBackgroundPickerProvider(ctx:Pick<Context,'call'|'requestTimeout'>):NasProvider{
 return createNasProvider({requestTimeout:ctx.requestTimeout,call:<T>(method:string,params?:Record<string,unknown>)=>{
  if(method==='nas.files.shares')return ctx.call<T>('bg.browse',{offset:params?.offset})
  if(method==='nas.files.list')return ctx.call<T>('bg.browse',{folder:params?.path,offset:params?.offset})
  return ctx.call<T>(method,params)
 }})
}
