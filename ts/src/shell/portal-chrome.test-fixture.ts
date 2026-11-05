export function portalFixture() {
 const main=document.createElement('div')
 main.className='sds-standalone-main-window'
 // DSM 7.3/7.4: the account toolbar is the window's child before the header,
 // whose text DSM wraps.
 main.innerHTML='<div class="sds-standalone-main-window-header"><span class="x-btn sds-standalone-main-window-header__toolbar__btn"><em class="x-btn-arrow"><button type="button" aria-label="alice" class="x-btn-text">alice</button></em></span></div><div class="x-window-header x-panel-icon"><div class="sds-standalone-main-window-header-text"><span class="x-window-header-text">Portal App</span></div></div>'
 document.body.append(main)
 return {main,title:main.querySelector<HTMLElement>('.sds-standalone-main-window-header-text')!,account:main.querySelector<HTMLElement>('.sds-standalone-main-window-header__toolbar__btn')!,remove(){main.remove()}}
}
