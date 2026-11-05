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

/** Keeps the App name in DSM's standalone header clear of the account button
 * at the UI's desktop and tablet widths. DSM gives the name the whole header
 * row and places the account button over its end, so a long name or account
 * runs under the button. While they overlap, the window is marked
 * (portalNameAttr="cut", with the room in portalNameRoom) and the stylesheet
 * ends the name with an ellipsis 12px before the button; with less than an
 * icon's width of room left it is marked "icon" and the name draws no text.
 * The name stays the element's text (the window's accessible name) and, while
 * cut, its tooltip. A name that fits, a window without the account button and
 * the UI's mobile layout (fitPortalHeader) are left as they are. Returns the
 * undo, which also stops watching.
 */
export const portalNameAttr='data-synopkgland-portal-name'
export const portalNameRoom='--synopkgland-portal-name-room'
const portalNameTitle='data-synopkgland-portal-name-title'
const nameSelector='.sds-standalone-main-window-header-text > .x-window-header-text'
const nameGap=12, iconWidth=24
export function watchPortalName(root: HTMLElement): () => void {
 const doc=root.ownerDocument, view=doc.defaultView
 if(!view) return ()=>{}
 let main: HTMLElement|null=null, name: HTMLElement|null=null, button: HTMLElement|null=null
 const clear=()=>{
  if(main?.hasAttribute(portalNameAttr)) main.removeAttribute(portalNameAttr)
  if(main?.style.getPropertyValue(portalNameRoom)) main.style.removeProperty(portalNameRoom)
  for(const element of Array.from(doc.querySelectorAll(`[${portalNameTitle}]`))) {element.removeAttribute('title');element.removeAttribute(portalNameTitle)}
 }
 const set=(mode: 'cut'|'icon', room: number, element: HTMLElement)=>{
  if(main!.getAttribute(portalNameAttr)!==mode) main!.setAttribute(portalNameAttr,mode)
  if(main!.style.getPropertyValue(portalNameRoom)!==`${room}px`) main!.style.setProperty(portalNameRoom,`${room}px`)
  // DSM's own tooltip, if it ever sets one, is kept.
  const full=element.textContent?.trim() ?? ''
  if(!element.hasAttribute('title') || element.hasAttribute(portalNameTitle)) {
   if(element.getAttribute('title')!==full) element.setAttribute('title',full)
   if(!element.hasAttribute(portalNameTitle)) element.setAttribute(portalNameTitle,'')
  }
 }
 // Measures only what this fit does not change: where the name starts, where
 // its text would end on one line (a range ignores clipping) and where the
 // button starts. Writing the result therefore never feeds back into it.
 const place=()=>{
  try {
   const nextMain=root.isConnected ? root.closest<HTMLElement>(mainSelector) : null
   if(nextMain!==main) {clear();main=nextMain}
   name=main?.querySelector<HTMLElement>(nameSelector) ?? null
   const nextButton=main?.querySelector<HTMLElement>(userSelector) ?? null
   if(nextButton!==button) {if(button) resize?.unobserve(button);button=nextButton;if(button) resize?.observe(button)}
   if(!main || !name || !button || name.closest(mainSelector)!==main || main.classList.contains('sds-standalone-main-window--no-banner') || main.hasAttribute(portalMobileAttr)) return clear()
   const b=button.getBoundingClientRect()
   const range=doc.createRange();range.selectNodeContents(name)
   const lines=Array.from(range.getClientRects()).filter(rect=>rect.width>0)
   if(b.width<=0 || b.height<=0 || !lines.length) return clear()
   const text=range.getBoundingClientRect(), box=name.getBoundingClientRect(), style=view.getComputedStyle(name)
   // A button above or below the name's line does not cover it.
   if(b.bottom<=text.top || b.top>=text.bottom) return clear()
   const oneLine=lines.every(rect=>Math.abs(rect.top-lines[0].top)<1)
   if(oneLine && text.right+parseFloat(style.paddingRight||'0')+nameGap<=b.left) return clear()
   const room=Math.max(0,Math.floor(b.left-box.left-nameGap))
   const padding=parseFloat(style.paddingLeft||'0')+parseFloat(style.paddingRight||'0')
   set(room-padding<iconWidth ? 'icon' : 'cut', room, name)
  } catch {clear()}
 }
 // DSM fills the account button in after mounting the App and re-renders on
 // resize; batch every change into one measurement per frame.
 let frame=0, stopped=false
 const schedule=()=>{if(!frame && !stopped) frame=view.requestAnimationFrame(()=>{frame=0;place()})}
 const resize=typeof view.ResizeObserver==='function' ? new view.ResizeObserver(schedule) : undefined
 const mutation=new view.MutationObserver(records=>{
  // The App's own rendering and this fit's marks are not DSM's header.
  if(records.some(record=>!root.contains(record.target) && record.attributeName!==portalNameAttr && record.attributeName!==portalNameTitle && record.attributeName!=='title')) schedule()
 })
 const host=root.closest<HTMLElement>(mainSelector)
 if(host) {
  mutation.observe(host,{subtree:true,childList:true,characterData:true,attributes:true,attributeFilter:['class','style','hidden',portalMobileAttr]})
  resize?.observe(host)
 }
 view.addEventListener('resize',schedule)
 doc.fonts?.addEventListener?.('loadingdone',schedule)
 place()
 return ()=>{
  stopped=true
  if(frame) view.cancelAnimationFrame(frame)
  frame=0
  mutation.disconnect();resize?.disconnect()
  view.removeEventListener('resize',schedule)
  doc.fonts?.removeEventListener?.('loadingdone',schedule)
  clear()
 }
}
