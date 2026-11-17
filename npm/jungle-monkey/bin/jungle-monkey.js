#!/usr/bin/env node
// Launcher for the jungle-monkey Go binary. No dependencies, no postinstall:
// it finds the binary in the platform package npm installed (the
// optionalDependencies pattern of esbuild and Biome) or, when that package is
// missing, downloads it from the GitHub Release once, checks its SHA-256
// against checksums.txt shipped in this package and caches it.
'use strict';

const { spawn } = require('node:child_process');
const crypto = require('node:crypto');
const fs = require('node:fs');
const https = require('node:https');
const os = require('node:os');
const path = require('node:path');

const pkg = require('../package.json');

const TARGETS = {
  'darwin-arm64': '@jungle-monkey/darwin-arm64',
  'darwin-x64': '@jungle-monkey/darwin-x64',
  'linux-x64': '@jungle-monkey/linux-x64',
  'linux-arm64': '@jungle-monkey/linux-arm64',
  'win32-x64': '@jungle-monkey/win32-x64',
};

function target(platform = process.platform, arch = process.arch) {
  const key = `${platform}-${arch}`;
  if (!TARGETS[key]) {
    throw new Error(`jungle-monkey does not support ${key} (supported: ${Object.keys(TARGETS).join(', ')})`);
  }
  return key;
}

function binName(platform = process.platform) {
  return platform === 'win32' ? 'jungle-monkey.exe' : 'jungle-monkey';
}

// Release asset name, as in checksums.txt.
function assetName(key) {
  return `jungle-monkey-${key}${key.startsWith('win32') ? '.exe' : ''}`;
}

// fromPackage returns the binary of the installed platform package, or null.
function fromPackage(key, resolve = require.resolve) {
  try {
    const pj = resolve(`${TARGETS[key]}/package.json`);
    const bin = path.join(path.dirname(pj), 'bin', binName(key.split('-')[0]));
    return fs.existsSync(bin) ? bin : null;
  } catch {
    return null;
  }
}

function dataDir(env = process.env, platform = process.platform) {
  if (env.JM_DATA_DIR) return env.JM_DATA_DIR;
  const home = os.homedir();
  if (platform === 'darwin') return path.join(home, 'Library', 'Application Support', 'JungleMonkey');
  if (platform === 'win32') return path.join(env.LOCALAPPDATA || path.join(home, 'AppData', 'Local'), 'JungleMonkey');
  return path.join(env.XDG_DATA_HOME || path.join(home, '.local', 'share'), 'jungle-monkey');
}

function expectedSum(asset, checksums) {
  for (const line of checksums.split('\n')) {
    const [sum, name] = line.trim().split(/\s+\*?/);
    if (name === asset) return sum.toLowerCase();
  }
  return null;
}

function get(url, redirects = 5) {
  return new Promise((resolve, reject) => {
    https
      .get(url, { headers: { 'User-Agent': `jungle-monkey-launcher/${pkg.version}` } }, (res) => {
        if (res.statusCode >= 300 && res.statusCode < 400 && res.headers.location && redirects > 0) {
          res.resume();
          resolve(get(new URL(res.headers.location, url).toString(), redirects - 1));
          return;
        }
        if (res.statusCode !== 200) {
          res.resume();
          reject(new Error(`GET ${url}: HTTP ${res.statusCode}`));
          return;
        }
        const chunks = [];
        res.on('data', (c) => chunks.push(c));
        res.on('end', () => resolve(Buffer.concat(chunks)));
        res.on('error', reject);
      })
      .on('error', reject);
  });
}

// download fetches the release binary, rejects a checksum mismatch and caches
// it in <data dir>/bin/<version>/.
async function download(key, opts = {}) {
  const fetch = opts.fetch || get;
  const checksums = opts.checksums ?? fs.readFileSync(path.join(__dirname, '..', 'checksums.txt'), 'utf8');
  const asset = assetName(key);
  const want = expectedSum(asset, checksums);
  if (!want) throw new Error(`checksums.txt has no entry for ${asset}`);
  const dir = path.join(opts.dataDir || dataDir(), 'bin', pkg.version);
  const dest = path.join(dir, binName(key.split('-')[0]));
  if (fs.existsSync(dest)) {
    const have = crypto.createHash('sha256').update(fs.readFileSync(dest)).digest('hex');
    if (have === want) return dest;
  }
  const base = process.env.JM_DOWNLOAD_BASE || `https://github.com/AndrewSl105/jungle_monkey/releases/download/v${pkg.version}`;
  process.stderr.write(`Downloading jungle-monkey ${pkg.version} for ${key}…\n`);
  const body = await fetch(`${base}/${asset}`);
  const got = crypto.createHash('sha256').update(body).digest('hex');
  if (got !== want) {
    throw new Error(`checksum mismatch for ${asset}: expected ${want}, got ${got}; refusing to run it`);
  }
  fs.mkdirSync(dir, { recursive: true, mode: 0o700 });
  const tmp = `${dest}.${process.pid}.tmp`;
  fs.writeFileSync(tmp, body, { mode: 0o755 });
  fs.renameSync(tmp, dest);
  return dest;
}

// run execs the binary with our args and stdio, forwards signals, and exits
// with its code (or re-raises the signal that killed it).
function run(bin, args = process.argv.slice(2), proc = process, spawnFn = spawn) {
  const child = spawnFn(bin, args, { stdio: 'inherit', windowsHide: false });
  const forward = (sig) => () => {
    try {
      child.kill(sig);
    } catch {
      /* already gone */
    }
  };
  const handlers = {};
  for (const sig of ['SIGINT', 'SIGTERM', 'SIGHUP']) {
    handlers[sig] = forward(sig);
    proc.on(sig, handlers[sig]);
  }
  child.on('error', (err) => {
    proc.stderr.write(`jungle-monkey: ${err.message}\n`);
    proc.exit(1);
  });
  child.on('exit', (code, signal) => {
    for (const sig of Object.keys(handlers)) proc.removeListener(sig, handlers[sig]);
    if (signal) {
      proc.kill(proc.pid, signal);
      return;
    }
    proc.exit(code ?? 1);
  });
  return child;
}

async function main() {
  const key = target();
  const bin = process.env.JM_BINARY || fromPackage(key) || (await download(key));
  run(bin);
}

module.exports = { target, binName, assetName, fromPackage, dataDir, expectedSum, download, run, TARGETS };

if (require.main === module) {
  main().catch((err) => {
    process.stderr.write(`jungle-monkey: ${err.message}\n`);
    process.exit(1);
  });
}
