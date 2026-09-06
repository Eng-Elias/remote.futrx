import WebSocket from 'ws';

function socketOpen(socket) {
  return socket.readyState === WebSocket.OPEN;
}

function safeURL(value) {
  const trimmed = String(value || '').trim();
  if (!trimmed) return 'about:blank';
  const candidate = /^[a-z][a-z0-9+.-]*:/i.test(trimmed) ? trimmed : `https://${trimmed}`;
  const parsed = new URL(candidate);
  if (!['http:', 'https:', 'about:'].includes(parsed.protocol)) throw new Error('unsupported navigation protocol');
  return parsed.href;
}

export class ViewSession {
  constructor(record, socket) {
    this.record = record;
    this.socket = socket;
    this.page = null;
    this.cdp = null;
    this.generation = 0;
    this.tabsTimer = null;
    this.pageListeners = null;
    this.messageQueue = Promise.resolve();
    this.pendingMessages = 0;
    this.closed = false;
    this.lastFrameAt = 0;
  }

  async start() {
    this.record.viewers.add(this.socket);
    this.socket.on('message', (data) => {
      if (++this.pendingMessages > 256) {
        this.pendingMessages--;
        this.socket.close(1008, 'browser input queue limit reached');
        return;
      }
      this.messageQueue = this.messageQueue
        .then(() => this.onMessage(data))
        .catch((error) => this.send({ type: 'error', message: error.message || 'Browser action failed' }))
        .finally(() => { this.pendingMessages--; });
    });
    this.socket.on('close', () => void this.close());
    this.socket.on('error', () => {});
    this.record.context.on('page', this.onPage);
    this.tabsTimer = setInterval(() => void this.sendTabs(), 1000);
    this.tabsTimer.unref?.();
    const pages = this.record.context.pages();
    await this.selectPage(pages.at(-1) || await this.record.context.newPage());
  }

  onPage = (page) => {
    void this.selectPage(page);
  };

  pageID(page) {
    let id = this.record.pageIDs.get(page);
    if (!id) {
      id = String(this.record.nextPageID++);
      this.record.pageIDs.set(page, id);
    }
    return id;
  }

  send(payload) {
    if (socketOpen(this.socket)) this.socket.send(JSON.stringify(payload));
  }

  async sendTabs() {
    const pages = this.record.context.pages();
    const tabs = await Promise.all(pages.map(async (page) => ({
      id: this.pageID(page),
      title: (await page.title().catch(() => '')) || 'New tab',
      url: page.url(),
      active: page === this.page,
    })));
    this.send({ type: 'tabs', tabs });
  }

  async selectPage(page) {
    if (!page || page.isClosed() || page === this.page) {
      await this.sendTabs();
      return;
    }
    const generation = ++this.generation;
    const previous = this.cdp;
    this.cdp = null;
    this.removePageListeners();
    if (previous) {
      await previous.send('Page.stopScreencast').catch(() => {});
      await previous.detach().catch(() => {});
    }
    this.page = page;
    await page.bringToFront().catch(() => {});
    const cdp = await this.record.context.newCDPSession(page);
    if (generation !== this.generation) {
      await cdp.detach().catch(() => {});
      return;
    }
    this.cdp = cdp;
    this.lastFrameAt = 0;
    cdp.on('Page.screencastFrame', (event) => {
      void cdp.send('Page.screencastFrameAck', { sessionId: event.sessionId }).catch(() => {});
      if (this.cdp !== cdp || !socketOpen(this.socket) || this.socket.bufferedAmount > 2_000_000) return;
      const now = Date.now();
      if (now - this.lastFrameAt < 66) return;
      this.lastFrameAt = now;
      const viewport = page.viewportSize() || { width: 1280, height: 720 };
      this.send({ type: 'frame', data: event.data, width: viewport.width, height: viewport.height });
    });
    const onClose = () => {
      if (this.page !== page) return;
      this.generation++;
      this.page = null;
      this.cdp = null;
      this.removePageListeners();
      void cdp.detach().catch(() => {});
      const replacement = this.record.context.pages().at(-1);
      if (replacement) void this.selectPage(replacement);
      else void this.sendTabs();
    };
    const onFrameNavigated = (frame) => {
      if (frame === page.mainFrame()) void this.sendTabs();
    };
    page.once('close', onClose);
    page.on('framenavigated', onFrameNavigated);
    this.pageListeners = { page, onClose, onFrameNavigated };
    await cdp.send('Page.enable');
    await cdp.send('Page.startScreencast', {
      format: 'jpeg',
      quality: 72,
      maxWidth: 1280,
      maxHeight: 720,
      everyNthFrame: 1,
    });
    const initial = await page.screenshot({ type: 'jpeg', quality: 72 }).catch(() => null);
    if (initial) {
      const viewport = page.viewportSize() || { width: 1280, height: 720 };
      this.send({ type: 'frame', data: initial.toString('base64'), width: viewport.width, height: viewport.height });
    }
    await this.sendTabs();
  }

