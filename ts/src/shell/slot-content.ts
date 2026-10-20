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
