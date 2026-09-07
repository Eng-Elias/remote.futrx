import assert from 'node:assert/strict';
import test from 'node:test';
import { directChromiumArguments } from '../src/direct-chromium.mjs';

test('direct Chromium launch stays headed, sandboxed, and loopback-only', () => {
  const args = directChromiumArguments({
    profileDir: '/run/remote-futrx-browser/profile-test',
    port: 19324,
  });

  assert.ok(args.includes('--user-data-dir=/run/remote-futrx-browser/profile-test'));
  assert.ok(args.includes('--remote-debugging-address=127.0.0.1'));
  assert.ok(args.includes('--remote-debugging-port=19324'));
  assert.ok(!args.includes('--remote-debugging-port=0'));
  assert.ok(!args.includes('--enable-automation'));
  assert.ok(!args.includes('--headless'));
  assert.ok(!args.includes('--no-sandbox'));
});

test('sandbox bypass is an explicit launcher option', () => {
  const args = directChromiumArguments({
    profileDir: '/tmp/profile-test',
    port: 19324,
    chromiumSandbox: false,
  });
  assert.ok(args.includes('--no-sandbox'));
});
