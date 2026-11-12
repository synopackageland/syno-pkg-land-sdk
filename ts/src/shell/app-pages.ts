// The pages an AppShell lays out. An App lists its own pages; the built-in
// 'status' and 'background' ids stay available as ready-made entries.
import type {Component} from 'vue'
import type {AppSidebarGroup, AppTabBarItem} from '@synopackageland/ui'
import {Info, Timer} from '@lucide/vue'
import type {ShellCopy} from './i18n'
import type {ShellPage} from './pages'

export interface AppShellPage {
  id: string
  /** Sidebar label and page title, already in the App's language. */
  title: string
  icon?: Component
  /** Pages that share a label sit together under it, in first-seen order. */
  group?: string
  badge?: string | number
  /** Rendered for this page; without one the default slot renders it. */
  component?: Component
  /** Reachable through v-model but not listed in the sidebar. */
  hidden?: boolean
  /** 'bottom': listed after every other page, in a group of its own (an App's settings). */
  placement?: 'bottom'
  /** @deprecated Not shown: a page has no subtitle. */
  description?: string
  /**
   * Omitted: no visible title (the sidebar names the page) and a visually
   * hidden h1; the page's actions and notes sit in a bar above it.
   * true: the shell's PageHeader with the title, its notes and actions.
   * false: the page draws its own header; the shell adds no title.
   */
  header?: boolean
  /**
   * The body fills the height under the notices and the page does not scroll:
   * the content scrolls itself (a treemap, a table paged by height, a log).
   * Omitted: a page that shows a SettingsPage or a SectionTabs fill fills,
   * any other page scrolls as a whole. false: the page always scrolls.
   */
  fill?: boolean
  /** false keeps the phone bars visible on this page; omitted uses the layout default. */
  hideOnScroll?: boolean
  /**
   * true keeps the phone's title bar on this page while it is a tab page (a
   * recording or live screen shown as a tab). Omitted: a tab page has no title
   * row on a phone, since the tab bar already names it.
   */
  phoneTitle?: boolean
}
export type AppShellPageEntry = ShellPage | AppShellPage

/** PageContent surface for both the current page and the denied page. */
export type AppShellPageSurface = 'auto' | 'flat' | 'card' | 'none'

/** The page a denied shell shows; each field falls back to the shell's own words. */
export interface AppShellDenied {
  /** Backend entry refusal code; other codes keep the generic denial copy. */
  code?: string
  title?: string
  /** The default body; the denied slot replaces it. */
  message?: string
  /** false: the denied slot draws its own header. */
  header?: boolean
}

const builtInIcons: Record<ShellPage, Component> = {status:Info,background:Timer}

export function resolvePages(entries: readonly AppShellPageEntry[], copy: ShellCopy): AppShellPage[] {
  return entries.map(entry => typeof entry === 'string' ? {id:entry,title:copy[entry],icon:builtInIcons[entry]} : entry)
}

export function sidebarGroups(pages: readonly AppShellPage[]): AppSidebarGroup[] {
  const groups = new Map<string | undefined, AppSidebarGroup>()
  const bottom: AppSidebarGroup = {items:[]}
  for(const page of pages) {
    if(page.hidden) continue
    let group = page.placement === 'bottom' ? bottom : groups.get(page.group)
    if(!group) groups.set(page.group, group = page.group === undefined ? {items:[]} : {label:page.group,items:[]})
    group.items.push({key:page.id,label:page.title,icon:page.icon,badge:page.badge})
  }
  return bottom.items.length ? [...groups.values(), bottom] : [...groups.values()]
}

/**
 * The phone's bottom tabs: the sidebar's items in its order, so the same pages
 * are listed (hidden pages are not). A numeric badge stays a count; any other
 * badge text, which the tab bar cannot show, becomes a dot.
 */
export function phoneTabItems(groups: readonly AppSidebarGroup[]): AppTabBarItem[] {
  return groups.flatMap(group => group.items).map(item => ({key:item.key,label:item.label,icon:item.icon,badge:tabBadge(item.badge)}))
}

function tabBadge(badge: string | number | undefined): number | boolean | undefined {
  if(typeof badge === 'number') return badge
  if(badge === undefined || badge === '') return undefined
  return /^\d+$/.test(badge) ? Number(badge) : true
}
