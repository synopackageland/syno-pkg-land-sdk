import {pathToFileURL} from 'node:url';
import {resolve} from 'node:path';

export function gatePlan(full = false) {
  return full ? ['basic', 'integrations', 'registry'] : ['basic'];
}
if (process.argv[1] && import.meta.url === pathToFileURL(resolve(process.argv[1])).href) {
  if (!['basic', 'full'].includes(process.argv[2])) throw new Error('Expected basic or full');
  console.log(gatePlan(process.argv[2] === 'full').join('\n'));
}
