export function portalFixture() {
 const main=document.createElement('div')
 main.className='sds-standalone-main-window'
 // DSM 7 wraps the header text and adds the account toolbar beside it.
 main.innerHTML='<div class="x-window-header x-panel-icon"><div class="sds-standalone-main-window-header-text"><span class="x-window-header-text">Portal App</span></div><div class="sds-standalone-main-window-header"><span class="x-btn sds-standalone-main-window-header__toolbar__btn"><em class="x-btn-arrow"><button type="button" class="x-btn-text">alice</button></em></span></div></div>'
 document.body.append(main)
 return {main,title:main.querySelector<HTMLElement>('.sds-standalone-main-window-header-text')!,account:main.querySelector<HTMLElement>('.sds-standalone-main-window-header__toolbar__btn')!,remove(){main.remove()}}
}
