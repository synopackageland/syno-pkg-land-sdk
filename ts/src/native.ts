// Native products with their own pinned root tasks render these runtimes into
// their own DSM window; generated Apps use the protected executor instead.
// Importing this entry creates no task or grant, and it never imports vue.
export {ROOT_RUNTIME, type RootTask} from './native/root-runtime'
export {AUTHORIZATION_RUNTIME} from './native/authorization-runtime'
