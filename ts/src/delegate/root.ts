// Results of the executor's root ops (spec: Root programs, Root jobs).
// Their params are generated in ../protocol/v1.ts.
import type {RootJobsGetParams} from '../protocol/v1'

// root.run answers output inline, or as a package-owned staging file:
// staging is its bare name (root-<32 hex>) in the package's
// var/executor-staging, which the server-side App reads and removes.
export type RootRunResult={request_id:string;exit_code:number;stderr:string}&({output:string}|{staging:string;size:number})
export type RootJobState='running'|'stopping'|'succeeded'|'failed'|'canceled'|'timed-out'|'interrupted'
export type RootJobRef=RootJobsGetParams
export interface RootJobProgress {percent?:number;message?:string;phase?:string}
export interface RootJob {
 job_id:string
 name:string
 state:RootJobState
 requested_by:string
 started_at:string
 ended_at?:string
 exit_code?:number
 cancel_reason?:string
 progress?:RootJobProgress
 result?:unknown
 output:string
}
export type RootStartResult={job_id:string;state:'running'}
// root.jobs.list answers summaries; root.jobs.get the whole job.
export type RootJobSummary=Omit<RootJob,'progress'|'result'|'output'>&{progress?:{percent?:number}}
