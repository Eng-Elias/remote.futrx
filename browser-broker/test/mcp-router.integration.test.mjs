import assert from 'node:assert/strict';
import { randomBytes } from 'node:crypto';
import { mkdtemp, rm } from 'node:fs/promises';
import http from 'node:http';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';
import { Client } from '@modelcontextprotocol/sdk/client/index.js';
import { StreamableHTTPClientTransport } from '@modelcontextprotocol/sdk/client/streamableHttp.js';
import { BrowserPool } from '../src/browser-pool.mjs';
import { DirectChromiumLauncher } from '../src/direct-chromium.mjs';
import { MCPRouter } from '../src/mcp-router.mjs';
import { EncryptedStateStore } from '../src/state-store.mjs';

const runBrowserIntegration = process.env.BROWSER_BROKER_INTEGRATION === '1';

async function listen(server) {
  await new Promise((resolve, reject) => {
    server.once('error', reject);
    server.listen(0, '127.0.0.1', resolve);
  });
  return server.address().port;
}

async function closeServer(server) {
  await new Promise((resolve) => server.close(resolve));
}

test('streamable HTTP MCP sessions are bound to separate project contexts', {
  skip: !runBrowserIntegration,
  timeout: 30_000,
}, async () => {
  const root = await mkdtemp(path.join(os.tmpdir(), 'remote-browser-mcp-'));
  const stateStore = new EncryptedStateStore(root, randomBytes(48));
  const pool = new BrowserPool({
    stateStore,
    launcher: new DirectChromiumLauncher({
      runtimeDir: path.join(root, 'runtime'),
      chromiumSandbox: process.getuid?.() !== 0,
    }),
  });
  const router = new MCPRouter(pool, { outputRoot: path.join(root, 'output') });
  const server = http.createServer((request, response) => {
    const project = request.headers['x-test-project'];
    if (typeof project !== 'string') {
      response.writeHead(401).end();
      return;
    }
    void router.handle(project, request, response).catch((error) => {
      response.writeHead(500).end(error.message);
    });
  });
  const clients = [];
  try {
    const port = await listen(server);
    for (const project of ['alpha', 'beta']) {
      await pool.ensure(project);
      const client = new Client({ name: `test-${project}`, version: '1.0.0' });
      const transport = new StreamableHTTPClientTransport(new URL(`http://127.0.0.1:${port}/mcp`), {
        requestInit: { headers: { 'x-test-project': project } },
      });
      await client.connect(transport);
      clients.push(client);
      const tools = await client.listTools();
      const toolNames = new Set(tools.tools.map((tool) => tool.name));
      assert.ok(toolNames.has('browser_navigate'));
      for (const unavailable of [
        'browser_run_code_unsafe',
        'browser_file_upload',
        'browser_drop',
        'browser_route',
        'browser_unroute',
        'browser_set_storage_state',
        'browser_get_config',
        'browser_start_tracing',
      ]) assert.equal(toolNames.has(unavailable), false, `${unavailable} must not be exposed`);
      for (const tool of tools.tools)
        assert.equal(Object.hasOwn(tool.inputSchema.properties || {}, 'filename'), false, `${tool.name} exposes filename`);
      assert.equal(
        Object.hasOwn(tools.tools.find((tool) => tool.name === 'browser_take_screenshot').inputSchema.properties, 'filename'),
        false,
      );
      await client.callTool({
        name: 'browser_navigate',
        arguments: { url: `data:text/html,<title>${project}</title><h1>${project}</h1>` },
      });
    }

    assert.equal(pool.health().contexts, 2);
    const alpha = pool.records.get('alpha');
    const beta = pool.records.get('beta');
    assert.notEqual(alpha.context, beta.context);
    assert.equal(alpha.context.pages().at(-1).url().includes('alpha'), true);
    assert.equal(beta.context.pages().at(-1).url().includes('beta'), true);
  } finally {
    await Promise.allSettled(clients.map((client) => client.close()));
    await router.close();
    await pool.close();
    await closeServer(server);
    await rm(root, { recursive: true, force: true });
  }
});
