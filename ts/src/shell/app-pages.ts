// The pages an AppShell lays out. An App lists its own pages; the built-in
// 'status' and 'background' ids stay available as ready-made entries.
import type {Component} from 'vue'
import type {AppSidebarGroup} from '@synopackageland/ui'
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
  /** Replaces the shell's description for this page. */
  description?: string
  /** false: the page draws its own header. */
  header?: boolean
  /**
   * The body fills the height under the header and the page does not scroll:
   * the content scrolls itself (a treemap, a table paged by height, a log).
   */
  fill?: boolean
}
export type AppShellPageEntry = ShellPage | AppShellPage

/** PageContent surface for both the current page and the denied page. 'none' needs UI 0.12.5+. */
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
