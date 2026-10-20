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
