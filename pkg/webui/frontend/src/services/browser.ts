type PendingCommand = {
  resolve: (value: Record<string, unknown>) => void;
  reject: (error: Error) => void;
  timeout: ReturnType<typeof setTimeout>;
};

interface BrowserCDPHandlers {
  onOpen: () => void;
  onEvent: (method: string, params: Record<string, unknown>) => void;
  onClose: (reason: string) => void;
}

// Each viewport owns one page connection; DevTools uses its own connection to the same page.
export class BrowserCDP {
  private nextID = 0;
  private closed = false;
  private pending = new Map<number, PendingCommand>();
  private connectionTimeout: ReturnType<typeof setTimeout>;

  constructor(
    private socket: WebSocket,
    private handlers: BrowserCDPHandlers
  ) {
    this.connectionTimeout = setTimeout(
      () => this.finish('Browser connection timed out. Reconnect to retry.'),
      20000
    );
    socket.addEventListener('open', this.open);
    socket.addEventListener('message', this.message);
    socket.addEventListener('close', this.disconnected);
    socket.addEventListener('error', this.failed);
  }

  async request<T = Record<string, unknown>>(
    method: string,
    params: Record<string, unknown> = {}
  ): Promise<T> {
    if (this.closed || this.socket.readyState !== 1) {
      throw new Error('Browser is disconnected. Reconnect to continue.');
    }
    if (this.pending.size >= 128) {
      throw new Error('Browser is busy. Wait for pending commands to finish.');
    }
    const id = ++this.nextID;
    return new Promise<Record<string, unknown>>((resolve, reject) => {
      const timeout = setTimeout(() => {
        this.pending.delete(id);
        reject(new Error(`${method} timed out.`));
      }, 15000);
      this.pending.set(id, { resolve, reject, timeout });
      try {
        this.socket.send(JSON.stringify({ id, method, params }));
      } catch (error) {
        this.pending.delete(id);
        clearTimeout(timeout);
        reject(error instanceof Error ? error : new Error('Failed to send browser command.'));
      }
    }) as Promise<T>;
  }

  close(): void {
    this.finish('Browser connection closed.');
  }

  private open = (): void => {
    clearTimeout(this.connectionTimeout);
    if (!this.closed) this.handlers.onOpen();
  };

  private message = (event: MessageEvent): void => {
    if (this.closed) return;
    try {
      const message = JSON.parse(event.data);
      if (!message || typeof message !== 'object') throw new Error('Invalid browser message.');
      if (typeof message.id === 'number') {
        const pending = this.pending.get(message.id);
        if (!pending) return;
        this.pending.delete(message.id);
        clearTimeout(pending.timeout);
        if (message.error) {
          pending.reject(new Error(String(message.error.message || 'Browser command failed.')));
        } else {
          pending.resolve(message.result || {});
        }
      } else if (typeof message.method === 'string') {
        this.handlers.onEvent(message.method, message.params || {});
      }
    } catch {
      this.finish('Invalid browser message. Reconnect to continue.');
    }
  };

  private disconnected = (): void => {
    this.finish('Browser disconnected. Reconnect to reattach to the workspace session.');
  };

  private failed = (): void => {
    this.finish('Browser connection failed. Check runner access and reconnect.');
  };

  private finish(reason: string): void {
    if (this.closed) return;
    this.closed = true;
    clearTimeout(this.connectionTimeout);
    this.socket.removeEventListener('open', this.open);
    this.socket.removeEventListener('message', this.message);
    this.socket.removeEventListener('close', this.disconnected);
    this.socket.removeEventListener('error', this.failed);
    for (const pending of this.pending.values()) {
      clearTimeout(pending.timeout);
      pending.reject(new Error(reason));
    }
    this.pending.clear();
    this.socket.close();
    this.handlers.onClose(reason);
  }
}
