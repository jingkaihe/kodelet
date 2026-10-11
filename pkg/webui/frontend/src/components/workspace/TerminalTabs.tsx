import { Pencil, Plus, X } from 'lucide-react';
import {
  type KeyboardEvent,
  type ReactNode,
  useCallback,
  useEffect,
  useId,
  useRef,
  useState,
} from 'react';
import apiService from '../../services/api';
import type { TerminalReadyEvent, WorkspaceTarget } from '../../types';
import UIInputDialog from '../chat/UIInputDialog';
import TerminalModalFrame from './TerminalModalFrame';
import { terminalShellDirectory } from './terminalDirectory';
import { createTerminalPopOutId } from './terminalPopOut';

const MAX_TABS = 8;
const MAX_TITLE_LENGTH = 100;
const STORAGE_PREFIX = 'kodelet.terminal.tabs.v1:';
const SAVED_STATE_LIMIT = 50;
const SAVED_STATE_MAX_AGE = 30 * 24 * 60 * 60 * 1000;
export const TERMINAL_TAB_TITLES_CHANNEL = 'kodelet.terminal.tabs.titles';

interface TerminalTab {
  id: string;
  number: number;
  customTitle: string;
  // Only directory labels are saved; program titles may contain secrets.
  directory?: string;
  sessionId?: string;
  cwd?: string;
}

interface TabState {
  tabs: TerminalTab[];
  activeId: string;
  nextNumber: number;
}

export interface TerminalTabSessionProps {
  terminalId: string;
  connectingEnabled: boolean;
  tabBar: (disabled: boolean) => ReactNode;
  onPopOutChange: (active: boolean) => void;
  onDirectoryChange: (cwd: string, homeDir?: string) => void;
  onTitleChange: (title: string, homeDir?: string) => void;
  onSessionReady: (event: TerminalReadyEvent) => void;
}

// Match runner ownership: a draft can visit multiple directories in one conversation.
export const getTerminalTabsKey = (target: WorkspaceTarget): string =>
  STORAGE_PREFIX +
  JSON.stringify(
    target.kind === 'runner'
      ? [target.runnerId, target.conversationId || '', target.cwd || '']
      : ['local', target.cwd || '']
  );

const initialState = (): TabState => ({
  // The first tab reattaches to pre-tabs versions' default shell.
  tabs: [{ id: '', number: 1, customTitle: '' }],
  activeId: '',
  nextNumber: 2,
});

const parseState = (raw: string | null): TabState | null => {
  if (!raw) return null;
  try {
    const state = JSON.parse(raw) as TabState;
    if (
      !state ||
      !Array.isArray(state.tabs) ||
      state.tabs.length > MAX_TABS ||
      !Number.isSafeInteger(state.nextNumber) ||
      state.nextNumber < 1 ||
      typeof state.activeId !== 'string' ||
      !state.tabs.every(
        (tab) =>
          tab &&
          typeof tab.id === 'string' &&
          /^[a-zA-Z0-9_-]{0,128}$/.test(tab.id) &&
          Number.isSafeInteger(tab.number) &&
          tab.number > 0 &&
          tab.number < state.nextNumber &&
          typeof tab.customTitle === 'string' &&
          tab.customTitle.length <= MAX_TITLE_LENGTH &&
          (tab.directory === undefined ||
            (typeof tab.directory === 'string' && tab.directory.length <= MAX_TITLE_LENGTH)) &&
          (tab.sessionId === undefined ||
            (typeof tab.sessionId === 'string' && tab.sessionId.length <= 128)) &&
          (tab.cwd === undefined || (typeof tab.cwd === 'string' && tab.cwd.startsWith('/')))
      ) ||
      new Set(state.tabs.map((tab) => tab.id)).size !== state.tabs.length ||
      (state.tabs.length > 0 && !state.tabs.some((tab) => tab.id === state.activeId))
    ) {
      return null;
    }
    // Rebuild tabs to drop legacy saved titles and unknown fields.
    return {
      tabs: state.tabs.map(({ id, number, customTitle, directory, sessionId, cwd }) => ({
        id,
        number,
        customTitle,
        directory,
        sessionId,
        cwd,
      })),
      activeId: state.activeId,
      nextNumber: state.nextNumber,
    };
  } catch {
    return null;
  }
};

const readState = (key: string): TabState => {
  try {
    return parseState(window.localStorage.getItem(key)) || initialState();
  } catch {
    return initialState();
  }
};

