import type {Context} from '../context'
import {createNasProvider} from '../nas/provider'
import type {NasProvider} from '../nas/contract'

// Metadata comes from DSM in the browser; no bg.browse session relay.
export function createBackgroundPickerProvider(ctx:Pick<Context,'call'|'files'|'requestTimeout'>):NasProvider{
 return createNasProvider(ctx)
}
