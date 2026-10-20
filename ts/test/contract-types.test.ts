import {expectTypeOf,test} from 'vitest'
import type {AuthorizationStatus,CallOptions,Context,FilesCancelOperationParams,FilesOperationStatusParams,OperationRef,RootJob,RootJobRef,RootJobsGetParams,RootJobState,RootRunResult,RootStartResult,RootJobSummary} from '../src/index'

// The App backend forwards an operation reference unchanged to the executor,
// so the UI type must not drift from the generated protocol params.
test('an operation reference is exactly the executor status and cancel params',()=>{
 expectTypeOf<OperationRef>().toEqualTypeOf<FilesOperationStatusParams>()
 expectTypeOf<OperationRef>().toEqualTypeOf<FilesCancelOperationParams>()
})

// App contract fixtures reply reason: null when there is no reason.
test('an authorization status may carry a null reason',()=>{
 const status:AuthorizationStatus={command:'authorization',schema_version:1,state:'ready',reason:null}
 expectTypeOf(status.reason).toEqualTypeOf<string|null|undefined>()
})

// A root job reference is what root.jobs.get and root.jobs.stop take.
test('a root job is the executor job record',()=>{
 expectTypeOf<RootJobRef>().toEqualTypeOf<RootJobsGetParams>()
 const job:RootJob={job_id:'c'.repeat(64),name:'clean',state:'running',requested_by:'alice',started_at:'2026-10-13T05:00:00Z',progress:{percent:40,phase:'scan'},output:''}
 expectTypeOf(job.state).toEqualTypeOf<RootJobState>()
 expectTypeOf<RootJobState>().toEqualTypeOf<'running'|'stopping'|'succeeded'|'failed'|'canceled'|'timed-out'|'interrupted'>()
 const inline:RootRunResult={request_id:'r',exit_code:3,stderr:'',output:'{}'}
 const staged:RootRunResult={request_id:'r',exit_code:0,stderr:'',staging:'root-'+'a'.repeat(32),size:10}
 expectTypeOf(inline.exit_code).toEqualTypeOf<number>()
 expectTypeOf(staged).toMatchTypeOf<RootRunResult>()
 const summary:RootJobSummary={job_id:'c'.repeat(64),name:'clean',state:'succeeded',requested_by:'alice',started_at:'2026-10-13T05:00:00Z',progress:{percent:100}}
 expectTypeOf(summary).not.toHaveProperty('output')
 expectTypeOf<RootStartResult>().toEqualTypeOf<{job_id:string;state:'running'}>()
})

// The DSM window's call takes {signal}: an abort rejects with request_canceled,
// as the client timeout rejects with request_timeout.
test('a call takes an optional AbortSignal as its third argument',()=>{
 expectTypeOf<CallOptions>().toEqualTypeOf<{signal?:AbortSignal}>()
 expectTypeOf<Parameters<Context['call']>>().toEqualTypeOf<[method:string,params?:Record<string,unknown>,options?:CallOptions]>()
 expectTypeOf<Context['call']>().toBeCallableWith('m',{},{signal:new AbortController().signal})
})