const resolveStorageKey = (key: string): string => {
  try {
    const stored = JSON.parse(window.localStorage.getItem(key) || 'null');
    return typeof stored?.canonicalKey === 'string' &&
      stored.canonicalKey.startsWith(STORAGE_PREFIX)
      ? stored.canonicalKey
      : key;
  } catch {
    return key;
  }
};

const saveState = (key: string, state: TabState): void =>
  window.localStorage.setItem(key, JSON.stringify({ ...state, updatedAt: Date.now() }));

// Removes saved tabs unused for 30 days, keeping at most the 50 most recent.
// Also stamps and rewrites entries from earlier versions, dropping saved titles.
const pruneSavedStates = (keep: Set<string>): void => {
  try {
    const now = Date.now();
    const states: { key: string; updatedAt: number }[] = [];
    const aliases: { key: string; canonicalKey: string }[] = [];
    const keys = Array.from({ length: window.localStorage.length }, (_, index) =>
      window.localStorage.key(index)
    );
    for (const key of keys) {
      if (!key?.startsWith(STORAGE_PREFIX)) continue;
      const raw = window.localStorage.getItem(key) ?? '';
      let stored: { canonicalKey?: unknown; updatedAt?: unknown } | null = null;
      try {
        stored = JSON.parse(raw);
      } catch {
        // Removed below as an invalid state.
      }
      if (typeof stored?.canonicalKey === 'string') {
        aliases.push({ key, canonicalKey: stored.canonicalKey });
        continue;
      }
      const state = parseState(raw);
      if (!state) {
        if (!keep.has(key)) window.localStorage.removeItem(key);
        continue;
      }
      const updatedAt = stored?.updatedAt;
      if (keep.has(key) || typeof updatedAt !== 'number' || raw.includes('"title"')) {
        saveState(key, state);
        states.push({ key, updatedAt: now });
      } else {
        states.push({ key, updatedAt });
      }
    }
    const retained = new Set(
      states
        .filter(({ key, updatedAt }) => keep.has(key) || now - updatedAt <= SAVED_STATE_MAX_AGE)
        .sort((a, b) => b.updatedAt - a.updatedAt)
        .filter(({ key }, index) => keep.has(key) || index < SAVED_STATE_LIMIT)
        .map(({ key }) => key)
    );
    for (const { key } of states) {
      if (!retained.has(key)) window.localStorage.removeItem(key);
    }
    for (const { key, canonicalKey } of aliases) {
      if (!keep.has(key) && !retained.has(canonicalKey)) window.localStorage.removeItem(key);
    }
  } catch {
    // Storage may be unavailable.
  }
};

const directoryLabel = (cwd: string, homeDir?: string): string => {
  const path = cwd.replace(/[/]+$/, '') || '/';
  const home = homeDir?.replace(/[/]+$/, '');
  if (home && (path === home || path.startsWith(`${home}/`))) {
    return `~${path.slice(home.length)}`;
  }
  return path;
};

interface TerminalTabsProps {
  target: WorkspaceTarget;
  open: boolean;
  children: (session: TerminalTabSessionProps) => ReactNode;
}

