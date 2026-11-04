import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { createHash } from 'node:crypto';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const source = path.join(root, 'frontend', 'assets');
const out = path.join(root, 'frontend', 'dist');
const config = JSON.parse(fs.readFileSync(path.join(root, 'wails.json'), 'utf8'));
const frontend = JSON.parse(fs.readFileSync(path.join(root, 'frontend', 'package.json'), 'utf8'));
if (frontend.version !== config.info.productVersion) throw new Error('前端版本与 wails.json 不一致');
if (!fs.existsSync(path.join(source, 'index.html'))) throw new Error('缺少界面资源 frontend/assets/index.html');
const manifest = JSON.parse(fs.readFileSync(path.join(source, 'manifest.json'), 'utf8'));
for (const entry of manifest.files) {
  const data = fs.readFileSync(path.join(source, entry.name));
  const hash = createHash('sha256').update(data).digest('hex');
  if (data.length !== entry.size || hash !== entry.sha256) throw new Error(`界面资源校验失败：${entry.name}`);
}
const bundle = fs.readFileSync(path.join(source, 'assets', 'index.a2b61b89.js'), 'utf8');
const methods = [...new Set([...bundle.matchAll(/window\.hgCompat\.(\w+)/g)].map(match => match[1]))];
if (methods.length !== 118) throw new Error(`界面绑定数量异常：${methods.length}`);
const bridge = fs.readFileSync(path.join(root, 'frontend', 'compat', 'bridge.js'));
const revision = createHash('sha256').update(JSON.stringify(manifest)).update(bridge).digest('hex').slice(0, 12);
let html = fs.readFileSync(path.join(source, 'index.html'), 'utf8').replace('<head>', '<head>\n  <script src="/compat/bridge.js"></script>');
html = html.replace(/(src|href)="([^"?]+\.(?:js|css))"/g, `$1="$2?v=${revision}"`);
// Only the generated frontend/dist directory may be cleared. Validate sources first.
if (path.relative(root, out) !== path.join('frontend', 'dist')) throw new Error('前端输出目录无效');
fs.rmSync(out, { recursive: true, force: true });
fs.mkdirSync(out, { recursive: true });
fs.cpSync(source, out, { recursive: true });
fs.cpSync(path.join(root, 'frontend', 'compat'), path.join(out, 'compat'), { recursive: true });
fs.writeFileSync(path.join(out, 'index.html'), html);
console.log(`界面构建成功，已校验 ${manifest.files.length} 个资源、${methods.length} 个接口绑定`);
