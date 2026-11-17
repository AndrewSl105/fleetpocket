// Builds the npm packages into npm/dist: one platform package per target with
// the binary from <bins>/jungle-monkey-<key>[.exe], and the main package with
// checksums.txt, all at the same version.
//
//   node npm/build.mjs <version> <bins dir>
import { createHash } from 'node:crypto';
import { cpSync, mkdirSync, readFileSync, rmSync, writeFileSync, chmodSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const here = dirname(fileURLToPath(import.meta.url));
const [version, bins] = process.argv.slice(2);
if (!version || !bins) {
  console.error('usage: node npm/build.mjs <version> <bins dir>');
  process.exit(2);
}
const v = version.replace(/^v/, '');
const out = join(here, 'dist');
rmSync(out, { recursive: true, force: true });
const platforms = JSON.parse(readFileSync(join(here, 'platforms.json'), 'utf8'));
const sums = [];
for (const p of platforms) {
  const exe = p.os === 'win32' ? '.exe' : '';
  const asset = `jungle-monkey-${p.key}${exe}`;
  const body = readFileSync(join(bins, asset));
  sums.push(`${createHash('sha256').update(body).digest('hex')}  ${asset}`);
  const dir = join(out, p.key);
  mkdirSync(join(dir, 'bin'), { recursive: true });
  const bin = join(dir, 'bin', `jungle-monkey${exe}`);
  writeFileSync(bin, body);
  chmodSync(bin, 0o755);
  writeFileSync(join(dir, 'package.json'), JSON.stringify({
    name: `@jungle-monkey/${p.key}`,
    version: v,
    description: `Self-hosted orchestrator for coding agents: Claude Code, Codex, Cursor (jungle-monkey binary for ${p.key})`,
    license: 'MIT',
    repository: { type: 'git', url: 'git+https://github.com/AndrewSl105/jungle_monkey.git', directory: 'npm' },
    os: [p.os],
    cpu: [p.cpu],
    files: ['bin/'],
    preferUnplugged: true,
  }, null, 2) + '\n');
}
const main = join(out, 'jungle-monkey');
cpSync(join(here, 'jungle-monkey'), main, { recursive: true, filter: (s) => !s.includes(`${'/'}test`) });
const pj = JSON.parse(readFileSync(join(main, 'package.json'), 'utf8'));
pj.version = v;
for (const k of Object.keys(pj.optionalDependencies)) pj.optionalDependencies[k] = v;
delete pj.scripts;
writeFileSync(join(main, 'package.json'), JSON.stringify(pj, null, 2) + '\n');
writeFileSync(join(main, 'checksums.txt'), sums.join('\n') + '\n');
writeFileSync(join(bins, 'checksums.txt'), sums.join('\n') + '\n');
console.log(`built ${platforms.length + 1} packages at ${v} in ${out}`);