const TerminalTabs = ({ target, open, children }: TerminalTabsProps) => {
  const [targetKey] = useState(() => getTerminalTabsKey(target));
  const [storageKey, setStorageKey] = useState(() => resolveStorageKey(targetKey));
  const storageKeyRef = useRef(storageKey);
  const closeTargetRef = useRef(target);
  const [state, setState] = useState(() => readState(storageKey));
  const stateRef = useRef(state);
  const [renamingId, setRenamingId] = useState<string | null>(null);
  const [name, setName] = useState('');
  const [pendingClose, setPendingClose] = useState<TerminalTab | null>(null);
  const [closing, setClosing] = useState(false);
  const [closeError, setCloseError] = useState<string | null>(null);
  const [draggedId, setDraggedId] = useState<string | null>(null);
  const [popOutActive, setPopOutActive] = useState(false);
  const [liveTitles, setLiveTitles] = useState<Record<string, string>>({});
  const liveTitlesRef = useRef(liveTitles);
  const titleChannelRef = useRef<BroadcastChannel | null>(null);
  const tabRefs = useRef(new Map<string, HTMLButtonElement>());
  const nameInputRef = useRef<HTMLInputElement>(null);
  const panelId = useId();
  const activeTab = state.tabs.find((tab) => tab.id === state.activeId);
  const tabLabel = (tab: TerminalTab): string =>
    tab.customTitle ||
    liveTitles[tab.id] ||
    tab.directory ||
    (target.cwd ? directoryLabel(target.cwd) : `Shell ${tab.number}`);

  // Persist synchronously before opening a pop-out, and do not echo storage events
  // back to their sender (which can otherwise overwrite a newer tab selection).
  const updateState = (update: (current: TabState) => TabState) => {
    const next = update(stateRef.current);
    if (next === stateRef.current) return;
    stateRef.current = next;
    setState(next);
    try {
      saveState(storageKeyRef.current, next);
    } catch {
      // The terminal remains usable when browser storage is unavailable.
    }
  };

  // Program titles stay in memory and are shared with other windows, never saved.
  const setLiveTitle = useCallback((id: string, title: string, broadcast = true) => {
    const normalized = title.trim().slice(0, MAX_TITLE_LENGTH);
    if ((liveTitlesRef.current[id] ?? '') === normalized) return;
    liveTitlesRef.current = { ...liveTitlesRef.current, [id]: normalized };
    setLiveTitles(liveTitlesRef.current);
    if (broadcast) {
      titleChannelRef.current?.postMessage({ key: storageKeyRef.current, id, title: normalized });
    }
  }, []);

  const showDirectory = (label: string) => {
    const id = activeTab?.id;
    if (id === undefined) return;
    setLiveTitle(id, '');
    const directory = label.trim().slice(0, MAX_TITLE_LENGTH);
    updateState((current) => {
      if (current.tabs.find((tab) => tab.id === id)?.directory === directory) return current;
      return {
        ...current,
        tabs: current.tabs.map((tab) => (tab.id === id ? { ...tab, directory } : tab)),
      };
    });
  };

  useEffect(() => {
    let channel: BroadcastChannel;
    try {
      channel = new BroadcastChannel(TERMINAL_TAB_TITLES_CHANNEL);
    } catch {
      return undefined;
    }
    titleChannelRef.current = channel;
    channel.onmessage = ({ data }: MessageEvent) => {
      if (
        data?.key === storageKeyRef.current &&
        typeof data.id === 'string' &&
        typeof data.title === 'string'
      ) {
        setLiveTitle(data.id, data.title, false);
      }
    };
    return () => {
      channel.close();
      titleChannelRef.current = null;
    };
  }, [setLiveTitle]);

  useEffect(() => {
    pruneSavedStates(new Set([targetKey, storageKeyRef.current]));
  }, [targetKey]);

  useEffect(() => {
    if (renamingId !== null) {
      nameInputRef.current?.focus();
      nameInputRef.current?.select();
    }
  }, [renamingId]);

  useEffect(() => {
    tabRefs.current.get(state.activeId)?.scrollIntoView?.({ block: 'nearest', inline: 'nearest' });
  }, [state.activeId]);

  useEffect(() => {
    const receive = (event: StorageEvent) => {
      if (event.key !== storageKey) return;
      const next = parseState(event.newValue);
      if (next) {
        stateRef.current = next;
        setState(next);
      }
    };
    window.addEventListener('storage', receive);
    return () => window.removeEventListener('storage', receive);
  }, [storageKey]);

  const activate = (id: string) => {
    updateState((current) => ({ ...current, activeId: id }));
  };

  const addTab = () => {
    if (popOutActive || closing || state.tabs.length >= MAX_TABS) return;
    const id = createTerminalPopOutId();
    updateState((current) => ({
      tabs: [...current.tabs, { id, number: current.nextNumber, customTitle: '' }],
      activeId: id,
      nextNumber: current.nextNumber + 1,
    }));
  };

  const rename = (tab: TerminalTab) => {
    setName(tab.customTitle || liveTitles[tab.id] || tab.directory || '');
    setRenamingId(tab.id);
  };

  const finishRename = (id: string, save: boolean, restoreFocus = true) => {
    if (save) {
      updateState((current) => ({
        ...current,
        tabs: current.tabs.map((tab) =>
          tab.id === id ? { ...tab, customTitle: name.trim().slice(0, MAX_TITLE_LENGTH) } : tab
        ),
      }));
    }
    setRenamingId(null);
    if (restoreFocus) queueMicrotask(() => tabRefs.current.get(id)?.focus());
  };

  const requestClose = (tab: TerminalTab) => {
    setCloseError(null);
    setPendingClose(tab);
  };

  const closeTab = async () => {
    if (!pendingClose || closing) return;
    const id = pendingClose.id;
    setClosing(true);
    setCloseError(null);
    try {
      const closeTarget = pendingClose.cwd
        ? { ...closeTargetRef.current, cwd: pendingClose.cwd }
        : closeTargetRef.current;
      await apiService.closeTerminal(closeTarget, id, pendingClose.sessionId);
      updateState((current) => {
        const index = current.tabs.findIndex((tab) => tab.id === id);
        const tabs = current.tabs.filter((tab) => tab.id !== id);
        return {
          ...current,
          tabs,
          activeId:
            current.activeId === id
              ? (tabs[Math.min(index, tabs.length - 1)]?.id ?? '')
              : current.activeId,
        };
      });
      setPendingClose(null);
    } catch (error) {
      setCloseError(error instanceof Error ? error.message : 'Unable to close terminal.');
    } finally {
      setClosing(false);
    }
  };

  const handleShortcut = (event: KeyboardEvent<HTMLDivElement>) => {
    if (popOutActive || closing || pendingClose || renamingId !== null) return;
    // Do not intercept desktop shortcuts or AltGr input on international layouts.
    if (
      !event.altKey ||
      event.ctrlKey ||
      event.metaKey ||
      event.shiftKey ||
      event.getModifierState('AltGraph')
    )
      return;
    // Option changes event.key on macOS (e.g. Option+1 is ¡). Match physical
    // digits too, so the displayed shortcuts work on those layouts.
    const key = /^Digit[1-8]$/.test(event.code) ? event.code.slice(5) : event.key;
    if (/^[1-8]$/.test(key) && state.tabs[Number(key) - 1]) {
      activate(state.tabs[Number(key) - 1].id);
    } else return;
    event.preventDefault();
    event.stopPropagation();
  };

  const tabBar = (disabled: boolean) => {
    const locked = disabled || closing;
    return (
      <div className="workspace-terminal-tab-bar">
        <div aria-label="Terminal tabs" className="workspace-terminal-tabs" role="tablist">
          {state.tabs.map((tab, index) => (
            <div
              className={`workspace-terminal-tab${tab.id === state.activeId ? ' is-active' : ''}`}
              key={tab.id}
              role="presentation"
            >
              {renamingId === tab.id ? (
                <input
                  aria-label="Terminal tab name"
                  className="workspace-terminal-tab-name"
                  maxLength={MAX_TITLE_LENGTH}
                  onBlur={() => finishRename(tab.id, true, false)}
                  onChange={(event) => setName(event.target.value)}
                  onKeyDown={(event) => {
                    event.stopPropagation();
                    if (event.key === 'Enter' || event.key === 'Escape') {
                      event.preventDefault();
                      finishRename(tab.id, event.key === 'Enter');
                    }
                  }}
                  ref={nameInputRef}
                  title="Enter to save, Escape to cancel. Leave blank to restore the automatic name."
                  value={name}
                />
              ) : (
                <button
                  aria-controls={panelId}
                  aria-label={tabLabel(tab)}
                  aria-keyshortcuts={`Alt+${index + 1}`}
                  aria-selected={tab.id === state.activeId}
                  className="workspace-terminal-tab-select"
                  disabled={locked}
                  draggable={!locked}
                  id={`${panelId}-tab-${tab.number}`}
                  onClick={() => activate(tab.id)}
                  onDoubleClick={() => rename(tab)}
                  onDragStart={(event) => {
                    event.dataTransfer.setData('text/plain', tab.id);
                    setDraggedId(tab.id);
                  }}
                  onDragEnd={() => setDraggedId(null)}
                  onDragOver={(event) => {
                    if (!locked) event.preventDefault();
                  }}
                  onDrop={(event) => {
                    event.preventDefault();
                    if (locked || draggedId === null || draggedId === tab.id) return;
                    updateState((current) => {
                      const dragged = current.tabs.find((item) => item.id === draggedId);
                      if (!dragged) return current;
                      const tabs = current.tabs.filter((item) => item.id !== draggedId);
                      tabs.splice(index, 0, dragged);
                      return { ...current, tabs };
                    });
                    setDraggedId(null);
                  }}
                  onKeyDown={(event) => {
                    if (event.ctrlKey || event.altKey || event.metaKey) return;
                    const next =
                      event.key === 'ArrowRight'
                        ? (index + 1) % state.tabs.length
                        : event.key === 'ArrowLeft'
                          ? (index - 1 + state.tabs.length) % state.tabs.length
                          : event.key === 'Home'
                            ? 0
                            : event.key === 'End'
                              ? state.tabs.length - 1
                              : -1;
                    if (next < 0) return;
                    event.preventDefault();
                    activate(state.tabs[next].id);
                    const id = state.tabs[next].id;
                    queueMicrotask(() => tabRefs.current.get(id)?.focus());
                  }}
                  ref={(button) => {
                    if (button) tabRefs.current.set(tab.id, button);
                    else tabRefs.current.delete(tab.id);
                  }}
                  role="tab"
                  tabIndex={tab.id === state.activeId ? 0 : -1}
                  title={`${tabLabel(tab)} — double-click to rename`}
                  type="button"
                >
                  <span>{tabLabel(tab)}</span>
                </button>
              )}
              <button
                aria-label={`Close ${tabLabel(tab)}`}
                className="workspace-terminal-tab-close"
                disabled={locked}
                onClick={() => requestClose(tab)}
                title="Close terminal tab"
                type="button"
              >
                <X aria-hidden="true" size={13} />
              </button>
            </div>
          ))}
        </div>
        <button
          aria-label="New terminal tab"
          className="workspace-terminal-icon-button"
          disabled={locked || state.tabs.length >= MAX_TABS}
          onClick={addTab}
          title={
            state.tabs.length >= MAX_TABS ? 'Terminal tab limit reached (8)' : 'New terminal tab'
          }
          type="button"
        >
          <Plus aria-hidden="true" size={16} />
        </button>
        <button
          aria-label="Rename terminal tab"
          className="workspace-terminal-icon-button"
          disabled={locked || !activeTab}
          onClick={() => {
            if (activeTab) rename(activeTab);
          }}
          title="Rename terminal tab"
          type="button"
        >
          <Pencil aria-hidden="true" size={14} />
        </button>
      </div>
    );
  };

  if (!open) return null;

  return (
    <div className="workspace-terminal-workspace" onKeyDownCapture={handleShortcut}>
      <div
        aria-labelledby={activeTab ? `${panelId}-tab-${activeTab.number}` : undefined}
        className="workspace-terminal-tab-panel"
        id={panelId}
        role="tabpanel"
      >
        {activeTab ? (
          children({
            terminalId: activeTab.id,
            connectingEnabled: !(closing && pendingClose?.id === activeTab.id),
            tabBar,
            onPopOutChange: setPopOutActive,
            onDirectoryChange: (cwd, homeDir) => showDirectory(directoryLabel(cwd, homeDir)),
            onTitleChange: (title, homeDir) => {
              // Shell prompt titles (user@host:path) report a directory.
              const directory = terminalShellDirectory(title);
              if (directory !== null) {
                showDirectory(directoryLabel(directory, homeDir));
                return;
              }
              const trimmed = title.trim();
              setLiveTitle(
                activeTab.id,
                trimmed.startsWith('/') ? directoryLabel(trimmed, homeDir) : trimmed
              );
            },
            onSessionReady: (event) => {
              closeTargetRef.current = { ...target, cwd: event.cwd };
              const canonicalKey = getTerminalTabsKey({ ...target, cwd: event.cwd });
              if (canonicalKey !== storageKeyRef.current) {
                let next = stateRef.current;
                try {
                  next = parseState(window.localStorage.getItem(canonicalKey)) || next;
                  saveState(canonicalKey, next);
                  window.localStorage.setItem(
                    getTerminalTabsKey(target),
                    JSON.stringify({ canonicalKey })
                  );
                } catch {
                  // Canonicalization must work even when persistence is unavailable.
                }
                stateRef.current = next;
                setState(next);
                storageKeyRef.current = canonicalKey;
                setStorageKey(canonicalKey);
              }
              updateState((current) => ({
                ...current,
                tabs: current.tabs.map((tab) =>
                  tab.id === activeTab.id
                    ? { ...tab, sessionId: event.sessionId, cwd: event.cwd }
                    : tab
                ),
              }));
            },
          })
        ) : (
          <TerminalModalFrame tabBar={tabBar(false)}>
            <div className="workspace-modal-placeholder">
              No terminal tabs. Open a new tab to start a shell.
            </div>
          </TerminalModalFrame>
        )}
      </div>
      {pendingClose ? (
        <UIInputDialog
          mode="confirm"
          onCancel={() => setPendingClose(null)}
          onSubmit={() => {
            void closeTab();
          }}
          request={{
            id: `close-terminal-${pendingClose.id}`,
            title: 'Close tab?',
            message:
              closeError || 'Closing this tab will stop the shell and any running processes.',
            confirmButtonText: 'Close',
          }}
          submitting={closing}
        />
      ) : null}
    </div>
  );
};

export default TerminalTabs;
