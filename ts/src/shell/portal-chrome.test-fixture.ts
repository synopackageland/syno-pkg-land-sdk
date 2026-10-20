export function portalFixture() {
 const main=document.createElement('div')
 main.className='sds-standalone-main-window'
 main.innerHTML='<div class="sds-standalone-main-window-header"><span class="sds-standalone-main-window-header__toolbar__btn">alice</span></div><div class="x-window-header"><div class="sds-standalone-main-window-header-text">Portal App</div></div>'
 document.body.append(main)
 return {main,title:main.querySelector<HTMLElement>('.sds-standalone-main-window-header-text')!,account:main.querySelector<HTMLElement>('.sds-standalone-main-window-header__toolbar__btn')!,remove(){main.remove()}}
}
