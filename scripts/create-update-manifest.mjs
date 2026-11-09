import { readFile, writeFile } from 'node:fs/promises';
import { basename, dirname, join } from 'node:path';
import { createHash } from 'node:crypto';

const [version, repository, executable] = process.argv.slice(2);
if (!/^\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?$/.test(version || '') ||
    !/^[A-Za-z0-9_.-]+\/[A-Za-z0-9_.-]+$/.test(repository || '') ||
    basename(executable || '') !== `DuanjuDesktop-${version}-windows-amd64.exe`) {
  throw new Error('Usage: create-update-manifest.mjs VERSION OWNER/REPO dist/DuanjuDesktop-VERSION-windows-amd64.exe');
}
const bytes = await readFile(executable);
const sha256 = createHash('sha256').update(bytes).digest('hex');
let notes = '';
try {
  const log = await readFile('CHANGELOG.md', 'utf8');
  notes = log.split(`## ${version}\n`)[1]?.split('\n## ')[0]?.trim() || '';
} catch (error) { if (error.code !== 'ENOENT') throw error; }
const index = { version, notes, pubDate: Math.floor(Date.now() / 1000), packages: [{
  platform: 'windows', arch: 'amd64', size: bytes.length, sha256,
  url: `https://github.com/${repository}/releases/download/v${version}/${basename(executable)}`,
}] };
await writeFile(join(dirname(executable), 'update.json'), JSON.stringify(index, null, 2) + '\n');
await writeFile(join(dirname(executable), 'SHA256SUMS.txt'), `${sha256}  ${basename(executable)}\n`);
console.log(`SHA256: ${sha256}`);
