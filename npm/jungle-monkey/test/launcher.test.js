'use strict';
const { test } = require('node:test');
const assert = require('node:assert');
const crypto = require('node:crypto');
const { EventEmitter } = require('node:events');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');

const L = require('../bin/jungle-monkey.js');

const sha = (b) => crypto.createHash('sha256').update(b).digest('hex');

test('target resolves supported platforms and rejects others', () => {
  assert.strictEqual(L.target('darwin', 'arm64'), 'darwin-arm64');
  assert.strictEqual(L.target('win32', 'x64'), 'win32-x64');
  assert.throws(() => L.target('freebsd', 'x64'), /does not support freebsd-x64/);
  assert.strictEqual(L.assetName('win32-x64'), 'jungle-monkey-win32-x64.exe');
  assert.strictEqual(L.binName('linux'), 'jungle-monkey');
});

test('fromPackage finds the platform package binary', () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'jm-'));
  fs.mkdirSync(path.join(dir, 'bin'));
  fs.writeFileSync(path.join(dir, 'package.json'), '{}');
  fs.writeFileSync(path.join(dir, 'bin', 'jungle-monkey'), 'bin');
  const resolve = (id) => {
    assert.strictEqual(id, '@jungle-monkey/linux-x64/package.json');
    return path.join(dir, 'package.json');
  };
  assert.strictEqual(L.fromPackage('linux-x64', resolve), path.join(dir, 'bin', 'jungle-monkey'));
  assert.strictEqual(L.fromPackage('linux-x64', () => { throw new Error('missing'); }), null);
});

test('dataDir per OS', () => {
  assert.strictEqual(L.dataDir({ JM_DATA_DIR: '/x' }, 'linux'), '/x');
  assert.strictEqual(L.dataDir({ XDG_DATA_HOME: '/xdg' }, 'linux'), path.join('/xdg', 'jungle-monkey'));
  assert.match(L.dataDir({}, 'darwin'), /Library.Application Support.JungleMonkey$/);
});

test('download verifies the checksum and caches the binary', async () => {
  const body = Buffer.from('real binary');
  const dataDir = fs.mkdtempSync(path.join(os.tmpdir(), 'jm-'));
  const checksums = `${sha(body)}  jungle-monkey-linux-x64\n${sha('x')}  jungle-monkey-darwin-arm64\n`;
  let calls = 0;
  const fetch = async () => { calls++; return body; };
  const bin = await L.download('linux-x64', { fetch, checksums, dataDir });
  assert.strictEqual(fs.readFileSync(bin, 'utf8'), 'real binary');
  if (process.platform !== 'win32') assert.strictEqual(fs.statSync(bin).mode & 0o777, 0o755);
  await L.download('linux-x64', { fetch, checksums, dataDir });
  assert.strictEqual(calls, 1, 'cached binary is reused');
});

test('download rejects a checksum mismatch and writes nothing', async () => {
  const dataDir = fs.mkdtempSync(path.join(os.tmpdir(), 'jm-'));
  const checksums = `${sha('expected')}  jungle-monkey-linux-x64\n`;
  await assert.rejects(
    L.download('linux-x64', { fetch: async () => Buffer.from('tampered'), checksums, dataDir }),
    /checksum mismatch/,
  );
  assert.deepStrictEqual(fs.readdirSync(dataDir), []);
  await assert.rejects(L.download('win32-x64', { fetch: async () => Buffer.from(''), checksums, dataDir }), /no entry/);
});

test('run forwards args, signals and the exit code', () => {
  const proc = new EventEmitter();
  let exited = null;
  proc.exit = (c) => { exited = c; };
  proc.stderr = { write() {} };
  const child = new EventEmitter();
  const killed = [];
  child.kill = (s) => killed.push(s);
  let spawned;
  L.run('/bin/jm', ['status', '--x'], proc, (bin, args, opts) => {
    spawned = { bin, args, opts };
    return child;
  });
  assert.deepStrictEqual(spawned.args, ['status', '--x']);
  assert.strictEqual(spawned.opts.stdio, 'inherit');
  proc.emit('SIGINT');
  proc.emit('SIGTERM');
  assert.deepStrictEqual(killed, ['SIGINT', 'SIGTERM']);
  child.emit('exit', 2, null);
  assert.strictEqual(exited, 2);
  assert.strictEqual(proc.listenerCount('SIGINT'), 0);
});

test('run re-raises the signal that killed the binary', () => {
  const proc = new EventEmitter();
  proc.pid = 123;
  let raised;
  proc.kill = (pid, sig) => { raised = [pid, sig]; };
  proc.exit = () => assert.fail('must not exit with a code');
  const child = new EventEmitter();
  child.kill = () => {};
  L.run('/bin/jm', [], proc, () => child);
  child.emit('exit', null, 'SIGTERM');
  assert.deepStrictEqual(raised, [123, 'SIGTERM']);
});

test('package has no install scripts and supports Node 18', () => {
  const pj = require('../package.json');
  for (const s of ['preinstall', 'install', 'postinstall']) assert.ok(!pj.scripts?.[s], s);
  assert.strictEqual(pj.engines.node, '>=18');
  assert.deepStrictEqual(Object.keys(pj.optionalDependencies).sort(), Object.values(L.TARGETS).sort());
});
