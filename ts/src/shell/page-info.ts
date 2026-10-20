import type {InjectionKey, ShallowRef} from 'vue'

/** Where AppShell's page header takes a page's PageInfo: on the title line, after the title. */
export const PageInfoTarget: InjectionKey<Readonly<ShallowRef<HTMLElement | null>>> = Symbol('synopackageland:page-info')
/** The shell's locale, which names a PageInfo's button when the page passes none. */
export const ShellLocale: InjectionKey<Readonly<{value: string}>> = Symbol('synopackageland:shell-locale')
