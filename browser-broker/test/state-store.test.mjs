import assert from 'node:assert/strict';
import { copyFile, mkdir, readFile, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { mkdtemp } from 'node:fs/promises';
import { test } from 'node:test';
import { EncryptedStateStore } from '../src/state-store.mjs';

test('browser storage state is encrypted at rest and round trips', async (t) => {
  const root = await mkdtemp(path.join(tmpdir(), 'browser-state-'));
  t.after(() => rm(root, { recursive: true, force: true }));
  const store = new EncryptedStateStore(root, Buffer.from('0123456789abcdef0123456789abcdef'));
  const state = { cookies: [{ name: 'session', value: 'super-secret-cookie' }], origins: [] };
  await store.save('alpha', state);

  const disk = await readFile(path.join(store.directory('alpha'), 'storage-state.enc.json'), 'utf8');
  assert.equal(disk.includes('super-secret-cookie'), false);
  assert.deepEqual(await store.load('alpha'), state);
  await store.delete('alpha');
  assert.equal(await store.load('alpha'), undefined);
});

test('state encrypted under another installation key cannot be read', async (t) => {
  const root = await mkdtemp(path.join(tmpdir(), 'browser-state-'));
  t.after(() => rm(root, { recursive: true, force: true }));
  const first = new EncryptedStateStore(root, Buffer.from('0123456789abcdef0123456789abcdef'));
  await first.save('alpha', { cookies: [], origins: [] });
  const second = new EncryptedStateStore(root, Buffer.from('abcdef0123456789abcdef0123456789'));
  await assert.rejects(() => second.load('alpha'), /authentication failed/);
});

test('encrypted state cannot be moved into another project', async (t) => {
  const root = await mkdtemp(path.join(tmpdir(), 'browser-state-'));
  t.after(() => rm(root, { recursive: true, force: true }));
  const store = new EncryptedStateStore(root, Buffer.from('0123456789abcdef0123456789abcdef'));
  await store.save('alpha', { cookies: [{ name: 'account', value: 'alpha' }], origins: [] });
  await mkdir(store.directory('beta'), { recursive: true });
  await copyFile(
    path.join(store.directory('alpha'), 'storage-state.enc.json'),
    path.join(store.directory('beta'), 'storage-state.enc.json'),
  );
  await assert.rejects(() => store.load('beta'), /authentication failed/);
});