  async onMessage(data) {
    if (this.closed || data.length > 64 * 1024) return;
    let message;
    try {
      message = JSON.parse(data.toString('utf8'));
    } catch {
      return;
    }
    const page = this.page;
    const cdp = this.cdp;
    this.record.lastActivity = Date.now();
    try {
      switch (message.type) {
        case 'mouse':
          if (!page || page.isClosed() || !cdp) return;
          await cdp.send('Input.dispatchMouseEvent', {
            type: message.eventType,
            x: Number(message.x) || 0,
            y: Number(message.y) || 0,
            button: message.button || 'none',
            buttons: Number(message.buttons) || 0,
            clickCount: Number(message.clickCount) || 0,
            deltaX: Number(message.deltaX) || 0,
            deltaY: Number(message.deltaY) || 0,
            modifiers: Number(message.modifiers) || 0,
          });
          break;
        case 'key':
          if (!page || page.isClosed() || !cdp) return;
          await cdp.send('Input.dispatchKeyEvent', {
            type: message.eventType === 'keyUp' ? 'keyUp' : 'keyDown',
            key: String(message.key || ''),
            code: String(message.code || ''),
            text: message.eventType === 'keyDown' ? String(message.text || '') : '',
            unmodifiedText: message.eventType === 'keyDown' ? String(message.text || '') : '',
            modifiers: Number(message.modifiers) || 0,
          });
          break;
        case 'insertText':
          if (page && !page.isClosed() && cdp && typeof message.text === 'string')
            await cdp.send('Input.insertText', { text: message.text.slice(0, 32_768) });
          break;
        case 'navigate':
          if (page && !page.isClosed()) await page.goto(safeURL(message.url));
          break;
        case 'reload':
          if (page && !page.isClosed()) await page.reload();
          break;
        case 'back':
          if (page && !page.isClosed()) await page.goBack();
          break;
        case 'forward':
          if (page && !page.isClosed()) await page.goForward();
          break;
        case 'newPage':
          await this.selectPage(await this.record.context.newPage());
          break;
        case 'selectPage': {
          const selected = this.record.context.pages().find((candidate) => this.pageID(candidate) === String(message.id));
          if (selected) await this.selectPage(selected);
          break;
        }
        case 'closePage': {
          const selected = this.record.context.pages().find((candidate) => this.pageID(candidate) === String(message.id));
          if (selected) await selected.close();
          if (!this.record.context.pages().length) await this.selectPage(await this.record.context.newPage());
          break;
        }
      }
    } catch (error) {
      this.send({ type: 'error', message: error.message || 'Browser action failed' });
    }
  }

  async close() {
    if (this.closed) return;
    this.closed = true;
    if (this.tabsTimer) clearInterval(this.tabsTimer);
    this.tabsTimer = null;
    this.record.context.off('page', this.onPage);
    this.removePageListeners();
    this.record.viewers.delete(this.socket);
    this.generation++;
    const cdp = this.cdp;
    this.cdp = null;
    if (cdp) {
      await cdp.send('Page.stopScreencast').catch(() => {});
      await cdp.detach().catch(() => {});
    }
  }

  removePageListeners() {
    if (!this.pageListeners) return;
    const { page, onClose, onFrameNavigated } = this.pageListeners;
    page.off('close', onClose);
    page.off('framenavigated', onFrameNavigated);
    this.pageListeners = null;
  }
}
