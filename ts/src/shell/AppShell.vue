<script setup lang="ts">
// DSM-style shell: a fixed left navigation and one right-hand page with a
// header. A page the default slot renders stays mounted (v-show) so provider
// leases and job polling keep running while the user moves between pages; a
// page's own component is rendered only while it is the current page.
import {computed, provide, ref, useTemplateRef, watch, watchEffect, type FunctionalComponent} from 'vue'
import {AppLayout, AppRoot, AppSidebar, AppSidebarUser, Callout, ConfigProvider, LoadingArea, PageContent, PageHeader, ensureDeviceViewport, type WideLayout} from '@synopackageland/ui'
import {isFullPage, type ShellContext} from '../context'
import {appRefusal} from '../authorization/refusal'
import {shellCopy} from './i18n'
import {readPortalChrome, type PortalChrome} from './portal-chrome'
import {uiLocale} from '../i18n'
import {hasContent, rendersContent} from './slot-content'
import type {ShellPage} from './pages'
import {resolvePages, sidebarGroups, type AppShellDenied, type AppShellPageEntry, type AppShellPageSurface} from './app-pages'
import ExecutorNotice from '../delegate/ExecutorNotice.vue'
import {PageActionsTarget} from './page-actions'
import {PageInfoTarget, ShellLocale} from './page-info'

