import {inject,type ComputedRef,type InjectionKey,type Ref} from 'vue'
import type {AuthorizationStatus} from '../context'
/** 'checking' until the background status arrives, and while it is checked again. */
export type AuthorizationGateState=AuthorizationStatus['state']|'checking'
/** What the Gate shares with the App inside it, so no App repeats its checks. */
export interface AuthorizationControl{
 state:ComputedRef<AuthorizationGateState>
 /**
  * Call before a root operation. Resolves true once the authorization is ready;
  * otherwise the Gate's dialog opens, and it resolves true when the user
  * authorizes or false when the dialog is dismissed.
  */
 requireAuthorization():Promise<boolean>
 text:ComputedRef<(key:string)=>string>
 busy:Ref<boolean>
 canManage():boolean
 revoke():Promise<void>
}
export const AuthorizationKey:InjectionKey<AuthorizationControl>=Symbol('authorization')
export function useAuthorization():AuthorizationControl {
 const control=inject(AuthorizationKey,undefined)
 if(!control)throw Error('authorization controls must be placed inside AuthorizationGate')
 return control
}
