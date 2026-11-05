/** DSM renders Portal chrome beside the App in its standalone main window.
 * Unknown markup stays visible in AppShell; no admin-only settings API is used.
 */
export interface PortalChrome {brand: boolean; user: boolean}
const mainSelector='.sds-standalone-main-window'
export function readPortalChrome(root: HTMLElement): PortalChrome {
 const absent={brand:false,user:false}
 try {
  const main=root.closest(mainSelector)
  if(!root.isConnected || !main || main.classList.contains('sds-standalone-main-window--no-banner')) return absent
  const visible=(selector:string)=>Array.from(main.querySelectorAll<HTMLElement>(selector)).some(element=>{
   // Never take chrome from the App's own subtree or a nested DSM window.
   if(root.contains(element) || element.closest(mainSelector)!==main || !element.textContent?.trim()) return false
   const view=element.ownerDocument.defaultView
   if(!view) return false
   for(let node:HTMLElement|null=element;node;node=node.parentElement){
    const style=view.getComputedStyle(node)
    if(node.hidden || style.display==='none' || style.visibility==='hidden' || style.visibility==='collapse' || style.opacity==='0') return false
   }
   const rect=element.getBoundingClientRect()
   return rect.width>0 && rect.height>0
  })
  return {
   brand:visible('.sds-standalone-main-window-header-text'),
   user:visible('.sds-standalone-main-window-header .sds-standalone-main-window-header__toolbar__btn'),
  }
 } catch {return absent}
}

/** Marks DSM's standalone header while the App is in the UI's mobile layout
 * (AppLayout data-ui-layout-mode="mobile", an app root 640px wide or less).
 * The SDK stylesheet then shows only the App icon and an account icon there;
 * the account name moves to the top of the account menu. Without the window,
 * its header or the account button, nothing changes. Returns the undo.
 */
export const portalMobileAttr='data-synopkgland-portal-mobile'
export const portalAccountAttr='data-synopkgland-portal-account'
const userSelector='.sds-standalone-main-window-header .sds-standalone-main-window-header__toolbar__btn'
const menuSelector='.sds-standalone-main-window-header__toolbar__menu'
export function fitPortalHeader(root: HTMLElement, mobile: boolean): () => void {
 const clear=()=>{
  for(const element of Array.from(root.ownerDocument.querySelectorAll(`[${portalMobileAttr}]`))) element.removeAttribute(portalMobileAttr)
  for(const element of Array.from(root.ownerDocument.querySelectorAll(`[${portalAccountAttr}]`))) element.removeAttribute(portalAccountAttr)
 }
 try {
  const main=mobile && root.isConnected ? root.closest<HTMLElement>(mainSelector) : null
  if(!main || main.classList.contains('sds-standalone-main-window--no-banner')) {clear();return ()=>{}}
  if(!main.hasAttribute(portalMobileAttr)) main.setAttribute(portalMobileAttr,'')
  // DSM renders the account menu into the body on its first open.
  const name=main.querySelector<HTMLElement>(userSelector)?.textContent?.trim() ?? ''
  for(const menu of Array.from(root.ownerDocument.querySelectorAll<HTMLElement>(menuSelector))) {
   if(menu.closest(mainSelector) && menu.closest(mainSelector)!==main) continue
   if(!name) menu.removeAttribute(portalAccountAttr)
   else if(menu.getAttribute(portalAccountAttr)!==name) menu.setAttribute(portalAccountAttr,name)
  }
 } catch {clear();return ()=>{}}
 return clear
}
