import assert from 'node:assert/strict';
import { randomBytes } from 'node:crypto';
import { mkdtemp, rm } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';
import { BrowserPool } from '../src/browser-pool.mjs';
import { EncryptedStateStore } from '../src/state-store.mjs';

const runBrowserIntegration = process.env.BROWSER_BROKER_INTEGRATION === '1';

test('one Chromium serves isolated project contexts and restores encrypted login state', {
  skip: !runBrowserIntegration,
  timeout: 30_000,
}, async () => {
  const root = await mkdtemp(path.join(os.tmpdir(), 'remote-browser-integration-'));
  const stateStore = new EncryptedStateStore(root, randomBytes(48));
  const pool = new BrowserPool({ stateStore, idleBrowserMs: 10 });
  try {
    const alpha = await pool.ensure('alpha');
    const browser = pool.browser;
    const beta = await pool.ensure('beta');
    assert.equal(pool.browser, browser);
    assert.notEqual(alpha.context, beta.context);

    await alpha.context.addCookies([{
      name: 'account', value: 'alpha-user', domain: 'example.com', path: '/',
    }]);
    assert.equal((await alpha.context.cookies('https://example.com'))[0]?.value, 'alpha-user');
    assert.equal((await beta.context.cookies('https://example.com')).length, 0);
    assert.equal(await alpha.context.pages()[0].evaluate(() => typeof RTCPeerConnection), 'undefined');

    const [firstConcurrent, secondConcurrent] = await Promise.all([
      pool.ensure('concurrent'),
      pool.ensure('concurrent', { view: true }),
    ]);
    assert.equal(firstConcurrent, secondConcurrent);
    assert.equal(pool.status('concurrent').view, 'ready');

    await Promise.all([pool.ensure('stop-race'), pool.stop('stop-race')]);
    assert.equal(pool.status('stop-race').status, 'stopped');

    await pool.stop('alpha');
    const restored = await pool.ensure('alpha');
    assert.equal((await restored.context.cookies('https://example.com'))[0]?.value, 'alpha-user');
    assert.equal((await beta.context.cookies('https://example.com')).length, 0);

    await pool.delete('alpha');
    const recreated = await pool.ensure('alpha');
    assert.equal((await recreated.context.cookies('https://example.com')).length, 0);
  } finally {
    await pool.close();
    await rm(root, { recursive: true, force: true });
  }
});
