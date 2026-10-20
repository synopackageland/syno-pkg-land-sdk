// App backend wire DTOs are generated from spec/delegate/v1.
import type {ScopeSelection,DestinationState,DestinationGrant,OperationRef,OperationResult,SelectDestination} from './dtos-gen'
export type {SelectDestination,ScopeSelection,DestinationState,DestinationGrant,DelegateOperationState,OperationRef,OperationResult} from './dtos-gen'
// Display metadata comes from App-owned jobs; only OperationResult is executor evidence.
export interface SaveOperation extends OperationResult,OperationRef {name:string}
// createBackgroundSaveFacade binds these methods to the App backend and App-owned metadata.
// No method accepts an owner/UID selector.
export interface BackgroundSaveStatus {disabled:boolean;selection?:ScopeSelection|null;scopeMode?:string;preselection?:string;mismatch?:boolean}
export interface DestinationSelection {
 key:string
 selectDestination(input:SelectDestination):Promise<DestinationGrant>
}
export interface BackgroundSaveFacade {
 destination(key:string):Promise<DestinationState>
 exportExample(input:{key:string;job_id:string}):Promise<OperationRef>
 selectDestination(input:SelectDestination):Promise<DestinationGrant>
 status():Promise<BackgroundSaveStatus>
 list():Promise<DestinationGrant[]>
 operations():Promise<SaveOperation[]>
 revoke(input:{delegation_id:string}):Promise<{revoked:true}>
 // Same-ref and idempotent. copying+CANCEL_REQUESTED is an accepted cancel, not
 // completion: poll operations() until canceled, or committed if publish won.
 cancel(input:OperationRef):Promise<OperationResult>
 disable():Promise<{disabled:true}>
 enable():Promise<{disabled:false}>
}
