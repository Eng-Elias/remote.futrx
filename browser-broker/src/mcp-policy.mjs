// Playwright MCP intentionally includes local-file and server-side code tools
// for a developer running it on their own machine. The shared broker is a
// multi-project host service, so those tools would turn one scoped browser
// token into access to the broker account's filesystem or process.
const blockedTools = new Set([
  'browser_drop',
  'browser_file_upload',
  'browser_run_code_unsafe',
]);

function errorResult(message) {
  return { content: [{ type: 'text', text: message }], isError: true };
}

function publicTool(tool) {
  if (!tool?.inputSchema?.properties?.filename) return tool;
  const properties = { ...tool.inputSchema.properties };
  delete properties.filename;
  return {
    ...tool,
    inputSchema: {
      ...tool.inputSchema,
      properties,
      required: tool.inputSchema.required?.filter((name) => name !== 'filename'),
    },
  };
}

// hardenMCPConnection wraps the pinned Playwright MCP server's registered SDK
// handlers. The package is version-pinned and tests fail closed if its handler
// layout changes.
export function hardenMCPConnection(server) {
  const handlers = server?._requestHandlers;
  const listTools = handlers?.get('tools/list');
  const callTool = handlers?.get('tools/call');
  if (!listTools || !callTool)
    throw new Error('unsupported Playwright MCP request-handler layout');

  handlers.set('tools/list', async (request, extra) => {
    const result = await listTools(request, extra);
    return {
      ...result,
      tools: result.tools.filter((tool) => !blockedTools.has(tool.name)).map(publicTool),
    };
  });
  handlers.set('tools/call', async (request, extra) => {
    const name = request?.params?.name;
    if (blockedTools.has(name))
      return errorResult(`Tool ${name} is unavailable on the shared browser broker`);
    const args = request?.params?.arguments;
    if (args && Object.hasOwn(args, 'filename'))
      return errorResult('Client-selected browser output paths are unavailable on the shared browser broker');
    return callTool(request, extra);
  });
  return server;
}

export function blockedMCPTools() {
  return new Set(blockedTools);
}
