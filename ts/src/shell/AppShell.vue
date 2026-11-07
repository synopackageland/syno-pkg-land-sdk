<script setup lang="ts">
// DSM-style shell: navigation and one page. The sidebar names the page, so
// the page draws no title of its own unless it asks for one (header: true); a
// visually hidden h1 still names it, and its actions sit in a bar above the
// page. The navigation, that bar, a page's tabs and a settings footer stay in
// place: only the content scrolls. A page the default slot renders stays
// mounted (v-show) so provider leases and job polling keep running while the
// user moves between pages; a page's own component is rendered only while it
// is the current page.
import {computed, provide, ref, useTemplateRef, watch, watchEffect, type FunctionalComponent} from 'vue'
import {CircleUser, LogOut} from '@lucide/vue'
import {AppLayout, AppRoot, AppSidebar, AppSidebarUser, AppToolbar, Button, Callout, Dialog, IconButton, ConfigProvider, LoadingArea, PageContent, PageHeader, ensureDeviceViewport, type DropdownMenuEntry, type WideLayout} from '@synopackageland/ui'
import {isFullPage, type ShellContext} from '../context'
import {appRefusal} from '../authorization/refusal'
import {shellCopy} from './i18n'
import {collapsePortalHeader, fitPortalHeader, readPortalChrome, watchPortalName, type PortalChrome} from './portal-chrome'
import {uiLocale} from '../i18n'
import {fillState, hasContent, rendersContent, rendersOwnContent} from './slot-content'
import type {ShellPage} from './pages'
import {resolvePages, sidebarGroups, type AppShellDenied, type AppShellPageEntry, type AppShellPageSurface} from './app-pages'
import ExecutorNotice from '../delegate/ExecutorNotice.vue'
import UiUpdateNotice from './UiUpdateNotice.vue'
import ShellPhone from './ShellPhone.vue'
import {PageActionsTarget} from './page-actions'
import {PageInfoTarget, ShellLocale} from './page-info'

