import type {InjectionKey, ShallowRef} from 'vue'
import type {DropdownMenuEntry} from '@synopackageland/ui'

/** Where AppShell's page header takes a page's PageMeta: at the start of the page bar (or the title line), before its PageInfo. */
export const PageMetaTarget: InjectionKey<Readonly<ShallowRef<HTMLElement | null>>> = Symbol('synopackageland:page-meta')

/** The phone bar's "…" menu, which takes a PageMeta's `menu` in place of its controls. */
export interface PageMetaMenuHost {
 /** True while the shell is in the mobile layout: a PageMeta with a `menu` then hides its controls. */
 readonly collapsed: Readonly<{value: boolean}>
 /** Adds the entries to the bar's menu; a selection of one of their keys calls `select`. Returns the removal. */
 register(entries: () => readonly DropdownMenuEntry[], select: (key: string) => void): () => void
}
export const PageMetaMenu: InjectionKey<PageMetaMenuHost> = Symbol('synopackageland:page-meta-menu')

/** Every key in the entries, submenus included. */
export function entryKeys(entries: readonly DropdownMenuEntry[]): string[] {
 return entries.flatMap(entry => entry.type === 'separator' ? [] : [entry.key, ...('children' in entry && entry.children ? entryKeys(entry.children) : [])])
}
