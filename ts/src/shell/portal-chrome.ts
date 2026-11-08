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
   // The header row collapsePortalHeader moves (hidden, or fading back in)
   // still holds DSM's chrome: what DSM shows does not follow the SDK's own
   // hiding, so a decision taken from it cannot undo the hiding.
   const own=main.hasAttribute(portalCollapsedAttr) || main.hasAttribute(portalOverlayAttr) ? main.querySelector(':scope > .x-window-tl') : null
   for(let node:HTMLElement|null=element;node;node=node.parentElement){
    if(node===own) continue
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

/** On a phone, DSM's standalone header gives way to the App's one top bar.
 * Every phone mode sizes the window body once, at the page's whole height
 * from where the header starts (the header lies over the body's top,
 * portalOverlayAttr), so nothing DSM or the App lays out changes while the
 * header comes and goes: the header moves by transform and opacity only, and
 * a momentum scroll is never interrupted.
 * 'shown': the header is shown over the body; the App keeps its room
 * (portalRoom, the header's height) above its content. 'scroll': the App's
 * bars are hidden on scroll (the UI's own decision, data-ui-bars-hidden) and
 * the header slides out with them, in the same motion (portalMotion, from the
 * UI's --ui-bars-motion); the App's content moves up into the room by
 * transform, as the UI moves it under its own top bar. 'bar': the App draws
 * its own phone bar (a sub-page with Back, a page with actions or the menu),
 * which takes the header's place: the header is hidden and the App has no
 * room. DSM's own inline size stays in place underneath; a restore corrects it
 * only when DSM resized the Portal's window while the header was moved.
 * false, a window without the banner, a missing header or a root outside the
 * window body restore DSM's header. Returns the undo.
 */
export const portalCollapsedAttr='data-synopkgland-portal-collapsed'
export const portalOverlayAttr='data-synopkgland-portal-overlay'
export const portalHeaderHeight='--synopkgland-portal-header'
export const portalBodyHeight='--synopkgland-portal-body'
export const portalRoom='--synopkgland-portal-room'
export const portalMotion='--synopkgland-portal-motion'
export type PortalHeaderMode=false|'shown'|'scroll'|'bar'
const portalProperties=[portalHeaderHeight,portalBodyHeight,portalRoom,portalMotion]
export function collapsePortalHeader(root: HTMLElement, mode: PortalHeaderMode): () => void {
 const doc=root.ownerDocument
 const clear=()=>{
  for(const element of Array.from(doc.querySelectorAll<HTMLElement>(`[${portalOverlayAttr}], [${portalCollapsedAttr}]`))) {
   element.removeAttribute(portalCollapsedAttr);element.removeAttribute(portalOverlayAttr)
   for(const name of portalProperties) element.style.removeProperty(name)
   refitPortalBody(element)
  }
 }
 try {
  const main=mode && root.isConnected ? root.closest<HTMLElement>(mainSelector) : null
  const top=main?.querySelector<HTMLElement>(':scope > .x-window-tl')
  const body=main?.querySelector<HTMLElement>(':scope > .x-window-bwrap .x-window-body')
  if(!main || !top || !body || !body.contains(root) || main.classList.contains('sds-standalone-main-window--no-banner')) {clear();return ()=>{}}
  // A transform does not change the header's layout height.
  const header=top.offsetHeight
  const view=doc.defaultView
  let height: number
  if(main.classList.contains('x-window-maximized') && view) {
   // The Portal's window fills the viewport: the body reaches its bottom from
   // where the header starts. Measured from the window and the body's place
   // in its frame, which the overlay does not change. DSM's own inline height
   // is not used: DSM resizes it (on rotation) while the header lies over the
   // body and then subtracts a different height.
   const wrap=main.querySelector<HTMLElement>(':scope > .x-window-bwrap')!
   const start=main.getBoundingClientRect().top+(body.getBoundingClientRect().top-wrap.getBoundingClientRect().top)
   height=Math.round(view.innerHeight-start)
  } else {
   // Another window: DSM's inline height (else the drawn one) and the header's.
   const inline=parseFloat(body.style.height)
   const stored=parseFloat(main.style.getPropertyValue(portalBodyHeight))
   const base=Number.isFinite(inline) ? inline : main.hasAttribute(portalOverlayAttr) && Number.isFinite(stored) ? stored-header : body.offsetHeight
   height=base+header
  }
  if(!header || !(height>0)) {clear();return ()=>{}}
  // The UI's bars move faster after a flick; the header moves with them. Read
  // before anything is written: reading a computed style after the marks
  // change would start the header's transition at the old speed.
  const style=view?.getComputedStyle(root)
  const motion=style?.getPropertyValue('--ui-bars-motion').trim() || style?.getPropertyValue('--ui-motion-bars').trim()
  const set=(name: string, value: string|undefined)=>{
   if(value===undefined) {if(main.style.getPropertyValue(name)) main.style.removeProperty(name)}
   else if(main.style.getPropertyValue(name)!==value) main.style.setProperty(name,value)
  }
  if(!main.hasAttribute(portalOverlayAttr)) main.setAttribute(portalOverlayAttr,'')
  if(mode==='shown') {if(main.hasAttribute(portalCollapsedAttr)) main.removeAttribute(portalCollapsedAttr)}
  else if(main.getAttribute(portalCollapsedAttr)!==mode) main.setAttribute(portalCollapsedAttr,mode as string)
  set(portalHeaderHeight,`${header}px`)
  set(portalBodyHeight,`${height}px`)
  set(portalRoom,mode==='bar' ? undefined : `${header}px`)
  set(portalMotion,motion || undefined)
 } catch {clear();return ()=>{}}
 return clear
}

// A rotation while the header is moved leaves DSM's inline body height short
// of the header's (DSM 7.3: 380px for 362px in a 402px landscape), and DSM
// keeps it until the next real resize. On restore, the Portal's maximized
// window gets the height DSM gives it with the header shown: the viewport
// less the header and the frame above the body. Other windows are left alone.
function refitPortalBody(main: HTMLElement) {
 try {
  const view=main.ownerDocument.defaultView
  const top=main.querySelector<HTMLElement>(':scope > .x-window-tl')
  const wrap=main.querySelector<HTMLElement>(':scope > .x-window-bwrap')
  const body=wrap?.querySelector<HTMLElement>('.x-window-body')
  if(!view || !top || !wrap || !body || !main.classList.contains('x-window-maximized')) return
  const inline=parseFloat(body.style.height)
  const height=Math.round(view.innerHeight-main.getBoundingClientRect().top-top.offsetHeight-(body.getBoundingClientRect().top-wrap.getBoundingClientRect().top))
  if(Number.isFinite(inline) && height>0 && Math.abs(inline-height)>1) body.style.height=`${height}px`
 } catch {}
}