const props = withDefaults(defineProps<{
  locale: string
  modelValue: string
  pages: readonly AppShellPageEntry[]
  // A full page (Portal/standalone) takes the web layout, a device-width
  // viewport and the account from it; a DSM window keeps its own. Both take
  // the brand: a window narrowed to the mobile layout shows it in the phone
  // drawer, where AppLayout would otherwise show DSM's document.title. With
  // ctx.executor, every page shows ExecutorNotice while it is not approved;
  // with ctx.uiUpdate, UiUpdateNotice once the installed UI replaced this page's.
  ctx?: ShellContext
  wideLayout?: WideLayout
  /** Forwarded to AppLayout; omitted keeps its sidebar default. */
  navigation?: 'sidebar' | 'top'
  /** Forwarded to AppLayout; false keeps the phone bars visible throughout the App. */
  hideOnScroll?: boolean
  /** Override the phone bar title; omitted keeps the current page title. */
  title?: string
  /** Phone bar actions, forwarded to AppLayout; emits headerAction on selection. */
  headerActions?: DropdownMenuEntry[]
  sidebarStorageKey?: string
  brandName?: string
  brandIcon?: string
  // An App's own pages start at the top; this keeps the scroll position instead.
  // A shell of only the built-in pages always keeps it, as it always did.
  keepScroll?: boolean
  pageWidth?: 'fill' | 'readable' | (string & {})
  /** Forwarded to both PageContent branches; omitted keeps the UI default. */
  pageSurface?: AppShellPageSurface
  // The account may not use the App: one page says so in place of the App's pages.
  denied?: boolean | AppShellDenied
  // Wraps the shell in an app-local AppRoot, with the UI's locale. In a DSM window its dialogs may
  // leave the window, and a press on one brings the window to the front.
  appRoot?: boolean
  // The App's first reads are still loading: the sidebar and page title stand,
  // the page body shows a loading area and the App's actions wait for them.
  loading?: boolean
  /** Where #accessory goes off a phone: 'bottom' (default), the layout's bottom row; 'notices', after the page's notices at its top. A phone keeps it above the tab bar. */
  accessoryDesktop?: 'bottom' | 'notices'
}>(), {hideOnScroll: undefined})
const emit = defineEmits<{'update:modelValue': [value: string]; brand: [event: MouseEvent]; headerAction: [key: string]}>()
// Apps can use @brand.prevent to replace the default home selection.
function onBrand(event: MouseEvent) {
  emit('brand', event)
  const first = pages.value[0]
  if(!event.defaultPrevented && first) emit('update:modelValue', first.id)
}
const copy = computed(() => shellCopy(props.locale))
const pages = computed(() => resolvePages(props.pages, copy.value))
const groups = computed(() => sidebarGroups(pages.value))
const current = computed(() => pages.value.find(page => page.id === props.modelValue))
const page = current
// 'shown': the shell's PageHeader (header: true; hidden on a phone's tab page). 'hidden', the default: a
// visually hidden title. 'own': the page draws its own header (header: false).
const headerMode = computed(() => page.value?.header === true ? (phoneTab.value ? 'hidden' : 'shown') : page.value?.header === false ? 'own' : 'hidden')
// A page fills the height when it says so, while it loads, or when it shows a
// SettingsPage or a SectionTabs fill: their tabs and footer stay and only their
// body scrolls. fill: false keeps the page scrolling as a whole.
const content = useTemplateRef<HTMLElement>('content')
const detected = ref({fill: false, footer: false})
watchEffect(onCleanup => {
  const element = content.value
  if(!element) return
  const read = () => {
    const next = fillState(element)
    if(next.fill !== detected.value.fill || next.footer !== detected.value.footer) detected.value = next
  }
  read()
  const observer = new MutationObserver(read)
  observer.observe(element, {childList: true, subtree: true, attributes: true, attributeFilter: ['style', 'hidden', 'class']})
  onCleanup(() => observer.disconnect())
}, {flush: 'post'})
const fill = computed(() => !!props.loading || page.value?.fill === true || (page.value?.fill !== false && detected.value.fill))
const title = computed(() => page.value?.title ?? copy.value[props.modelValue as ShellPage] ?? copy.value.status)
// The phone bar names the current page. A page that draws its own header names
// itself: AppLayout reads that PageHeader's title.
const barTitle = computed(() => headerMode.value === 'own' ? undefined : title.value)
const scroller = useTemplateRef<{$el: HTMLElement}>('scroller')
const ownPages = computed(() => props.pages.some(page => typeof page !== 'string'))
watch(() => props.modelValue, () => {
  if(ownPages.value && !props.keepScroll && scroller.value) scroller.value.$el.scrollTop = 0
}, {flush:'post'})
const slots = defineSlots<{
  default?: (scope: {page: string; phone: boolean}) => unknown
  actions?: (scope: {page: string; phone: boolean}) => unknown
  // Beside the page title, such as when a page was last scanned.
  meta?: (scope: {page: string; phone: boolean}) => unknown
  toolbar?: (scope: {page: string; phone: boolean}) => unknown
  notices?: (scope: {page: string; phone: boolean}) => unknown
  /** Forwarded only when supplied, so AppLayout retains its menu fallback. */
  tabbar?: (scope: {page: string; phone: boolean}) => unknown
  /** Bottom bar forwarded to AppLayout; requires a UI that supports the accessory slot. */
  accessory?: (scope: {page: string; phone: boolean}) => unknown
  'sidebar-footer'?: (scope: {phone: boolean}) => unknown
  denied?: (scope: {phone: boolean}) => unknown
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
// What AppLayout shows on a phone, read from its markup: the phone signal, the
// key of the tab bar's current tab, its own top bar (with Back on a sub-page)
// and bars hidden on scroll. A UI without the phone shell reads none.
interface PhoneShell {phone: boolean; tab: string | undefined; bar: boolean; back: boolean; barsHidden: boolean}
const noPhoneShell: PhoneShell = {phone:false,tab:undefined,bar:false,back:false,barsHidden:false}
const phoneShell = ref<PhoneShell>(noPhoneShell)
const currentTab = '[data-ui-tab-bar-item][aria-current="page"]'
function readPhoneShell(root: HTMLElement): PhoneShell {
  const column = Array.from(root.children).find(child => child.hasAttribute('data-ui-layout-main'))
  const chrome = column ? Array.from(column.children).filter(child => child.tagName !== 'MAIN') : []
  const bar = chrome.find(child => child.hasAttribute('data-ui-mobile-navigation'))
  return {
    phone: root.hasAttribute('data-ui-phone'),
    // Only a tab drawn in the bar counts: More (a page under it) is not a tab item.
    tab: chrome.map(child => child.matches(currentTab) ? child : child.querySelector(currentTab)).find(item => item)?.getAttribute('data-ui-tab-bar-item') ?? undefined,
    bar: !!bar,
    back: !!bar?.querySelector('[data-ui-mobile-back]'),
    barsHidden: root.hasAttribute('data-ui-bars-hidden'),
  }
}
watch(layout, (component, _, onCleanup) => {
  const root = component?.$el
  if(!(root instanceof HTMLElement)) {phoneShell.value = noPhoneShell;return}
  const read = () => {
    const next = readPhoneShell(root)
    const now = phoneShell.value
    if((Object.keys(next) as (keyof PhoneShell)[]).some(key => next[key] !== now[key])) phoneShell.value = next
  }
  read()
  const observer = new MutationObserver(read)
  // One observe call: observing the same node again replaces its options.
  observer.observe(root, {attributes: true, attributeFilter: ['data-ui-phone', 'data-ui-bars-hidden', 'aria-current'], childList: true, subtree: true})
  onCleanup(() => observer.disconnect())
}, {flush: 'post', immediate: true})
// A phone outside a DSM window (a full page or no ctx) draws no title row on
// one of the tab bar's pages: the tab bar already says where the user is. Its
// one top bar holds only the account and the App's header actions, and is not
// drawn without them. A sub-page keeps its Back, title and actions. A tab page
// is the page whose own tab is current: a page outside the tab bar (a detail
// or live page) keeps its title bar even while the App marks a tab current.
const phoneTab = computed(() => phoneShell.value.phone && !inWindow.value && phoneShell.value.tab !== undefined && phoneShell.value.tab === props.modelValue)
const portalChrome = ref<PortalChrome>({brand:false,user:false})
// Where DSM draws no account (ui.banner: hidden), the shell's account also signs out.
const canSignOut = computed(() => {
  const host = pageCtx.value?.host
  return typeof host?.signOut === 'function' && host.platform().host === 'dsm'
})
function signOut() {accountOpen.value = false; pageCtx.value?.host?.signOut?.()}
const appActions = computed(() => props.headerActions ?? [])
// Whether AppLayout draws its top bar, leaving the account out of it.
const barWithoutAccount = computed(() => !phoneTab.value || appActions.value.length > 0 || phoneShell.value.back)
// On a phone the account joins the one top bar: always where DSM shows no
// account, and in DSM's portal while the App's bar takes the place of DSM's
// header (which then shows no account). Never a second account control.
const accountMergeable = computed(() => phoneShell.value.phone && !!pageCtx.value?.user?.name && canSignOut.value)
const accountInBar = computed(() => accountMergeable.value && (!portalChrome.value.user || barWithoutAccount.value))
const accountKey = 'synopkgland-shell-account'
const accountOpen = ref(false)
// With the App's actions the account follows them in "…"; alone, it is the bar's account icon.
const barActions = computed<DropdownMenuEntry[]>(() => {
  if(!accountInBar.value) return appActions.value
  const account: DropdownMenuEntry = {key: accountKey, label: pageCtx.value?.user?.name ?? copy.value.account, icon: CircleUser}
  return appActions.value.length ? [...appActions.value, {type: 'separator', key: `${accountKey}-separator`}, account] : [account]
})
function onHeaderAction(key: string) {
  if(key === accountKey && accountInBar.value) accountOpen.value = true
  else emit('headerAction', key)
}
watch(accountInBar, shown => {if(!shown) accountOpen.value = false})
// AppLayout has no tab-page mode: it draws its phone bar for a title, a brand,
// Back or header actions, and next to a title it shows the brand's icon. So a
// tab page passes no brand, and an empty title only while actions need the bar
// (without one, AppLayout would title it "Application navigation"). Without
// actions there is no bar; Back brings its own title.
const layoutTitle = computed(() => phoneTab.value ? (barActions.value.length ? '' : undefined) : props.title ?? barTitle.value)
const layoutBrandName = computed(() => phoneTab.value ? '' : props.brandName ?? props.ctx?.displayName)
const layoutBrandIcon = computed(() => phoneTab.value ? '' : props.brandIcon ?? props.ctx?.appIcon)
watch([fullPage,layout,accountMergeable], (_, __, onCleanup) => {
  const root=layout.value?.$el
  if(!fullPage.value || !root) {portalChrome.value={brand:false,user:false};return}
  let undoFit=()=>{}, undoCollapse=()=>{}
  const read=()=>{
    const next=readPortalChrome(root)
    if(next.brand!==portalChrome.value.brand || next.user!==portalChrome.value.user) portalChrome.value=next
    // In the UI's mobile layout DSM's header keeps only the App icon and an account icon.
    undoFit=fitPortalHeader(root, root.getAttribute('data-ui-layout-mode')==='mobile')
    // On a phone the App's own top bar takes the header's place, and the
    // header hides with the App's bars on scroll: one top bar at a time. The
    // bar takes it only when it can hold the account DSM's header shows.
    const shell=readPhoneShell(root)
    const bar=shell.bar && (accountMergeable.value || !next.user)
    undoCollapse=collapsePortalHeader(root, !shell.phone ? false : bar ? 'bar' : shell.barsHidden ? 'scroll' : false)
  }
  read()
  // At wider layouts a long App name ends before DSM's account button.
  const stopName=watchPortalName(root)
  const mode=new MutationObserver(read)
  mode.observe(root,{attributes:true,attributeFilter:['data-ui-layout-mode','data-ui-phone','data-ui-bars-hidden']})
  // The App's own top bar comes and goes as the page changes.
  const column=Array.from(root.children).find(child=>child.hasAttribute('data-ui-layout-main'))
  if(column) mode.observe(column,{childList:true})
  // DSM finishes its header after mounting the App. Watch host changes too;
  // ignore mutations inside the App to avoid observing our own rendering.
  const observer=new MutationObserver(records=>{
    if(records.some(record=>!root.contains(record.target))) read()
  })
  observer.observe(root.ownerDocument.body,{subtree:true,childList:true,attributes:true,attributeFilter:['class','style','hidden'],characterData:true})
  const view=root.ownerDocument.defaultView
  view?.addEventListener('resize',read)
  onCleanup(()=>{observer.disconnect();mode.disconnect();view?.removeEventListener('resize',read);undoFit();undoCollapse();stopName()})
}, {flush:'post',immediate:true})
const user = computed(() => !portalChrome.value.user && !accountInBar.value && pageCtx.value?.user?.name ? pageCtx.value.user : undefined)
// A page component's PageActions render into the header (or the page bar), before the App's actions.
provide(PageActionsTarget, useTemplateRef<HTMLElement>('page-actions'))
// A page component's PageInfo renders on the title line (or the page bar), before the App's meta.
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
// The page bar holds the actions and notes of a page without the shell's
// header; it shows only while one of them renders something.
const pageBar = useTemplateRef<HTMLElement>('page-bar')
const pageBarEmpty = ref(true)
watchEffect(onCleanup => {
  const bar = pageBar.value
  if(!bar) {pageBarEmpty.value = true; return}
  const read = () => {pageBarEmpty.value = !Array.from(bar.querySelectorAll('.app-page-meta, .app-page-bar-actions')).some(slot => rendersOwnContent(slot, '.app-page-slot'))}
  read()
  const observer = new MutationObserver(read)
  observer.observe(bar, {childList: true, subtree: true, characterData: true})
  onCleanup(() => observer.disconnect())
}, {flush: 'post'})
// Without one a phone lays a full page out ~980px wide and never reaches the mobile layout.
watchEffect(onCleanup => {if(fullPage.value) onCleanup(ensureDeviceViewport())})
</script>

<template>
  <component :is="appRoot ? AppRoot : Bare" v-bind="appRoot ? {modalScope: 'app', dialogBounds: inWindow ? 'viewport' : 'app', style: 'height: 100%;', onActivate: toFront} : {}">
  <component :is="appRoot ? ConfigProvider : Bare" v-bind="appRoot ? {locale: uiLocale(locale)} : {}">
  <ShellPhone v-slot="{phone}">
  <PageContent v-if="denied" class="app-denied" :width="pageWidth" :surface="pageSurface">
    <PageHeader v-if="denied.header !== false" :title="denied.title ?? refusal?.title ?? copy.deniedTitle" />
    <slot name="denied" :phone="phone"><Callout tone="danger"><span role="alert">{{ denied.message ?? refusal?.message ?? copy.deniedMessage }}</span></Callout></slot>
  </PageContent>
  <AppLayout v-else ref="layout" :class="['app-shell', {'app-shell-fill': fill, 'app-shell-portal-brand': portalChrome.brand}]" min-height="0" :wide-layout="wideLayout ?? (fullPage ? 'web' : 'window')" :sidebar-storage-key="sidebarStorageKey"
    :navigation="navigation" :hide-on-scroll="hideOnScroll" :title="layoutTitle" :header-actions="barActions" @header-action="onHeaderAction" :brand-name="layoutBrandName" :brand-icon="layoutBrandIcon" @brand="onBrand">
    <template #sidebar>
      <AppSidebar :model-value="modelValue" :groups="groups" @update:model-value="emit('update:modelValue', $event)" />
    </template>
    <template v-if="slots['sidebar-footer'] || user" #sidebar-footer>
      <slot name="sidebar-footer" :phone="phone"><div v-if="user" class="app-shell-account"><AppSidebarUser :name="user.name" :detail="user.isAdmin ? copy.administrator : copy.dsmUser" /><IconButton v-if="canSignOut" class="app-shell-sign-out" variant="ghost" :label="copy.signOut" @click="signOut"><LogOut :size="16" aria-hidden="true" /></IconButton></div></slot>
    </template>
    <template v-if="slots.accessory && (phone || accessoryDesktop !== 'notices')" #accessory><slot name="accessory" :page="modelValue" :phone="phone" /></template>
    <template v-if="slots.tabbar" #tabbar><slot name="tabbar" :page="modelValue" :phone="phone" /></template>
    <template v-if="slots.toolbar || headerMode !== 'shown'" #toolbar>
      <slot name="toolbar" :page="modelValue" :phone="phone" />
      <div v-if="headerMode !== 'shown'" ref="page-bar" class="app-page-bar" :hidden="pageBarEmpty" data-app-page-bar>
        <AppToolbar>
          <div class="app-page-slot app-page-meta"><span ref="page-info" class="app-page-slot app-page-info" /><slot name="meta" :page="modelValue" :phone="phone" /></div>
          <template #actions><div class="app-page-slot app-page-bar-actions"><div ref="page-actions" class="app-page-slot app-page-actions" /><slot v-if="!loading" name="actions" :page="modelValue" :phone="phone" /></div></template>
        </AppToolbar>
      </div>
    </template>
    <PageContent ref="scroller" :class="['app-page', {'app-page-fill': fill, 'app-page-untitled': headerMode === 'hidden', 'app-page-flush': detected.footer}]" :width="pageWidth" :surface="pageSurface" :hide-on-scroll="page?.hideOnScroll">
      <PageHeader v-if="headerMode === 'shown'" :title="title">
        <template #meta><span ref="page-info" class="app-page-info" /><slot name="meta" :page="modelValue" :phone="phone" /></template>
        <template #actions><div ref="page-actions" class="app-page-actions" /><slot v-if="!loading" name="actions" :page="modelValue" :phone="phone" /></template>
      </PageHeader>
      <h1 v-else-if="headerMode === 'hidden'" class="app-page-title">{{ title }}</h1>
      <UiUpdateNotice v-if="ctx?.uiUpdate" :ctx="ctx" :locale="locale" />
      <ExecutorNotice v-if="ctx?.executor" :ctx="ctx" :locale="locale" />
      <div v-if="hasContent(slots.notices?.({page: modelValue, phone})) || (!phone && accessoryDesktop === 'notices' && hasContent(slots.accessory?.({page: modelValue, phone})))" ref="notices" class="app-notices" :hidden="noticesEmpty"><slot name="notices" :page="modelValue" :phone="phone" /><slot v-if="!phone && accessoryDesktop === 'notices'" name="accessory" :page="modelValue" :phone="phone" /></div>
      <div ref="content" class="app-page-content"><LoadingArea v-if="loading" :text="copy.loading" /><template v-else><component :is="page.component" v-if="page?.component" :key="page.id" /><slot :page="modelValue" :phone="phone" /></template></div>
    </PageContent>
  </AppLayout>
  <Dialog v-if="accountInBar && pageCtx?.user" v-model:open="accountOpen" :title="copy.account">
    <AppSidebarUser :name="pageCtx.user.name" :detail="pageCtx.user.isAdmin ? copy.administrator : copy.dsmUser" />
    <template #footer><Button class="app-shell-account-sign-out" @click="signOut"><LogOut :size="16" aria-hidden="true" />{{ copy.signOut }}</Button></template>
  </Dialog>
  </ShellPhone>
  </component>
  </component>
</template>

<style scoped>
/* The library clips AppLayout at window height; the page body scrolls while
   the sidebar keeps its own height. Modeled on syno-reclaim's AppShell. */
.app-shell { height: 100%; min-width: 0; }
/* Top navigation owns two rows in the UI; keep its bar above the page. */
.app-shell:not([data-ui-navigation='top']) { grid-template-rows: minmax(0, 1fr); }
/* Keep the UI's navigation and collapse controls; remove the duplicate brand block. */
.app-shell-portal-brand :deep([data-ui-sidebar-brand]) { display: none; }
.app-shell :deep(> div) { min-height: 0; }
/* Only the layout's own main: a <main> inside an App's page keeps its scroll. */
.app-shell :deep(> div > main) { display: flex; flex-direction: column; overflow: hidden; }
.app-page { flex: 1 1 auto; min-height: 0; overflow-y: auto; }
.app-denied { height: 100%; overflow-y: auto; }
/* The account is one row beside the collapse button, as wide as the footer
   leaves: the user (its name truncates), then sign out. A collapsed sidebar
   stacks them, as the footer stacks the collapse button. */
.app-shell-account { display: flex; flex: 1 1 auto; align-items: center; gap: var(--ui-space-1, 4px); min-width: 0; }
.app-shell[data-ui-sidebar-collapsed] .app-shell-account { flex: 0 0 auto; flex-direction: column; }
/* Lays a page's own actions and notes out as the header's own items. */
.app-page-actions, .app-page-info { display: contents; }
/* A page without the shell's header: its notes sit at the start of a bar above
   the page and its actions at the end, as the header placed them. The bar
   stays while the page scrolls and shows only while it holds something. */
.app-page-bar[hidden] { display: none; }
.app-page-meta, .app-page-bar-actions { display: flex; align-items: center; gap: var(--ui-space-2, 8px); min-width: 0; }
.app-page-bar-actions { flex-wrap: wrap; justify-content: flex-end; }
/* The page's name for assistive technology, where the sidebar shows it. Out of
   the flow, so it takes no gap in the page. */
.app-page-title { position: absolute; width: 1px; height: 1px; margin: -1px; padding: 0; overflow: hidden; clip: rect(0 0 0 0); clip-path: inset(50%); white-space: nowrap; border: 0; }
.app-page-untitled { padding-top: var(--ui-page-padding-y, 16px); }
/* A fill page: the body takes the height under the header and notices, its
   content stretches to it and scrolls itself. 100dvh follows a phone's
   address bar, which never collapses while the page does not scroll. */
.app-shell-fill { max-height: 100dvh; }
.app-page-fill { display: flex; flex-direction: column; overflow: hidden; }
.app-page-fill > :deep([data-page-content-body]) { display: flex; flex: 1 1 auto; flex-direction: column; width: 100%; min-height: 0; }
.app-page-fill .app-notices { flex: 0 0 auto; }
.app-page-fill .app-page-content { display: flex; flex: 1 1 auto; flex-direction: column; min-width: 0; min-height: 0; }
/* The first child that draws something takes the height; other nodes in the
   slot (an empty or hidden anchor, a toast host) keep their own size. */
.app-page-fill .app-page-content > :deep(*) { flex: none; min-width: 0; }
.app-page-fill .app-page-content > :deep(:not(:empty, [hidden])) { flex: 1 1 0%; min-height: 0; }
.app-page-fill .app-page-content > :deep(:not(:empty, [hidden]) ~ *) { flex: none; }
/* Notices share the header's padding and scroll with the page; PageContent
   spaces them from the header and the content. */
.app-notices { display: flex; flex-direction: column; gap: var(--ui-space-2, 8px); }
.app-notices[hidden] { display: none; }
/* The UI's room after a page's last row: only the safe area while the bars
   are hidden on a fill page. */
.app-page-content { padding-bottom: var(--ui-page-end-space, var(--ui-space-6, 24px)); }
/* A SettingsPage's footer is the bottom of the window, as in DSM: a page that
   shows one drops the space under it, however deeply the App wraps it. */
.app-page-flush .app-page-content { padding-bottom: 0; }
/* A fill page gives its height to a SettingsPage or a SectionTabs fill through
   the App's own wrappers: each element between the page and that component is
   a flex column taking the room its siblings leave, so the App needs no CSS to
   pass the height down. A hidden wrapper (v-show, hidden) stays hidden. */
.app-page-fill .app-page-content :deep(:not([hidden], [data-ui-section-tabs-fill], [data-ui-settings-page]):has([data-ui-section-tabs-fill], [data-ui-settings-page])) { display: flex; flex: 1 1 0%; flex-direction: column; min-width: 0; min-height: 0; }
/* Its other top-level nodes keep their own size instead of sharing the height. */
.app-page-fill .app-page-content:has([data-ui-section-tabs-fill], [data-ui-settings-page]) > :deep(:not([data-ui-section-tabs-fill], [data-ui-settings-page]):not(:has([data-ui-section-tabs-fill], [data-ui-settings-page]))) { flex: none; }
/* A tab that is not a SettingsPage scrolls under the strip. */
.app-page-fill .app-page-content :deep(.ui-section-tabs-fill > .ui-tab-panel:not(:has(> [data-ui-settings-page]))) { overflow-y: auto; overscroll-behavior: contain; }
</style>

<style>
/* DSM's standalone header sits outside the App root, so these rules are not
   scoped. fitPortalHeader marks it in the UI's mobile layout only: the App
   name gives way to its icon and the account button to an account icon; the
   account name heads the account menu. Desktop widths are unchanged. */
.sds-standalone-main-window[data-synopkgland-portal-mobile] .sds-standalone-main-window-header-text > .x-window-header-text {
  display: inline-block; box-sizing: content-box; inline-size: 0; overflow: hidden; white-space: nowrap; vertical-align: top;
}
.sds-standalone-main-window[data-synopkgland-portal-mobile] .sds-standalone-main-window-header__toolbar__btn .x-btn-arrow { padding-right: 0 !important; }
.sds-standalone-main-window[data-synopkgland-portal-mobile] .sds-standalone-main-window-header__toolbar__btn .x-btn-arrow::after { display: none; }
.sds-standalone-main-window[data-synopkgland-portal-mobile] .sds-standalone-main-window-header__toolbar__btn .x-btn-text {
  box-sizing: border-box; inline-size: 32px; block-size: 32px; margin: -6px; padding: 0; overflow: hidden; white-space: nowrap; text-indent: 100%; cursor: pointer;
  /* DSM's text colour through the icon's shape: the header is not themed by the UI. */
  background: currentColor; -webkit-mask: center / 20px 20px no-repeat url("data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 24 24' fill='none' stroke='black' stroke-width='2' stroke-linecap='round' stroke-linejoin='round'%3E%3Ccircle cx='12' cy='12' r='10'/%3E%3Ccircle cx='12' cy='10' r='3'/%3E%3Cpath d='M7 20.662V19a2 2 0 0 1 2-2h6a2 2 0 0 1 2 2v1.662'/%3E%3C/svg%3E"); mask: center / 20px 20px no-repeat url("data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 24 24' fill='none' stroke='black' stroke-width='2' stroke-linecap='round' stroke-linejoin='round'%3E%3Ccircle cx='12' cy='12' r='10'/%3E%3Ccircle cx='12' cy='10' r='3'/%3E%3Cpath d='M7 20.662V19a2 2 0 0 1 2-2h6a2 2 0 0 1 2 2v1.662'/%3E%3C/svg%3E");
}
/* Wider layouts: watchPortalName marks the window while the App name would
   run under the account button. The name ends with an ellipsis 12px before
   the button, or, with less than an icon's width left, draws no text; it
   stays the element's text and its tooltip. A name that fits is untouched. */
.sds-standalone-main-window[data-synopkgland-portal-name]:not([data-synopkgland-portal-mobile]) .sds-standalone-main-window-header-text > .x-window-header-text {
  display: inline-block; box-sizing: border-box; max-inline-size: var(--synopkgland-portal-name-room, none); overflow: hidden; white-space: nowrap; text-overflow: ellipsis; vertical-align: top;
}
.sds-standalone-main-window[data-synopkgland-portal-name="icon"]:not([data-synopkgland-portal-mobile]) .sds-standalone-main-window-header-text > .x-window-header-text {
  box-sizing: content-box; inline-size: 0; max-inline-size: none; text-overflow: clip;
}
/* On a phone the App's one top bar takes the place of DSM's header
   (collapsePortalHeader): the header row moves up out of the window by its
   own height and the window body, which DSM sizes inline, takes that height,
   so the App fills the page. With the App's bars it hides on scroll and
   comes back in the same motion. */
.sds-standalone-main-window[data-synopkgland-portal-mobile] > .x-window-tl { transition: margin-top 200ms cubic-bezier(0.32, 0.72, 0, 1); }
.sds-standalone-main-window[data-synopkgland-portal-collapsed] > .x-window-tl { margin-top: calc(-1 * var(--synopkgland-portal-header, 0px)); }
.sds-standalone-main-window[data-synopkgland-portal-collapsed] > .x-window-bwrap .x-window-body { height: var(--synopkgland-portal-body) !important; }
.sds-standalone-main-window[data-synopkgland-portal-collapsed="bar"] > .x-window-tl { transition: none; }
@media (prefers-reduced-motion: reduce) { .sds-standalone-main-window[data-synopkgland-portal-mobile] > .x-window-tl { transition: none; } }
/* A non-interactive heading above DSM's items (Help, Sign out). DSM measures
   and places the menu before the SDK names it: the heading takes the menu's
   width (inline-size 0, min 100%) instead of widening it past the button, and
   a long name wraps so it shows in full. */
.sds-standalone-main-window-header__toolbar__menu[data-synopkgland-portal-account]::before {
  content: attr(data-synopkgland-portal-account); display: block; box-sizing: border-box; inline-size: 0; min-inline-size: 100%; padding: 8px 16px 6px;
  white-space: normal; overflow-wrap: anywhere; font-size: var(--ui-font-size-sm, 13px); font-weight: 700; line-height: 20px;
  cursor: default; pointer-events: none; box-shadow: 0 1px 0 color-mix(in srgb, currentColor 12%, transparent);
}
</style>
