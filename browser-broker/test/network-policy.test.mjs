import assert from 'node:assert/strict';
import { test } from 'node:test';
import { isPrivateAddress, ProjectNetworkPolicy } from '../src/network-policy.mjs';

test('private and metadata addresses are blocked', () => {
  for (const address of [
    '127.0.0.1',
    '10.0.0.2',
    '100.100.100.200',
    '172.16.4.2',
    '192.168.1.4',
    '169.254.169.254',
    '198.18.0.1',
    '::1',
    '::ffff:127.0.0.1',
    'fd00::1',
  ])
    assert.equal(isPrivateAddress(address), true, address);
  assert.equal(isPrivateAddress('8.8.8.8'), false);
  assert.equal(isPrivateAddress('2606:4700:4700::1111'), false);
});

test('one project may reach only its own lxd hostname', async () => {
  const policy = new ProjectNetworkPolicy('alpha', {
    lookup: async () => [{ address: '93.184.216.34', family: 4 }],
  });
  assert.equal(await policy.allows('http://alpha.lxd:3000/'), true);
  assert.equal(await policy.allows('http://beta.lxd:3000/'), false);
  assert.equal(await policy.allows('http://169.254.169.254/latest/meta-data'), false);
  assert.equal(await policy.allows('http://[::ffff:127.0.0.1]/'), false);
  assert.equal(await policy.allows('https://example.com/'), true);
});
