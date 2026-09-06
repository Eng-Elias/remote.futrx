import assert from 'node:assert/strict';
import test from 'node:test';
import { blockedMCPTools, hardenMCPConnection } from '../src/mcp-policy.mjs';

test('shared MCP policy hides host-file and server-code tools', async () => {
  const handlers = new Map([
    ['tools/list', async () => ({
      tools: [
        { name: 'browser_navigate', inputSchema: { type: 'object', properties: {} } },
        { name: 'browser_file_upload', inputSchema: { type: 'object', properties: {} } },
        { name: 'browser_run_code_unsafe', inputSchema: { type: 'object', properties: {} } },
        {
          name: 'browser_take_screenshot',
          inputSchema: { type: 'object', properties: { filename: { type: 'string' }, type: { type: 'string' } }, required: ['filename'] },
        },
      ],
    })],
    ['tools/call', async () => ({ content: [{ type: 'text', text: 'called' }] })],
  ]);
  hardenMCPConnection({ _requestHandlers: handlers });

  const listed = await handlers.get('tools/list')({}, {});
  assert.deepEqual(listed.tools.map((tool) => tool.name), ['browser_navigate', 'browser_take_screenshot']);
  assert.equal(listed.tools[1].inputSchema.properties.filename, undefined);
  assert.deepEqual(listed.tools[1].inputSchema.required, []);

  for (const name of blockedMCPTools()) {
    const result = await handlers.get('tools/call')({ params: { name, arguments: {} } }, {});
    assert.equal(result.isError, true);
  }
  const pathResult = await handlers.get('tools/call')({
    params: { name: 'browser_take_screenshot', arguments: { filename: '/etc/passwd' } },
  }, {});
  assert.equal(pathResult.isError, true);
  const allowed = await handlers.get('tools/call')({ params: { name: 'browser_navigate', arguments: {} } }, {});
  assert.equal(allowed.isError, undefined);
});
