import {onBeforeUnmount,watch} from 'vue'
import {lifecycleRetryDelay,type AppLifecycle} from './lifecycle'
// While a panel shows a stopped or starting package it checks again on its
// own, backing off (lifecycleRetryDelay), until check leaves the lifecycle.
// A stopped package that turns starting starts the backoff over, so the end
// of its start is seen soon. One check waits at a time.
export function useLifecycleRetry(lifecycle:()=>AppLifecycle|undefined,check:()=>Promise<unknown>){
 let disposed=false,retry:ReturnType<typeof setTimeout>|undefined,attempt=0
 function recheck(){clearTimeout(retry);retry=setTimeout(()=>{void check().then(()=>{if(!disposed&&lifecycle())recheck()})},lifecycleRetryDelay(attempt++,lifecycle()))}
 watch(lifecycle,down=>{clearTimeout(retry);attempt=0;if(down&&!disposed)recheck()})
 onBeforeUnmount(()=>{disposed=true;clearTimeout(retry)})
}
