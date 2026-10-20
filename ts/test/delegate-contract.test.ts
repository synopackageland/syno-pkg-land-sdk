import {readFileSync} from 'node:fs'
import {resolve} from 'node:path'
import {expect,test} from 'vitest'
import {encodeRequest,protocolSchemas} from '../src/index'
import type {DelegateOperationState,DestinationGrant,OperationResult,SaveOperation,SelectDestination} from '../src/delegate/long-running'
import type {FilesMoveFromStagingParams} from '../src/protocol/v1'

// The frozen W1a vector of the background-save routes (fixtures/delegate-contract/README.md).
const fixture=JSON.parse(readFileSync(resolve(__dirname,'fixtures/delegate-contract/v1.json'),'utf8'))
const STATES:DelegateOperationState[]=['queued','copying','committed','canceled','interrupted','uncertain']

test('the v1 delegate vector matches the DTOs and the executor request',()=>{
 const select:SelectDestination=fixture.select
 const grant:DestinationGrant=fixture.grant
 const move:FilesMoveFromStagingParams=fixture.move
 expect(Object.keys(select).sort()).toEqual(['key','path'])
 expect(select.path,'home scope uses the File Station virtual path').toBe('/home/exports')
 expect(grant.path).toBe(select.path)
 expect(grant.lifetime).toBe('until-revoked')
 expect(Object.keys(move).sort()).toEqual(['delegation_id','job_id','name','size','staging'])
 expect(protocolSchemas['files.moveFromStaging'].required).toEqual(['delegation_id','job_id','staging','name','size'])
 expect(JSON.parse(encodeRequest('files.moveFromStaging',move))).toEqual(fixture.request)
 expect(fixture.states).toEqual(STATES)
 const committed:OperationResult=fixture.committed.result
 expect(committed.receipt!.path).toBe(`${select.path}/${move.name}`)
})

test('the status and operations routes take empty params; a mirror is not live completion evidence',()=>{
 expect(fixture.status.method).toBe('delegate.status')
 expect(fixture.status.params).toEqual({})
 expect(fixture.status.result).toMatchObject({state:'ready',disabled:false,lifetime:'until-revoked'})
 expect(fixture.operations.method).toBe('delegate.operations.list')
 expect(fixture.operations.params).toEqual({})
 const mirror:Omit<SaveOperation,'receipt'>=fixture.operations.result.operations[0]
 expect(Object.keys(mirror).sort()).toEqual(['delegation_id','job_id','name','state'])
 expect(mirror).toMatchObject({job_id:fixture.move.job_id,delegation_id:fixture.move.delegation_id,state:'uncertain'})
})