const props = defineProps<{
  locale: string
  modelValue: string
  pages: readonly AppShellPageEntry[]
  description?: string
  // A full page (Portal/standalone) takes the web layout, a device-width
  // viewport and the account from it; a DSM window keeps its own. Both take
  // the brand: a window narrowed to the mobile layout shows it in the phone
  // bar, where AppLayout would otherwise show DSM's document.title. With
  // ctx.executor, every page shows ExecutorNotice while it is not approved.
  ctx?: ShellContext
  wideLayout?: WideLayout
  sidebarStorageKey?: string
  brandName?: string
  brandIcon?: string
  // An App's own pages start at the top; this keeps the scroll position instead.
  // A shell of only the built-in pages always keeps it, as it always did.
  keepScroll?: boolean
  pageWidth?: 'fill' | 'readable' | (string & {})
  /** Forwarded to both PageContent branches; omitted keeps the UI default. 'none' needs UI 0.12.5+. */
  pageSurface?: AppShellPageSurface
  // The account may not use the App: one page says so in place of the App's pages.
  denied?: boolean | AppShellDenied
  // Wraps the shell in an app-local AppRoot, with the UI's locale. In a DSM window its dialogs may
  // leave the window, and a press on one brings the window to the front.
  appRoot?: boolean
  // The App's first reads are still loading: the sidebar and page title stand,
  // the page body shows a loading area and the App's actions wait for them.
  loading?: boolean
}>()
const emit = defineEmits<{'update:modelValue': [value: string]}>()
const copy = computed(() => shellCopy(props.locale))
// Older supported UI declarations do not include 'none'. Forward it unchanged:
// UI 0.12.5+ supplies its rendering; leave the default to the installed UI.
const contentSurface = computed(() => props.pageSurface as InstanceType<typeof PageContent>['$props']['surface'])
const pages = computed(() => resolvePages(props.pages, copy.value))
const groups = computed(() => sidebarGroups(pages.value))
const current = computed(() => pages.value.find(page => page.id === props.modelValue))
const page = computed(() => current.value && props.loading ? {...current.value, fill: true} : current.value)
const title = computed(() => page.value?.title ?? copy.value[props.modelValue as ShellPage] ?? copy.value.status)
const scroller = useTemplateRef<{$el: HTMLElement}>('scroller')
const ownPages = computed(() => props.pages.some(page => typeof page !== 'string'))
watch(() => props.modelValue, () => {
  if(ownPages.value && !props.keepScroll && scroller.value) scroller.value.$el.scrollTop = 0
}, {flush:'post'})
const slots = defineSlots<{
  default?: (scope: {page: string}) => unknown
  actions?: (scope: {page: string}) => unknown
  // Beside the page title, such as when a page was last scanned.
  meta?: (scope: {page: string}) => unknown
  toolbar?: (scope: {page: string}) => unknown
  notices?: (scope: {page: string}) => unknown
  'sidebar-footer'?: () => unknown
  denied?: () => unknown
}>()
const denied = computed<AppShellDenied | undefined>(() => props.denied === true ? {} : props.denied || undefined)
const refusal = computed(() => appRefusal(denied.value, props.locale, props.brandName ?? props.ctx?.displayName))
const fullPage = computed(() => props.ctx !== undefined && isFullPage(props.ctx))
const pageCtx = computed(() => fullPage.value ? props.ctx : undefined)
// An account without a name (no signed-in user) shows no sidebar user.
// Without app-root the shell renders as it is, inside the App's own root.
const Bare: FunctionalComponent = (_, {slots}) => slots.default?.()
const inWindow = computed(() => props.ctx !== undefined && !fullPage.value)
function toFront() {props.ctx?.host?.activateWindow?.()}
const layout = useTemplateRef<{$el: HTMLElement}>('layout')
const portalChrome = ref<PortalChrome>({brand:false,user:false})
watch([fullPage,layout], (_, __, onCleanup) => {
  const root=layout.value?.$el
  if(!fullPage.value || !root) {portalChrome.value={brand:false,user:false};return}
  const read=()=>{
    const next=readPortalChrome(root)
    if(next.brand!==portalChrome.value.brand || next.user!==portalChrome.value.user) portalChrome.value=next
  }
  read()
  // DSM finishes its header after mounting the App. Watch host changes too;
  // ignore mutations inside the App to avoid observing our own rendering.
  const observer=new MutationObserver(records=>{
    if(records.some(record=>!root.contains(record.target))) read()
  })
  observer.observe(root.ownerDocument.body,{subtree:true,childList:true,attributes:true,attributeFilter:['class','style','hidden'],characterData:true})
  const view=root.ownerDocument.defaultView
  view?.addEventListener('resize',read)
  onCleanup(()=>{observer.disconnect();view?.removeEventListener('resize',read)})
}, {flush:'post',immediate:true})
const user = computed(() => !portalChrome.value.user && pageCtx.value?.user?.name ? pageCtx.value.user : undefined)
// A page component's PageActions render into the header, before the App's actions.
provide(PageActionsTarget, useTemplateRef<HTMLElement>('page-actions'))
// A page component's PageInfo renders on the title line, before the App's meta.
provide(PageInfoTarget, useTemplateRef<HTMLElement>('page-info'))
provide(ShellLocale, computed(() => props.locale))
// The notices block shows by what its slot renders: a notice component that
// renders nothing passes the slot a vnode all the same.
const noticesBlock = useTemplateRef<HTMLElement>('notices')
const noticesEmpty = ref(true)
watchEffect(onCleanup => {
  const block = noticesBlock.value
  if(!block) return
  const read = () => {noticesEmpty.value = !rendersContent(block)}
  read()
  const observer = new MutationObserver(read)
  observer.observe(block, {childList: true, subtree: true, characterData: true})
  onCleanup(() => observer.disconnect())
}, {flush: 'post'})
// Without one a phone lays a full page out ~980px wide and never reaches the mobile layout.
watchEffect(onCleanup => {if(fullPage.value) onCleanup(ensureDeviceViewport())})
</script>

