import { Pencil, Plus, X } from 'lucide-react';
import { type KeyboardEvent, type ReactNode, useEffect, useId, useRef, useState } from 'react';
import apiService from '../../services/api';
import type { TerminalReadyEvent, WorkspaceTarget } from '../../types';
import UIInputDialog from '../chat/UIInputDialog';
import TerminalModalFrame from './TerminalModalFrame';
import { createTerminalPopOutId } from './terminalPopOut';

const MAX_TABS = 8;
const MAX_TITLE_LENGTH = 100;
const STORAGE_PREFIX = 'kodelet.terminal.tabs.v1:';

interface TerminalTab {
  id: string;
  number: number;
  title: string;
  customTitle: string;
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
  onDirectoryChange: (cwd: string) => void;
  onTitleChange: (title: string) => void;
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
  tabs: [{ id: '', number: 1, title: '', customTitle: '' }],
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
          typeof tab.title === 'string' &&
          tab.title.length <= MAX_TITLE_LENGTH &&
          typeof tab.customTitle === 'string' &&
          tab.customTitle.length <= MAX_TITLE_LENGTH &&
          (tab.sessionId === undefined ||
            (typeof tab.sessionId === 'string' && tab.sessionId.length <= 128)) &&
          (tab.cwd === undefined || (typeof tab.cwd === 'string' && tab.cwd.startsWith('/')))
      ) ||
      new Set(state.tabs.map((tab) => tab.id)).size !== state.tabs.length ||
      (state.tabs.length > 0 && !state.tabs.some((tab) => tab.id === state.activeId))
    ) {
      return null;
    }
    return state;
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

const directoryName = (cwd: string): string => cwd.replace(/[/]+$/, '').split('/').pop() || '/';

interface TerminalTabsProps {
  target: WorkspaceTarget;
  open: boolean;
  children: (session: TerminalTabSessionProps) => ReactNode;
}

const TerminalTabs = ({ target, open, children }: TerminalTabsProps) => {
  const [storageKey, setStorageKey] = useState(() => resolveStorageKey(getTerminalTabsKey(target)));
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
  const disabledRef = useRef(false);
  const tabRefs = useRef(new Map<string, HTMLButtonElement>());
  const nameInputRef = useRef<HTMLInputElement>(null);
  const panelId = useId();
  const activeTab = state.tabs.find((tab) => tab.id === state.activeId);
  const tabLabel = (tab: TerminalTab): string =>
    tab.customTitle ||
    tab.title ||
    (target.cwd ? directoryName(target.cwd) : `Shell ${tab.number}`);

  // Persist synchronously before opening a pop-out, and do not echo storage events
  // back to their sender (which can otherwise overwrite a newer tab selection).
  const updateState = (update: (current: TabState) => TabState) => {
    const next = update(stateRef.current);
    if (next === stateRef.current) return;
    stateRef.current = next;
    setState(next);
    try {
      window.localStorage.setItem(storageKeyRef.current, JSON.stringify(next));
    } catch {
      // The terminal remains usable when browser storage is unavailable.
    }
  };

  const updateAutomaticTitle = (title: string) => {
    const normalized = title.trim().slice(0, MAX_TITLE_LENGTH);
    updateState((current) => {
      if (current.tabs.find((tab) => tab.id === activeTab?.id)?.title === normalized)
        return current;
      return {
        ...current,
        tabs: current.tabs.map((tab) =>
          tab.id === activeTab?.id ? { ...tab, title: normalized } : tab
        ),
      };
    });
  };

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
    if (disabledRef.current || closing || state.tabs.length >= MAX_TABS) return;
    const id = createTerminalPopOutId();
    updateState((current) => ({
      tabs: [...current.tabs, { id, number: current.nextNumber, title: '', customTitle: '' }],
      activeId: id,
      nextNumber: current.nextNumber + 1,
    }));
  };

  const rename = (tab: TerminalTab) => {
    setName(tab.customTitle || tab.title);
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
      if (pendingClose.sessionId) {
        await apiService.closeTerminal(closeTarget, id, pendingClose.sessionId);
      } else {
        await apiService.closeTerminal(closeTarget, id);
      }
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
    if (disabledRef.current || closing || pendingClose || renamingId !== null) return;
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
    disabledRef.current = disabled;
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
                    if (event.key === 'F2') {
                      event.preventDefault();
                      rename(tab);
                      return;
                    }
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
                  title={`${tabLabel(tab)} — double-click or press F2 to rename`}
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
            onDirectoryChange: (cwd) => updateAutomaticTitle(directoryName(cwd)),
            onTitleChange: (title) => {
              // Shell prompt titles often use user@host:path. Keep the path,
              // including ~/..., without altering other program-set titles.
              updateAutomaticTitle(
                title.trim().replace(/^[^@\s/:]+@[^@\s/:]+:(?=~(?:\/|$)|\/)/u, '')
              );
            },
            onSessionReady: (event) => {
              closeTargetRef.current = { ...target, cwd: event.cwd };
              const canonicalKey = getTerminalTabsKey({ ...target, cwd: event.cwd });
              if (canonicalKey !== storageKeyRef.current) {
                let next = stateRef.current;
                try {
                  next = parseState(window.localStorage.getItem(canonicalKey)) || next;
                  window.localStorage.setItem(canonicalKey, JSON.stringify(next));
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
