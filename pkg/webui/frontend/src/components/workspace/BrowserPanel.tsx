import { ArrowLeft, ArrowRight, RefreshCw, Square } from 'lucide-react';
import type React from 'react';
import { useCallback, useEffect, useEffectEvent, useId, useRef, useState } from 'react';
import apiService from '../../services/api';
import { BrowserCDP } from '../../services/browser';
import type { BrowserSession, WorkspaceTarget } from '../../types';
import { cn } from '../../utils';

type RemoteObject = {
  value?: unknown;
  description?: string;
  type?: string;
  unserializableValue?: string;
};
type Evaluation = {
  result: RemoteObject;
  exceptionDetails?: { text?: string; exception?: RemoteObject };
};
type Frame = { key: number; sessionId: number; data: string; width: number; height: number };
type ConsoleEntry = { id: number; text: string; error: boolean };
type NetworkEntry = { id: string; method: string; url: string; status: string };
type PageDialog = { type: string; message: string; promptText: string; responding: boolean };
type DebugView = 'console' | 'network' | 'inspect' | 'devtools' | null;
type Status = 'connecting' | 'live' | 'disconnected' | 'stopped';

const MAX_ENTRIES = 200;
const displayObject = (value?: RemoteObject): string => {
  if (!value) return '';
  const text =
    value.value === undefined
      ? value.unserializableValue || value.description || value.type || 'undefined'
      : typeof value.value === 'string'
        ? value.value
        : JSON.stringify(value.value);
  return String(text).slice(0, 8000);
};
const errorText = (error: unknown): string =>
  error instanceof Error ? error.message : String(error);
const modifiers = (event: {
  altKey: boolean;
  ctrlKey: boolean;
  metaKey: boolean;
  shiftKey: boolean;
}): number =>
  (event.altKey ? 1 : 0) |
  (event.ctrlKey ? 2 : 0) |
  (event.metaKey ? 4 : 0) |
  (event.shiftKey ? 8 : 0);