<template>
  <component :is="appRoot ? AppRoot : Bare" v-bind="appRoot ? {modalScope: 'app', dialogBounds: inWindow ? 'viewport' : 'app', style: 'height: 100%;', onActivate: toFront} : {}">
  <component :is="appRoot ? ConfigProvider : Bare" v-bind="appRoot ? {locale: uiLocale(locale)} : {}">
  <PageContent v-if="denied" class="app-denied" :width="pageWidth" :surface="contentSurface">
    <PageHeader v-if="denied.header !== false" :title="denied.title ?? refusal?.title ?? copy.deniedTitle" />
    <slot name="denied"><Callout tone="danger"><span role="alert">{{ denied.message ?? refusal?.message ?? copy.deniedMessage }}</span></Callout></slot>
  </PageContent>
  <AppLayout v-else ref="layout" :class="['app-shell', {'app-shell-fill': page?.fill, 'app-shell-portal-brand': portalChrome.brand}]" min-height="0" :wide-layout="wideLayout ?? (fullPage ? 'web' : 'window')" :sidebar-storage-key="sidebarStorageKey"
    :brand-name="brandName ?? ctx?.displayName" :brand-icon="brandIcon ?? ctx?.appIcon">
    <template #sidebar>
      <AppSidebar :model-value="modelValue" :groups="groups" @update:model-value="emit('update:modelValue', $event)" />
    </template>
    <template v-if="slots['sidebar-footer'] || user" #sidebar-footer>
      <slot name="sidebar-footer"><AppSidebarUser v-if="user" :name="user.name" :detail="user.isAdmin ? copy.administrator : copy.dsmUser" /></slot>
    </template>
    <template v-if="slots.toolbar" #toolbar><slot name="toolbar" :page="modelValue" /></template>
    <PageContent ref="scroller" :class="['app-page', {'app-page-fill': page?.fill}]" :width="pageWidth" :surface="contentSurface">
      <PageHeader v-if="page?.header !== false" :title="title" :description="page?.description ?? description">
        <template #meta><span ref="page-info" class="app-page-info" /><slot name="meta" :page="modelValue" /></template>
        <template #actions><div ref="page-actions" class="app-page-actions" /><slot v-if="!loading" name="actions" :page="modelValue" /></template>
      </PageHeader>
      <ExecutorNotice v-if="ctx?.executor" :ctx="ctx" :locale="locale" />
      <div v-if="hasContent(slots.notices?.({page: modelValue}))" ref="notices" class="app-notices" :hidden="noticesEmpty"><slot name="notices" :page="modelValue" /></div>
      <div class="app-page-content"><LoadingArea v-if="loading" :text="copy.loading" /><template v-else><component :is="page.component" v-if="page?.component" :key="page.id" /><slot :page="modelValue" /></template></div>
    </PageContent>
  </AppLayout>
  </component>
  </component>
</template>

<style scoped>
/* The library clips AppLayout at window height; the page body scrolls while
   the sidebar keeps its own height. Modeled on syno-reclaim's AppShell. */
.app-shell { height: 100%; min-width: 0; grid-template-rows: minmax(0, 1fr); }
/* Keep the UI's navigation and collapse controls; remove the duplicate brand block. */
.app-shell-portal-brand :deep([data-ui-sidebar-brand]) { display: none; }
.app-shell :deep(> div) { min-height: 0; }
.app-shell :deep(main) { display: flex; flex-direction: column; overflow: hidden; }
.app-page { flex: 1 1 auto; min-height: 0; overflow-y: auto; }
.app-denied { height: 100%; overflow-y: auto; }
/* Lays a page's own actions and notes out as the header's own items. */
.app-page-actions, .app-page-info { display: contents; }
/* A fill page: the body takes the height under the header and notices, its
   content stretches to it and scrolls itself. 100dvh follows a phone's
   address bar, which never collapses while the page does not scroll. */
.app-shell-fill { max-height: 100dvh; }
.app-page-fill { display: flex; flex-direction: column; overflow: hidden; }
.app-page-fill > :deep([data-page-content-body]) { display: flex; flex: 1 1 auto; flex-direction: column; width: 100%; min-height: 0; }
.app-page-fill .app-notices { flex: 0 0 auto; }
.app-page-fill .app-page-content { display: grid; flex: 1 1 auto; grid-template-columns: minmax(0, 1fr); grid-template-rows: minmax(0, 1fr); min-height: 0; }
/* Notices share the header's padding and scroll with the page; PageContent
   spaces them from the header and the content. */
.app-notices { display: flex; flex-direction: column; gap: var(--ui-space-2, 8px); }
.app-notices[hidden] { display: none; }
.app-page-content { padding-bottom: var(--ui-space-6, 24px); }
</style>
