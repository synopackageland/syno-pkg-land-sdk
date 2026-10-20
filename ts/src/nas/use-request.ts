import {onBeforeUnmount,ref} from 'vue'

// The provider owns the 22 s bound (backend handlers are bounded to 20 s).
// Local cancellation suppresses late display updates, never promises rollback.
export function useNasRequest(){
 const busy=ref(false),error=ref<unknown>()
 let disposed=false,generation=0,active:AbortController|undefined
 function cancel(){generation++;active?.abort();active=undefined;busy.value=false;error.value=undefined}
 async function run<T>(fn:(signal:AbortSignal)=>Promise<T>):Promise<T|undefined>{
  if(disposed||busy.value)return
  const version=++generation,controller=new AbortController();active=controller;busy.value=true;error.value=undefined
  try{const value=await fn(controller.signal);return !disposed&&version===generation?value:undefined}
  catch(e){if(!disposed&&version===generation)error.value=e;return undefined}
  finally{if(!disposed&&version===generation){busy.value=false;active=undefined}}
 }
 onBeforeUnmount(()=>{disposed=true;cancel()})
 return {busy,error,run,cancel}
}
