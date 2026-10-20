import {expect,test} from 'vitest'
import {confirmFailure,passwordRejected} from './use-executor-setup'

test('confirmFailure reads the code, and an older CLI\'s English reason',()=>{
 expect(confirmFailure({state:'blocked',code:'password_rejected',reason:'x'})).toBe('password_rejected')
 expect(confirmFailure({state:'blocked',code:'password_required'})).toBe('password_required')
 expect(confirmFailure({state:'blocked',code:'password_confirm_failed'})).toBe('password_confirm_failed')
 expect(confirmFailure({state:'blocked',code:'scheduler_denied',reason:'dsm_3121: looks like a password'})).toBeUndefined()
 expect(confirmFailure({state:'blocked',reason:'dsm_3121: DSM rejected the administrator password. Nothing was created.'})).toBe('password_rejected')
 expect(confirmFailure({state:'blocked',reason:'dsm_4100: administrator password confirmation failed. Nothing was created.'})).toBe('password_confirm_failed')
 expect(confirmFailure({state:'needs-admin-approval',code:'password_rejected'})).toBeUndefined()
 expect(passwordRejected({state:'blocked',code:'password_rejected'})).toBe(true)
 expect(passwordRejected({state:'blocked',code:'password_required'})).toBe(false)
})