const BrowserPanel: React.FC<{ target: WorkspaceTarget }> = ({ target }) => {
  const helpID = useId();
  const [session, setSession] = useState<BrowserSession | null>(null);
  const [status, setStatus] = useState<Status>('connecting');
  const [error, setError] = useState<string | null>(null);
  const [retry, setRetry] = useState(0);
  const [url, setURL] = useState('http://localhost:1234');
  const [frame, setFrame] = useState<Frame | null>(null);
  const [debugView, setDebugView] = useState<DebugView>(null);
  const [consoleEntries, setConsoleEntries] = useState<ConsoleEntry[]>([]);
  const [networkEntries, setNetworkEntries] = useState<NetworkEntry[]>([]);
  const [expression, setExpression] = useState('');
  const [inspecting, setInspecting] = useState(false);
  const [inspection, setInspection] = useState('Choose “Pick element”, then click the page.');
  const [stopping, setStopping] = useState(false);
  const [dialog, setDialog] = useState<PageDialog | null>(null);
  const clientRef = useRef<BrowserCDP | null>(null);
  const viewportRef = useRef<HTMLDivElement>(null);
  const resizeViewportRef = useRef<(() => void) | null>(null);
  const imageRef = useRef<HTMLImageElement>(null);
  const inputRef = useRef<HTMLTextAreaElement>(null);
  const addressRef = useRef<HTMLInputElement>(null);
  const activeFrame = useRef<Frame | null>(null);
  const nextFrame = useRef<Frame | null>(null);
  const frameSequence = useRef(0);
  const entrySequence = useRef(0);
  const composing = useRef(false);
  const stopped = useRef(false);
  const heldKeys = useRef(new Map<string, Record<string, unknown>>());
  const mouseMove = useRef<Record<string, unknown> | null>(null);
  const mouseMoveFrame = useRef<number | null>(null);
  const lastClick = useRef({ time: 0, x: 0, y: 0, count: 0, button: -1 });

  const appendConsole = useCallback((text: string, isError = false) => {
    const entry = { id: ++entrySequence.current, text: text.slice(0, 16000), error: isError };
    setConsoleEntries((entries) => [...entries, entry].slice(-MAX_ENTRIES));
  }, []);

  const command = useCallback(
    async <T,>(method: string, params: Record<string, unknown> = {}): Promise<T | undefined> => {
      const client = clientRef.current;
      if (!client) return undefined;
      try {
        const result = await client.request<T>(method, params);
        return clientRef.current === client ? result : undefined;
      } catch (reason) {
        if (clientRef.current === client && !stopped.current) setError(errorText(reason));
        return undefined;
      }
    },
    []
  );

  // biome-ignore lint/correctness/useExhaustiveDependencies(retry): An explicit reconnect opens a fresh authenticated handle to the retained runner session.
  useEffect(() => {
    const abort = new AbortController();
    let disposed = false;
    let client: BrowserCDP | null = null;
    let mainFrameID = '';
    stopped.current = false;
    setStatus('connecting');
    setError(null);
    setSession(null);
    setFrame(null);
    setInspecting(false);
    setStopping(false);
    setDialog(null);
    setConsoleEntries([]);
    setNetworkEntries([]);
    activeFrame.current = null;
    nextFrame.current = null;
    const acknowledge = (item: Frame) => {
      void client
        ?.request('Page.screencastFrameAck', { sessionId: item.sessionId })
        .catch(() => {});
    };
    const receiveEvent = (method: string, params: Record<string, unknown>) => {
      if (disposed || stopped.current) return;
      switch (method) {
        case 'Page.javascriptDialogOpening':
          // Never answer automatically: the agent or another viewer may own this dialog.
          setDialog({
            type: String(params.type || 'alert'),
            message: String(params.message || ''),
            promptText: String(params.defaultPrompt || ''),
            responding: false,
          });
          break;
        case 'Page.javascriptDialogClosed':
          setDialog(null);
          break;
        case 'Page.screencastFrame': {
          const metadata = params.metadata as
            | { deviceWidth?: number; deviceHeight?: number }
            | undefined;
          if (typeof params.sessionId !== 'number' || typeof params.data !== 'string') return;
          const item: Frame = {
            key: ++frameSequence.current,
            sessionId: params.sessionId,
            data: params.data,
            width: Math.max(1, metadata?.deviceWidth || 1),
            height: Math.max(1, metadata?.deviceHeight || 1),
          };
          // Decode one image at a time and retain only the newest waiting image.
          if (activeFrame.current) {
            if (nextFrame.current) acknowledge(nextFrame.current);
            nextFrame.current = item;
          } else {
            activeFrame.current = item;
            setFrame(item);
          }
          break;
        }
        case 'Page.frameNavigated': {
          const page = params.frame as { id?: string; parentId?: string; url?: string } | undefined;
          if (page?.url && !page.parentId) {
            mainFrameID = page.id || mainFrameID;
            setURL(page.url);
            resizeViewportRef.current?.();
          }
          break;
        }
        case 'Page.loadEventFired':
          // Navigation can replace Chrome's capture surface while retaining its
          // emulated layout size. Reapply metrics to synchronize the screencast.
          resizeViewportRef.current?.();
          break;
        case 'Page.navigatedWithinDocument':
          if (params.frameId === mainFrameID && typeof params.url === 'string') setURL(params.url);
          break;
        case 'Runtime.consoleAPICalled':
          appendConsole(
            ((params.args as RemoteObject[]) || []).map(displayObject).join(' '),
            params.type === 'error'
          );
          break;
        case 'Runtime.exceptionThrown': {
          const details = params.exceptionDetails as Evaluation['exceptionDetails'];
          appendConsole(
            displayObject(details?.exception) || details?.text || 'JavaScript exception',
            true
          );
          break;
        }
        case 'Network.requestWillBeSent': {
          const request = params.request as { url?: string; method?: string } | undefined;
          const entry = {
            id: String(params.requestId),
            method: request?.method || '',
            url: (request?.url || '').slice(0, 8000),
            status: 'Pending',
          };
          setNetworkEntries((entries) =>
            [...entries.filter((item) => item.id !== entry.id), entry].slice(-MAX_ENTRIES)
          );
          break;
        }
        case 'Network.responseReceived':
        case 'Network.loadingFailed': {
          const response = params.response as { status?: number } | undefined;
          const requestStatus =
            method === 'Network.loadingFailed'
              ? String(params.errorText || 'Failed')
              : String(response?.status ?? 'Received');
          setNetworkEntries((entries) =>
            entries.map((item) =>
              item.id === params.requestId
                ? { ...item, status: requestStatus.slice(0, 1000) }
                : item
            )
          );
          break;
        }
      }
    };

    void apiService
      .openBrowserSession(target, abort.signal)
      .then((opened) => {
        if (disposed) return;
        setSession(opened);
        client = new BrowserCDP(apiService.createBrowserWebSocket(opened.id), {
          onOpen: () => {
            void (async () => {
              if (!client) return;
              await client.request('Page.enable');
              await client.request('Runtime.enable');
              await client.request('Network.enable', {
                maxTotalBufferSize: 5000000,
                maxResourceBufferSize: 1000000,
              });
              const tree = await client.request<{
                frameTree: { frame: { id: string; url: string } };
              }>('Page.getFrameTree');
              if (!disposed && tree.frameTree?.frame?.url) {
                mainFrameID = tree.frameTree.frame.id;
                setURL(tree.frameTree.frame.url);
              }
              await client.request('Page.startScreencast', {
                format: 'jpeg',
                quality: 80,
                maxWidth: 1920,
                maxHeight: 1440,
              });
              if (!disposed && !stopped.current) setStatus('live');
            })().catch((reason) => {
              if (!disposed && !stopped.current) {
                client?.close();
                setError(errorText(reason));
              }
            });
          },
          onEvent: receiveEvent,
          onClose: (reason) => {
            if (disposed || stopped.current) return;
            setStatus('disconnected');
            setError(reason);
            setFrame(null);
            setInspecting(false);
            setDialog(null);
            activeFrame.current = null;
            nextFrame.current = null;
            heldKeys.current.clear();
          },
        });
        clientRef.current = client;
      })
      .catch((reason) => {
        if (!disposed) {
          setStatus('disconnected');
          setError(errorText(reason));
        }
      });

    return () => {
      disposed = true;
      abort.abort();
      clientRef.current = null;
      client?.close();
      activeFrame.current = null;
      nextFrame.current = null;
      heldKeys.current.clear();
      if (mouseMoveFrame.current !== null) cancelAnimationFrame(mouseMoveFrame.current);
      mouseMoveFrame.current = null;
      mouseMove.current = null;
    };
  }, [appendConsole, target, retry]);

  useEffect(() => {
    const viewport = viewportRef.current;
    if (!viewport || status !== 'live') return undefined;
    let timer: ReturnType<typeof setTimeout>;
    const resize = () => {
      clearTimeout(timer);
      timer = setTimeout(() => {
        const { clientWidth: width, clientHeight: height } = viewport;
        if (width < 1 || height < 1) return;
        void command('Emulation.setDeviceMetricsOverride', {
          // Match the panel's content box; only the encoded screencast is size-limited.
          width,
          height,
          deviceScaleFactor: 1,
          mobile: false,
        });
      }, 80);
    };
    const observer = new ResizeObserver(resize);
    observer.observe(viewport);
    resizeViewportRef.current = resize;
    resize();
    return () => {
      resizeViewportRef.current = null;
      observer.disconnect();
      clearTimeout(timer);
    };
  }, [command, status]);

  const finishFrame = (item: Frame, failed = false) => {
    if (activeFrame.current?.key !== item.key) return;
    void command('Page.screencastFrameAck', { sessionId: item.sessionId });
    activeFrame.current = nextFrame.current;
    nextFrame.current = null;
    if (activeFrame.current) setFrame(activeFrame.current);
    if (failed)
      setError('Could not decode a browser frame. Reconnect if the view does not recover.');
  };

  const navigate = async (event: React.FormEvent) => {
    event.preventDefault();
    const address = url.trim();
    if (!address) return;
    const destination = /^(https?:\/\/|about:blank$)/i.test(address)
      ? address
      : `http://${address}`;
    try {
      if (/^(javascript|data|file|ftp|about):/i.test(address) && address !== 'about:blank')
        throw new Error('Use an HTTP or HTTPS address.');
      const parsed = new URL(destination);
      if (!['http:', 'https:'].includes(parsed.protocol) && destination !== 'about:blank')
        throw new Error('Use an HTTP or HTTPS address.');
      setError(null);
      const result = await command<{ errorText?: string }>('Page.navigate', { url: destination });
      if (result?.errorText) setError(result.errorText);
    } catch (reason) {
      setError(errorText(reason));
    }
  };

  const history = async (offset: number) => {
    const result = await command<{ currentIndex: number; entries: { id: number }[] }>(
      'Page.getNavigationHistory'
    );
    const entry = result?.entries[result.currentIndex + offset];
    if (entry) await command('Page.navigateToHistoryEntry', { entryId: entry.id });
  };

  const point = (event: { clientX: number; clientY: number }, captured = false) => {
    const image = imageRef.current;
    if (!image?.complete || !image.naturalWidth || !image.naturalHeight || !frame) return null;
    const bounds = image.getBoundingClientRect();
    if (bounds.width < 1 || bounds.height < 1) return null;
    // object-fit: contain can letterbox a previous frame during a resize. Map only
    // the displayed pixels, not the entire image element, back into page coordinates.
    const scale = Math.min(bounds.width / image.naturalWidth, bounds.height / image.naturalHeight);
    const width = image.naturalWidth * scale;
    const height = image.naturalHeight * scale;
    const x = event.clientX - bounds.left - (bounds.width - width) / 2;
    const y = event.clientY - bounds.top - (bounds.height - height) / 2;
    if (!captured && (x < 0 || y < 0 || x >= width || y >= height)) return null;
    return {
      x: Math.max(0, Math.min(frame.width - 1, (x * frame.width) / width)),
      y: Math.max(0, Math.min(frame.height - 1, (y * frame.height) / height)),
    };
  };

  const pointer = (event: React.PointerEvent<HTMLTextAreaElement>, type: string) => {
    if (status !== 'live') return;
    const coordinates = point(event, event.currentTarget.hasPointerCapture?.(event.pointerId));
    if (!coordinates) return;
    if (type === 'mousePressed') {
      inputRef.current?.focus();
      event.currentTarget.setPointerCapture?.(event.pointerId);
      const previous = lastClick.current;
      const consecutive =
        previous.button === event.button &&
        event.timeStamp - previous.time < 500 &&
        Math.hypot(coordinates.x - previous.x, coordinates.y - previous.y) < 5;
      lastClick.current = {
        ...coordinates,
        time: event.timeStamp,
        button: event.button,
        count: consecutive ? (previous.count % 3) + 1 : 1,
      };
    }
    if (inspecting) {
      if (type === 'mouseReleased') {
        setInspecting(false);
        void command<Evaluation>('Runtime.evaluate', {
          expression: `(() => { const el = document.elementFromPoint(${coordinates.x}, ${coordinates.y}); if (!el) return 'No element'; const s = getComputedStyle(el); return { html: el.outerHTML.slice(0, 12000), styles: { display: s.display, color: s.color, background: s.backgroundColor, font: s.font, margin: s.margin, padding: s.padding, width: s.width, height: s.height } }; })()`,
          returnByValue: true,
          timeout: 10000,
        }).then((result) => {
          if (result)
            setInspection(displayObject(result.exceptionDetails?.exception || result.result));
        });
      }
      return;
    }
    const params = {
      type,
      ...coordinates,
      modifiers: modifiers(event),
      buttons: event.buttons,
      button: ['left', 'middle', 'right', 'back', 'forward'][event.button] || 'none',
      clickCount: type === 'mouseMoved' ? 0 : lastClick.current.count || 1,
    };
    if (type === 'mouseMoved') {
      mouseMove.current = params;
      if (mouseMoveFrame.current === null)
        mouseMoveFrame.current = requestAnimationFrame(() => {
          mouseMoveFrame.current = null;
          if (mouseMove.current) void command('Input.dispatchMouseEvent', mouseMove.current);
          mouseMove.current = null;
        });
    } else {
      if (mouseMoveFrame.current !== null) cancelAnimationFrame(mouseMoveFrame.current);
      mouseMoveFrame.current = null;
      mouseMove.current = null;
      void command('Input.dispatchMouseEvent', params);
    }
  };

  const wheel = useEffectEvent((event: WheelEvent) => {
    const coordinates = point(event);
    if (status !== 'live' || !coordinates) return;
    event.preventDefault();
    const scale = event.deltaMode === 1 ? 16 : event.deltaMode === 2 ? frame?.height || 600 : 1;
    void command('Input.dispatchMouseEvent', {
      type: 'mouseWheel',
      ...coordinates,
      deltaX: event.deltaX * scale,
      deltaY: event.deltaY * scale,
      modifiers: modifiers(event),
    });
  });

  useEffect(() => {
    const input = inputRef.current;
    if (!input) return undefined;
    // React's delegated wheel listeners are passive; prevent scrolling the portal itself.
    const listener = (event: WheelEvent) => wheel(event);
    input.addEventListener('wheel', listener, { passive: false });
    return () => input.removeEventListener('wheel', listener);
  }, []);

  const key = (event: React.KeyboardEvent<HTMLTextAreaElement>, up: boolean) => {
    if (
      status !== 'live' ||
      composing.current ||
      event.nativeEvent.isComposing ||
      event.key === 'Process' ||
      event.key === 'Dead'
    )
      return;
    if (event.key === 'F6') {
      event.preventDefault();
      if (!up) addressRef.current?.focus();
      return;
    }
    // Let the local browser provide clipboard text to onPaste, never grant clipboard access to the remote page.
    if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === 'v') return;
    event.preventDefault();
    const text =
      !up && !event.ctrlKey && !event.metaKey && !event.altKey
        ? event.key.length === 1
          ? event.key
          : event.key === 'Enter'
            ? '\r'
            : undefined
        : undefined;
    const params = {
      key: event.key,
      code: event.code,
      modifiers: modifiers(event),
      windowsVirtualKeyCode: event.keyCode,
      ...(text ? { text, unmodifiedText: text } : {}),
    };
    if (up) heldKeys.current.delete(event.code || event.key);
    else heldKeys.current.set(event.code || event.key, params);
    void command('Input.dispatchKeyEvent', {
      ...params,
      type: up ? 'keyUp' : text ? 'keyDown' : 'rawKeyDown',
      autoRepeat: event.repeat,
    });
  };

  const evaluate = async (event: React.FormEvent) => {
    event.preventDefault();
    if (!expression.trim()) return;
    const source = expression;
    setExpression('');
    appendConsole(`> ${source}`);
    const result = await command<Evaluation>('Runtime.evaluate', {
      expression: source,
      awaitPromise: true,
      generatePreview: true,
      objectGroup: 'kodelet-console',
      timeout: 10000,
    });
    if (result) {
      appendConsole(
        displayObject(result.exceptionDetails?.exception || result.result) ||
          result.exceptionDetails?.text ||
          '',
        Boolean(result.exceptionDetails)
      );
      void command('Runtime.releaseObjectGroup', { objectGroup: 'kodelet-console' });
    }
  };

  const respondToDialog = async (accept: boolean) => {
    if (!dialog || dialog.responding) return;
    const pending = { ...dialog, responding: true };
    setDialog(pending);
    setError(null);
    const result = await command('Page.handleJavaScriptDialog', {
      accept,
      ...(accept && dialog.type === 'prompt' ? { promptText: dialog.promptText } : {}),
    });
    // A shared client can close this dialog and open another before our reply arrives.
    setDialog((current) => (current === pending ? (result ? null : dialog) : current));
  };

  const stop = async () => {
    if (!session) return;
    const client = clientRef.current;
    setStopping(true);
    try {
      await apiService.stopBrowserSession(session.id);
      if (clientRef.current !== client) return;
      stopped.current = true;
      client?.close();
      clientRef.current = null;
      setStatus('stopped');
      setError(null);
      setFrame(null);
      setSession(null);
      setDialog(null);
    } catch (reason) {
      if (clientRef.current === client) setError(errorText(reason));
    } finally {
      if (clientRef.current === client || stopped.current) setStopping(false);
    }
  };

  const live = status === 'live' && !stopping;
  return (
    <section
      aria-label="Browser"
      className="workspace-side-panel workspace-browser-panel surface-panel"
      data-testid="browser-panel"
    >
      <form
        aria-label="Browser navigation"
        className="workspace-browser-navigation"
        onSubmit={navigate}
      >
        <button
          aria-label="Back"
          className="workspace-terminal-icon-button"
          disabled={!live}
          onClick={() => void history(-1)}
          type="button"
        >
          <ArrowLeft aria-hidden="true" size={16} />
        </button>
        <button
          aria-label="Forward"
          className="workspace-terminal-icon-button"
          disabled={!live}
          onClick={() => void history(1)}
          type="button"
        >
          <ArrowRight aria-hidden="true" size={16} />
        </button>
        <button
          aria-label="Reload page"
          className="workspace-terminal-icon-button"
          disabled={!live}
          onClick={() => void command('Page.reload')}
          type="button"
        >
          <RefreshCw aria-hidden="true" size={16} />
        </button>
        <input
          aria-label="Browser address"
          autoCapitalize="off"
          autoComplete="off"
          className="input input-bordered input-sm min-w-0 flex-1 font-mono text-xs"
          onChange={(event) => setURL(event.target.value)}
          ref={addressRef}
          spellCheck={false}
          value={url}
        />
        <button className="btn btn-ghost btn-sm" disabled={!live} type="submit">
          Go
        </button>
        <button
          aria-label="Stop workspace browser"
          className="workspace-terminal-icon-button"
          disabled={!session || stopping}
          onClick={() => void stop()}
          title="Stop the shared workspace browser, including agent access"
          type="button"
        >
          <Square aria-hidden="true" size={14} />
        </button>
      </form>
      <div className="workspace-browser-status">
        <output>
          {status === 'live'
            ? 'Live · Runner browser'
            : status === 'connecting'
              ? 'Connecting to runner browser…'
              : status === 'stopped'
                ? 'Browser stopped'
                : 'Disconnected'}
        </output>
        {status === 'disconnected' || status === 'stopped' ? (
          <button
            className="btn btn-ghost btn-xs"
            disabled={stopping}
            onClick={() => setRetry((value) => value + 1)}
            type="button"
          >
            {status === 'stopped' ? 'Start browser' : 'Reconnect'}
          </button>
        ) : null}
      </div>
      {error ? (
        <div className="px-3 py-2 text-xs text-error" role="alert">
          {error}
        </div>
      ) : null}
      {dialog ? (
        <div
          aria-label="Page JavaScript dialog"
          aria-describedby={`${helpID}-dialog-message`}
          aria-live="polite"
          className="max-h-64 shrink-0 overflow-auto border-y border-base-content/10 p-3 text-xs"
          role="dialog"
        >
          <p className="font-semibold">Page {dialog.type} dialog</p>
          <p className="my-2 whitespace-pre-wrap break-words" id={`${helpID}-dialog-message`}>
            {dialog.message}
          </p>
          <p className="mb-2 text-base-content/60">
            Shared with the agent and other viewers. Respond only if you intend to handle it.
          </p>
          {dialog.type === 'prompt' ? (
            <input
              aria-label="Dialog prompt text"
              className="input input-bordered input-sm mb-2 w-full"
              disabled={dialog.responding || stopping}
              onChange={(event) => setDialog({ ...dialog, promptText: event.target.value })}
              value={dialog.promptText}
            />
          ) : null}
          <div className="flex gap-2">
            <button
              className="btn btn-ghost btn-sm"
              disabled={dialog.responding || stopping}
              onClick={() => void respondToDialog(true)}
              type="button"
            >
              Accept
            </button>
            <button
              className="btn btn-ghost btn-sm"
              disabled={dialog.responding || stopping}
              onClick={() => void respondToDialog(false)}
              type="button"
            >
              Dismiss
            </button>
          </div>
        </div>
      ) : null}
      <div
        className={cn('workspace-browser-viewport', inspecting && 'is-inspecting')}
        ref={viewportRef}
      >
        {frame ? (
          <img
            alt="Runner browser page"
            className="workspace-browser-image"
            draggable={false}
            key={frame.key}
            onError={() => finishFrame(frame, true)}
            onLoad={() => finishFrame(frame)}
            ref={imageRef}
            src={`data:image/jpeg;base64,${frame.data}`}
          />
        ) : (
          <div className="workspace-browser-placeholder">
            {live ? 'Waiting for the page…' : 'The browser runs on your workspace runner.'}
          </div>
        )}
        <textarea
          aria-label="Remote browser input"
          aria-describedby={helpID}
          autoCapitalize="off"
          autoComplete="off"
          className="workspace-browser-input"
          disabled={!live}
          ref={inputRef}
          spellCheck={false}
          onBlur={() => {
            for (const params of heldKeys.current.values())
              void command('Input.dispatchKeyEvent', {
                ...params,
                type: 'keyUp',
                text: undefined,
                modifiers: 0,
              });
            heldKeys.current.clear();
          }}
          onCompositionStart={() => {
            composing.current = true;
          }}
          onCompositionEnd={(event) => {
            composing.current = false;
            if (event.data) void command('Input.insertText', { text: event.data });
            event.currentTarget.value = '';
          }}
          onContextMenu={(event) => event.preventDefault()}
          onInput={(event) => {
            if (composing.current) return;
            const text = event.currentTarget.value;
            event.currentTarget.value = '';
            const inputType = (event.nativeEvent as InputEvent).inputType;
            if (
              text &&
              inputType !== 'insertCompositionText' &&
              inputType !== 'insertFromComposition'
            )
              void command('Input.insertText', { text });
          }}
          onKeyDown={(event) => key(event, false)}
          onKeyUp={(event) => key(event, true)}
          onPaste={(event) => {
            event.preventDefault();
            void command('Input.insertText', { text: event.clipboardData.getData('text/plain') });
          }}
          onPointerDown={(event) => pointer(event, 'mousePressed')}
          onPointerMove={(event) => pointer(event, 'mouseMoved')}
          onPointerUp={(event) => pointer(event, 'mouseReleased')}
          onPointerCancel={(event) => pointer(event, 'mouseReleased')}
        />
      </div>
      <p className="px-3 py-1 text-[11px] text-base-content/60" id={helpID}>
        Click to interact. F6 returns to the address bar. Closing this panel keeps the browser
        running.
      </p>
      <div aria-label="Browser debugging" className="workspace-browser-debug-tabs" role="tablist">
        {(['console', 'network', 'inspect', 'devtools'] as const).map((view) => (
          <button
            aria-selected={debugView === view}
            className={cn('btn btn-ghost btn-xs', debugView === view && 'btn-active')}
            key={view}
            onClick={() => setDebugView(debugView === view ? null : view)}
            role="tab"
            type="button"
          >
            {view === 'devtools'
              ? 'DevTools'
              : view === 'console'
                ? 'Console'
                : view === 'network'
                  ? 'Network'
                  : 'Inspect'}
          </button>
        ))}
      </div>
      {debugView ? (
        <div
          className="workspace-browser-debug"
          role="tabpanel"
          aria-label={debugView === 'devtools' ? 'DevTools' : debugView}
        >
          {debugView === 'console' ? (
            <>
              <div
                className="workspace-browser-debug-output"
                role="log"
                aria-label="Browser console"
              >
                {consoleEntries.map((entry) => (
                  <pre
                    className={cn(
                      'whitespace-pre-wrap break-words text-xs',
                      entry.error && 'text-error'
                    )}
                    key={entry.id}
                  >
                    {entry.text}
                  </pre>
                ))}
              </div>
              <form
                aria-label="Evaluate JavaScript"
                className="flex gap-1 border-t border-base-content/10 p-2"
                onSubmit={evaluate}
              >
                <input
                  aria-label="Console expression"
                  className="input input-bordered input-sm min-w-0 flex-1 font-mono text-xs"
                  disabled={!live}
                  onChange={(event) => setExpression(event.target.value)}
                  placeholder="Evaluate in the runner page"
                  spellCheck={false}
                  value={expression}
                />
                <button className="btn btn-ghost btn-sm" disabled={!live} type="submit">
                  Run
                </button>
                <button
                  aria-label="Clear console"
                  className="btn btn-ghost btn-sm"
                  onClick={() => setConsoleEntries([])}
                  type="button"
                >
                  Clear
                </button>
              </form>
            </>
          ) : null}
          {debugView === 'network' ? (
            <div className="workspace-browser-debug-output">
              <p className="mb-2 text-xs text-base-content/60">
                Latest {MAX_ENTRIES} requests observed while attached.
              </p>
              <table className="w-full table-fixed text-left text-xs">
                <thead>
                  <tr>
                    <th className="w-16">Method</th>
                    <th className="w-24">Status</th>
                    <th>URL</th>
                  </tr>
                </thead>
                <tbody>
                  {networkEntries.map((entry) => (
                    <tr key={entry.id}>
                      <td className="align-top">{entry.method}</td>
                      <td className="break-words align-top">{entry.status}</td>
                      <td className="break-all">{entry.url}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          ) : null}
          {debugView === 'inspect' ? (
            <div className="workspace-browser-debug-output">
              <button
                aria-pressed={inspecting}
                className="btn btn-ghost btn-sm mb-2"
                disabled={!live}
                onClick={() => setInspecting(!inspecting)}
                type="button"
              >
                {inspecting ? 'Cancel picking' : 'Pick element'}
              </button>
              <p className="mb-2 text-xs text-base-content/60">
                Read-only element and computed-style snapshot. Use DevTools for full DOM editing.
              </p>
              <pre className="whitespace-pre-wrap break-words text-xs">{inspection}</pre>
            </div>
          ) : null}
          {debugView === 'devtools' ? (
            session?.devTools && live ? (
              <iframe
                className="h-full w-full border-0"
                src={apiService.browserDevToolsURL(session.id)}
                title="Runner Chrome DevTools"
              />
            ) : (
              <p className="p-3 text-xs text-base-content/70">
                {session?.devTools
                  ? 'Reconnect to use DevTools.'
                  : 'Full Chrome DevTools requires compiled DevTools assets configured on the runner. Console, Network, and read-only Inspect are available without them.'}
              </p>
            )
          ) : null}
        </div>
      ) : null}
    </section>
  );
};

export default BrowserPanel;
