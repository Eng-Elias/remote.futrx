import { spawn } from 'node:child_process';
import { once } from 'node:events';
import { mkdir, mkdtemp, rm } from 'node:fs/promises';
import net from 'node:net';
import os from 'node:os';
import path from 'node:path';
import { setTimeout as delay } from 'node:timers/promises';
import { chromium } from 'playwright';

const loopbackHost = '127.0.0.1';

function positiveInteger(value, fallback) {
  const parsed = Number.parseInt(String(value || ''), 10);
  return Number.isInteger(parsed) && parsed > 0 ? parsed : fallback;
}

async function reserveLoopbackPort() {
  const server = net.createServer();
  server.unref();
  await new Promise((resolve, reject) => {
    server.once('error', reject);
    server.listen({ host: loopbackHost, port: 0, exclusive: true }, resolve);
  });
  const address = server.address();
  const port = typeof address === 'object' && address ? address.port : 0;
  await new Promise((resolve, reject) => server.close((error) => error ? reject(error) : resolve()));
  if (!port) throw new Error('could not allocate Chromium CDP port');
  return port;
}

export function directChromiumArguments({ profileDir, port, chromiumSandbox = true }) {
  const args = [
    `--user-data-dir=${profileDir}`,
    '--no-first-run',
    '--no-default-browser-check',
    '--disable-dev-shm-usage',
    '--disable-quic',
    '--disable-sync',
    '--force-webrtc-ip-handling-policy=disable_non_proxied_udp',
    `--remote-debugging-address=${loopbackHost}`,
    `--remote-debugging-port=${port}`,
    '--window-position=0,0',
    '--window-size=1280,720',
    'about:blank',
  ];
  if (!chromiumSandbox) args.unshift('--no-sandbox');
  return args;
}

async function waitForCDP(endpoint, child, getSpawnError, timeoutMs) {
  const deadline = Date.now() + timeoutMs;
  let lastError;
  while (Date.now() < deadline) {
    const spawnError = getSpawnError();
    if (spawnError) throw new Error(`could not launch Chromium: ${spawnError.message}`);
    if (child.exitCode !== null || child.signalCode !== null) {
      throw new Error(`Chromium exited before CDP was ready (code=${child.exitCode ?? 'none'}, signal=${child.signalCode ?? 'none'})`);
    }
    try {
      const response = await fetch(`${endpoint}/json/version`, {
        cache: 'no-store',
        signal: AbortSignal.timeout(500),
      });
      if (response.ok) {
        const version = await response.json();
        if (typeof version.webSocketDebuggerUrl === 'string') return;
      }
    } catch (error) {
      lastError = error;
    }
    await delay(50);
  }
  throw new Error(`Chromium CDP did not become ready within ${timeoutMs}ms${lastError?.message ? `: ${lastError.message}` : ''}`);
}

async function waitForExit(child, timeoutMs) {
  if (child.exitCode !== null || child.signalCode !== null) return true;
  return await Promise.race([
    once(child, 'exit').then(() => true),
    delay(timeoutMs, false, { ref: false }),
  ]);
}

async function terminate(child) {
  if (child.exitCode !== null || child.signalCode !== null) return;
  child.kill('SIGTERM');
  if (await waitForExit(child, 5_000)) return;
  child.kill('SIGKILL');
  await waitForExit(child, 2_000);
}

class AttachedChromium {
  constructor(browser, child, profileDir) {
    this.browser = browser;
    this.child = child;
    this.profileDir = profileDir;
    this.closePromise = null;
    this.cleanupPromise = null;
    child.once('exit', () => void this.cleanupProfile());
  }

  isConnected() {
    return this.browser.isConnected();
  }

  newContext(options) {
    return this.browser.newContext(options);
  }

  on(event, listener) {
    this.browser.on(event, listener);
    return this;
  }

  cleanupProfile() {
    if (!this.cleanupPromise) {
      this.cleanupPromise = rm(this.profileDir, {
        recursive: true,
        force: true,
        maxRetries: 5,
        retryDelay: 100,
      }).catch((error) => {
        console.error(`browser-broker: could not remove temporary Chromium profile: ${error.message}`);
      });
    }
    return this.cleanupPromise;
  }

  close() {
    if (!this.closePromise) {
      this.closePromise = (async () => {
        try {
          if (this.browser.isConnected()) await this.browser.close();
        } finally {
          await terminate(this.child);
          await this.cleanupProfile();
        }
      })();
    }
    return this.closePromise;
  }
}

// Launch Chrome as a normal systemd-cgroup child, then attach Playwright over
// a short-lived loopback CDP port. Playwright never supplies its automation
// launch arguments, while the broker still owns the browser lifecycle.
export class DirectChromiumLauncher {
  constructor({
    playwright = chromium,
    executablePath,
    runtimeDir = process.env.BROWSER_BROKER_RUNTIME_DIR || path.join(os.tmpdir(), 'remote-futrx-browser'),
    chromiumSandbox = true,
    startupTimeoutMs = positiveInteger(process.env.BROWSER_CDP_STARTUP_TIMEOUT_MS, 30_000),
    spawnProcess = spawn,
  } = {}) {
    this.playwright = playwright;
    this.executablePath = executablePath;
    this.runtimeDir = runtimeDir;
    this.chromiumSandbox = chromiumSandbox;
    this.startupTimeoutMs = startupTimeoutMs;
    this.spawnProcess = spawnProcess;
  }

  async launch() {
    await mkdir(this.runtimeDir, { recursive: true, mode: 0o700 });
    const profileDir = await mkdtemp(path.join(this.runtimeDir, 'profile-'));
    const port = await reserveLoopbackPort();
    const endpoint = `http://${loopbackHost}:${port}`;
    const executablePath = this.executablePath || this.playwright.executablePath();
    const child = this.spawnProcess(executablePath, directChromiumArguments({
      profileDir,
      port,
      chromiumSandbox: this.chromiumSandbox,
    }), {
      detached: false,
      env: process.env,
      stdio: ['ignore', 'ignore', 'pipe'],
    });
    let spawnError = null;
    let stderrTail = '';
    child.once('error', (error) => { spawnError = error; });
    child.stderr?.setEncoding('utf8');
    child.stderr?.on('data', (chunk) => {
      stderrTail = `${stderrTail}${chunk}`.slice(-16_384);
    });

    try {
      await waitForCDP(endpoint, child, () => spawnError, this.startupTimeoutMs);
      const browser = await this.playwright.connectOverCDP(endpoint, { timeout: this.startupTimeoutMs });
      // Keep Chrome's initial unscoped about:blank page alive. Closing the last
      // normal browser window exits a directly launched headed browser. The
      // blank default context is never routed to a viewer or MCP session;
      // project pages always live in explicit isolated BrowserContexts.
      return new AttachedChromium(browser, child, profileDir);
    } catch (error) {
      await terminate(child);
      await rm(profileDir, { recursive: true, force: true, maxRetries: 5, retryDelay: 100 }).catch(() => {});
      const diagnostics = stderrTail.trim();
      const failure = error instanceof Error ? error : new Error(String(error));
      if (diagnostics) failure.message = `${failure.message}\n${diagnostics}`;
      throw failure;
    }
  }
}
