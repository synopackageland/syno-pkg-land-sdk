import type {InjectionKey, ShallowRef} from 'vue'

/** Where AppShell's page header takes a page's own actions. */
export const PageActionsTarget: InjectionKey<Readonly<ShallowRef<HTMLElement | null>>> = Symbol('synopackageland:page-actions')
