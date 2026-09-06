import { createHash, randomUUID } from 'node:crypto';
import { mkdir, rm } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { createConnection } from '@playwright/mcp';
import { StreamableHTTPServerTransport } from '@modelcontextprotocol/sdk/server/streamableHttp.js';
import { hardenMCPConnection } from './mcp-policy.mjs';

export class MCPRouter {
  constructor(pool, { maxSessions = 128, maxSessionsPerProject = 8, outputRoot } = {}) {
    this.pool = pool;
    this.maxSessions = maxSessions;
    this.maxSessionsPerProject = maxSessionsPerProject;
    this.outputRoot = outputRoot || path.join(os.tmpdir(), 'remote-futrx-browser-output');
    this.sessions = new Map();
    this.pendingSessions = 0;
    this.pendingByProject = new Map();
    this.projectEpochs = new Map();
    this.blockedProjects = new Set();
  }

  async handle(project, request, response) {
    if (this.blockedProjects.has(project)) {
      response.writeHead(409).end('Browser context is stopping');
      return;
    }
    const sessionID = request.headers['mcp-session-id'];
    if (sessionID) {
      const session = this.sessions.get(sessionID);
      if (!session || session.project !== project) {
        response.writeHead(404).end('MCP session not found');
        return;
      }
      await session.transport.handleRequest(request, response);
      if (request.method === 'DELETE') this.sessions.delete(sessionID);
      return;
    }

    if (request.method !== 'POST') {
      response.writeHead(400).end('Missing MCP session');
      return;
    }
    if (this.sessions.size + this.pendingSessions >= this.maxSessions ||
        this.sessionCount(project) + (this.pendingByProject.get(project) || 0) >= this.maxSessionsPerProject) {
      response.writeHead(429).end('MCP session limit reached');
      return;
    }

    this.pendingSessions++;
    this.pendingByProject.set(project, (this.pendingByProject.get(project) || 0) + 1);
    let transport;
    try {
      const record = this.pool.records.get(project);
      if (!record) {
        response.writeHead(409).end('Browser context is not running');
        return;
      }
      const epoch = this.projectEpoch(project);
      const outputDir = this.outputDirectory(project);
      await mkdir(outputDir, { recursive: true, mode: 0o700 });
      if (this.projectEpoch(project) !== epoch || this.pool.records.get(project) !== record) {
        response.writeHead(409).end('Browser context stopped during MCP initialization');
        return;
      }

      let session;
      let server;
      transport = new StreamableHTTPServerTransport({
        sessionIdGenerator: randomUUID,
        onsessioninitialized: (id) => {
          if (this.projectEpoch(project) !== epoch || this.pool.records.get(project) !== record) {
            queueMicrotask(() => void transport.close().catch(() => {}));
            return;
          }
          session = { project, transport, server };
          this.sessions.set(id, session);
        },
      });
      server = hardenMCPConnection(await createConnection({
        browser: { isolated: false },
        capabilities: ['vision'],
        outputDir,
        outputMaxSize: 64 * 1024 * 1024,
      }, async () => record.context));
      const previousOnClose = server.onclose;
      server.onclose = () => {
        previousOnClose?.();
        if (transport.sessionId) this.sessions.delete(transport.sessionId);
      };

      await server.connect(transport);
      await transport.handleRequest(request, response);
      if (this.projectEpoch(project) !== epoch || this.pool.records.get(project) !== record) {
        if (transport.sessionId) this.sessions.delete(transport.sessionId);
        await transport.close().catch(() => {});
        return;
      }
      if (!session) await transport.close().catch(() => {});
    } catch (error) {
      if (transport) {
        if (transport.sessionId) this.sessions.delete(transport.sessionId);
        await transport.close().catch(() => {});
      }
      throw error;
    } finally {
      this.pendingSessions--;
      const pending = (this.pendingByProject.get(project) || 1) - 1;
      if (pending > 0) this.pendingByProject.set(project, pending);
      else this.pendingByProject.delete(project);
    }
  }

  async closeProject(project) {
    this.blockedProjects.add(project);
    this.projectEpochs.set(project, this.projectEpoch(project) + 1);
    const closing = [];
    for (const [id, session] of this.sessions) {
      if (session.project !== project) continue;
      this.sessions.delete(id);
      closing.push(session.transport.close().catch(() => {}));
    }
    await Promise.allSettled(closing);
  }

  openProject(project) {
    if (!this.blockedProjects.delete(project)) return;
    this.projectEpochs.set(project, this.projectEpoch(project) + 1);
  }

  async deleteProjectArtifacts(project) {
    await rm(this.outputDirectory(project), { recursive: true, force: true });
  }

  async close() {
    const sessions = [...this.sessions.values()];
    this.sessions.clear();
    await Promise.allSettled(sessions.map((session) => session.transport.close()));
  }

  sessionCount(project) {
    let count = 0;
    for (const session of this.sessions.values()) {
      if (session.project === project) count++;
    }
    return count;
  }

  projectEpoch(project) {
    return this.projectEpochs.get(project) || 0;
  }

  outputDirectory(project) {
    const digest = createHash('sha256').update(project).digest('hex');
    return path.join(this.outputRoot, digest);
  }
}
