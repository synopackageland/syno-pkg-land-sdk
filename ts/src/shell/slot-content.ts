import {Comment, Fragment, Text, type VNode} from 'vue'

/** Whether a slot's vnodes show anything: not only v-if comments and whitespace, also inside fragments. */
export function hasContent(nodes: unknown): boolean {
 return Array.isArray(nodes) && nodes.some(node => {
  const vnode = node as VNode
  if(vnode.type === Comment) return false
  if(vnode.type === Text) return String(vnode.children).trim() !== ''
  if(vnode.type === Fragment) return hasContent(vnode.children)
  return true
 })
}

/** Whether an element shows anything: an element, or text that is not blank, among its children. */
export function rendersContent(element: Node): boolean {
 return Array.from(element.childNodes).some(node => node.nodeType === Node.ELEMENT_NODE || node.nodeType === Node.TEXT_NODE && node.textContent!.trim() !== '')
}

/**
 * Whether an element shows anything besides the shell's own empty containers
 * (matching `own`): those count only through what they hold.
 */
export function rendersOwnContent(element: Element, own: string): boolean {
 return Array.from(element.childNodes).some(node => {
  if(node.nodeType === Node.TEXT_NODE) return node.textContent!.trim() !== ''
  if(node.nodeType !== Node.ELEMENT_NODE) return false
  return (node as Element).matches(own) ? rendersOwnContent(node as Element, own) : true
 })
}

/** A SettingsPage or a SectionTabs fill: a page that shows one fills the height. */
const FILLS = '[data-ui-settings-page], [data-ui-section-tabs-fill]'

function shown(element: Element, root: Element): boolean {
 for(let node: Element | null = element; node && node !== root; node = node.parentElement) {
  if((node as HTMLElement).hidden || getComputedStyle(node).display === 'none') return false
 }
 return true
}

/**
 * A bar docked at the bottom of the page: a UI footer bar (an AppActionBar) or
 * an App's own bar marked `data-ui-page-bar`.
 */
export const DOCKED_BAR = '[data-ui-footer-bar], [data-ui-page-bar]'

function drawn(element: Element, root: Element): boolean {
 return shown(element, root) && rendersContent(element)
}

/**
 * Whether the content ends with a docked bar: its last element that draws
 * something is one, or ends with one, however the App wraps it.
 */
function endsWithBar(root: Element): boolean {
 let parent: Element = root
 for(;;) {
  const last = Array.from(parent.children).reverse().find(element => drawn(element, root))
  if(!last) return false
  if(last.matches(DOCKED_BAR)) return true
  parent = last
 }
}

/**
 * What the page's content shows: a SettingsPage or a SectionTabs fill (the
 * page fills the height), a settings footer (the page drops the space under
 * it, so the footer is the window's bottom edge) and a docked bar as the
 * content's last element (on a fill page the bar is the bottom edge too).
 * Hidden pages, by v-show or `hidden`, do not count.
 */
export function fillState(root: Element): {fill: boolean; footer: boolean; bar: boolean} {
 const any = (selector: string) => Array.from(root.querySelectorAll(selector)).some(element => shown(element, root))
 return {fill: any(FILLS), footer: any('[data-ui-settings-page-footer]'), bar: endsWithBar(root)}
}
