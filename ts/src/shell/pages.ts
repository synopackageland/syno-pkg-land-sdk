// The DSM-style shell shows one sidebar entry per feature that this build
// actually provides. The manifest is resolved at build time, so the entries
// here are driven by runtime feature flags rather than by the YAML itself.
export type ShellPage = 'status' | 'background'
export interface ShellFeatureFlags {background?: boolean}
const ORDER: readonly ShellPage[] = ['status','background']

export function shellPages(features: ShellFeatureFlags): ShellPage[] {
  return ORDER.filter(page => page === 'status' || features[page] === true)
}

/** A page id, or an App's page with an id that may be left out of the sidebar. */
export type PageRef<T extends string = string> = T | {readonly id: T; readonly hidden?: boolean}
const pageId = <T extends string>(page: PageRef<T>): T => typeof page === 'string' ? page : page.id

/** Keeps a selection that still exists; otherwise the first page the sidebar shows wins. */
export function activePage<T extends string>(current: string | undefined, pages: readonly PageRef<T>[]): T {
  const kept = pages.find(page => pageId(page) === current)
  const first = pages.find(page => typeof page === 'string' || !page.hidden)
  return pageId(kept ?? first ?? ('status' as T))
}
