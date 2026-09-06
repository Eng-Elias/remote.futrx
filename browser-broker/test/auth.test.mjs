import assert from 'node:assert/strict';
import { test } from 'node:test';
import {
  issueControlToken,
  issueProjectToken,
  verifyControlToken,
  verifyProjectToken,
} from '../src/auth.mjs';

const secret = Buffer.from('0123456789abcdef0123456789abcdef');

test('project tokens are scoped and tamper evident', () => {
  const token = issueProjectToken(secret, 'alpha-project');
  assert.equal(token, 'v1.YWxwaGEtcHJvamVjdA.Lz53nHpENrHBHc_cjqK4A1KqVkuEaoApWsXEG-wyy4c');
  assert.equal(verifyProjectToken(secret, token), 'alpha-project');
  assert.equal(verifyProjectToken(Buffer.from('abcdef0123456789abcdef0123456789'), token), null);
  assert.equal(verifyProjectToken(secret, `${token.slice(0, -1)}x`), null);

  const control = issueControlToken(secret, 'alpha-project');
  assert.match(control, /^v1c\./);
  assert.equal(verifyControlToken(secret, control), 'alpha-project');
  assert.equal(verifyProjectToken(secret, control), null);
  assert.equal(verifyControlToken(secret, token), null);
});

test('invalid project keys are rejected', () => {
  assert.throws(() => issueProjectToken(secret, '../other-project'));
  assert.throws(() => issueProjectToken(secret, 'UPPERCASE'));
});
