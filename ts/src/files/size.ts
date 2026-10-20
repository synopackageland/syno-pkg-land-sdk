// Only Vue components use it: a module the headless entry also bundles would split vue.js into a shared chunk.
const sizeUnits=['B','KB','MB','GB','TB','PB']
/** A byte count in File Station's 1024-based units, with the locale's digits. */
export function formatSize(bytes:number,locale:string):string{
 let unit=0,value=bytes
 while(value>=1024&&unit<sizeUnits.length-1){value/=1024;unit++}
 return `${new Intl.NumberFormat(locale,{maximumFractionDigits:unit?1:0}).format(value)} ${sizeUnits[unit]}`
}
