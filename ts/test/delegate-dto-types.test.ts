import {expectTypeOf,test} from 'vitest'
import type * as Public from '../src/index'
import type * as Wire from '../src/delegate/dtos-gen'

// vue-tsc checks these assertions. Replacing a re-export with a hand-written
// declaration must not let the facade/UI drift from the generated wire DTOs.
test('public delegate DTOs use the spec types',()=>{
 expectTypeOf<Public.SelectDestination>().toEqualTypeOf<Wire.SelectDestination>()
 expectTypeOf<Public.ScopeSelection>().toEqualTypeOf<Wire.ScopeSelection>()
 expectTypeOf<Public.DestinationState>().toEqualTypeOf<Wire.DestinationState>()
 expectTypeOf<Public.DestinationGrant>().toEqualTypeOf<Wire.DestinationGrant>()
 expectTypeOf<Public.DelegateOperationState>().toEqualTypeOf<Wire.DelegateOperationState>()
 expectTypeOf<Public.OperationRef>().toEqualTypeOf<Wire.OperationRef>()
 expectTypeOf<Public.OperationResult>().toEqualTypeOf<Wire.OperationResult>()
 expectTypeOf<Public.AppSaveJob>().toEqualTypeOf<Pick<Wire.MoveMirror,'job_id'|'delegation_id'|'name'|'refusal'>>()
 expectTypeOf<Omit<Public.SaveOperation,'delegation_id'|'name'>>().toEqualTypeOf<Wire.OperationResult>()
 expectTypeOf<Pick<Public.SaveOperation,'delegation_id'|'job_id'>>().toEqualTypeOf<Wire.OperationRef>()
 expectTypeOf<Public.SaveOperation['name']>().toEqualTypeOf<string>()
})
