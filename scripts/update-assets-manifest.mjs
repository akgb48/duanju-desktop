import fs from 'node:fs';
import path from 'node:path';
import { createHash } from 'node:crypto';
import { fileURLToPath } from 'node:url';

const assets = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../frontend/assets');
function list(directory) {
  return fs.readdirSync(directory, {withFileTypes:true}).flatMap(entry => {
    const target=path.join(directory,entry.name);
    return entry.isDirectory()?list(target):[target];
  });
}
const files=list(assets).filter(file=>path.relative(assets,file)!=='manifest.json').sort().map(file=>{
  const data=fs.readFileSync(file);
  return {name:path.relative(assets,file).split(path.sep).join('/'),size:data.length,sha256:createHash('sha256').update(data).digest('hex')};
});
fs.writeFileSync(path.join(assets,'manifest.json'),JSON.stringify({format:1,files},null,2)+'\n');
console.log(`已更新 ${files.length} 个界面资源摘要`);
