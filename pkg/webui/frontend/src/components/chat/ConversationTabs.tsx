import {
  ChevronDown,
  ChevronsUpDown,
  Copy,
  FolderOpen,
  MessageSquarePlus,
  Plus,
  SlidersHorizontal,
  X,
} from 'lucide-react';
import {
  type KeyboardEvent,
  type ReactNode,
  useEffect,
  useEffectEvent,
  useId,
  useLayoutEffect,
  useRef,
  useState,
} from 'react';
import { cn, copyToClipboard } from '../../utils';
import Spinner from '../Spinner';

export type ConversationTabStatus = 'idle' | 'running' | 'unread' | 'attention' | 'error';

export interface ConversationTab {
  /** Conversation ID, or a client-side ID for an unsaved new chat. */
  id: string;
  title: string;
  cwd?: string;
  status?: ConversationTabStatus;
  /** An unsaved new chat; at most one is open at a time. */
  draft?: boolean;
}

const STATUS_LABELS: Record<ConversationTabStatus, string> = {
  idle: '',
  running: 'Running',
  unread: 'New activity',
  attention: 'Needs input',
  error: 'Failed',
};

const MENU_EDGE_GAP = 8;

/** Returns the tab `delta` steps from the active tab, wrapping at either end. */
export const adjacentConversationTabId = (
  tabs: ConversationTab[],
  activeId: string,
  delta: number
): string | undefined => {
  if (tabs.length === 0) return undefined;
  const index = Math.max(
    0,
    tabs.findIndex((tab) => tab.id === activeId)
  );
  return tabs[(index + delta + tabs.length) % tabs.length]?.id;
};

const basename = (cwd?: string): string => {
  const path = cwd?.replace(/\/+$/, '') || '';
  return path.slice(path.lastIndexOf('/') + 1) || path;
};

const tabTooltip = (tab: ConversationTab): string =>
  [tab.title, STATUS_LABELS[tab.status ?? 'idle'], tab.cwd].filter(Boolean).join('\n');

const TabStatusIcon = ({ tab, reserve }: { tab: ConversationTab; reserve?: boolean }) => {
  const status = tab.status ?? 'idle';
  if (status === 'running') {
    return (
      <span className="chat-tab-status is-running">
        <Spinner />
        <span className="sr-only">{STATUS_LABELS.running}: </span>
      </span>
    );
  }
  if (tab.draft && status === 'idle') {
    return (
      <span aria-hidden="true" className="chat-tab-status is-draft">
        <MessageSquarePlus size={13} strokeWidth={1.8} />
      </span>
    );
  }
  if (status === 'idle') {
    // List rows reserve the slot so titles stay aligned.
    return reserve ? <span aria-hidden="true" className="chat-tab-status" /> : null;
  }
  return (
    <span className={cn('chat-tab-status', `is-${status}`)}>
      <span aria-hidden="true" className="chat-tab-dot" />
      <span className="sr-only">{STATUS_LABELS[status]}: </span>
    </span>
  );
};

interface TabMenuState {
  id: string;
  /** Element the menu is aligned to. */
  anchor: HTMLElement;
  /** Element focused again when the menu closes. */
  returnFocus: HTMLElement;
}

interface ConversationTabsProps {
  tabs: ConversationTab[];
  activeId: string;
  disabled?: boolean;
  onSelect: (id: string) => void;
  onClose: (id: string) => void;
  onNew: () => void;
  onReorder?: (ids: string[]) => void;
  /** Opens the new chat settings (profile, runner and workspace) for a draft tab. */
  onConfigOpen?: (id: string) => void;
}

const ConversationTabs = ({
  tabs,
  activeId,
  disabled,
  onSelect,
  onClose,
  onNew,
  onReorder,
  onConfigOpen,
}: ConversationTabsProps) => {
  const [listOpen, setListOpen] = useState(false);
  const [tabMenu, setTabMenu] = useState<TabMenuState | null>(null);
  const [menuLeft, setMenuLeft] = useState(0);
  const [draggedId, setDraggedId] = useState<string | null>(null);
  const headerRef = useRef<HTMLElement>(null);
  const stripRef = useRef<HTMLDivElement>(null);
  const switcherRef = useRef<HTMLButtonElement>(null);
  const tabRefs = useRef(new Map<string, HTMLButtonElement>());
  const listRef = useRef<HTMLDivElement>(null);
  const listPanelRef = useRef<HTMLDivElement>(null);
  const listOpenerRef = useRef<HTMLElement | null>(null);
  const tabMenuRef = useRef<HTMLDivElement>(null);
  // Focus target after a close re-renders the tabs: a tab ID, or null for the active tab.
  const pendingFocusRef = useRef<string | null | undefined>(undefined);
  const activeIdRef = useRef(activeId);
  activeIdRef.current = activeId;
  const listId = useId();
  const tabMenuId = useId();
  const activeTab = tabs.find((tab) => tab.id === activeId);
  const menuTab = tabMenu ? tabs.find((tab) => tab.id === tabMenu.id) : undefined;
  const attentionCount = tabs.filter(
    (tab) => tab.id !== activeId && (tab.status === 'attention' || tab.status === 'error')
  ).length;

  useEffect(() => {
    tabRefs.current.get(activeId)?.scrollIntoView?.({ block: 'nearest', inline: 'nearest' });
  }, [activeId]);

  useEffect(() => {
    if (!listOpen && !tabMenu) return undefined;
    const dismiss = (event: PointerEvent) => {
      const target = event.target as Element;
      if (listOpen && !listRef.current?.contains(target)) setListOpen(false);
      // Anchors toggle the menu themselves on click.
      if (tabMenu && !tabMenuRef.current?.contains(target) && !tabMenu.anchor.contains(target)) {
        setTabMenu(null);
      }
    };
    document.addEventListener('pointerdown', dismiss);
    return () => document.removeEventListener('pointerdown', dismiss);
  }, [listOpen, tabMenu]);

  // Position against the header: the tab list scrolls, so it would clip the menu.
  useLayoutEffect(() => {
    const header = headerRef.current;
    const menu = tabMenuRef.current;
    if (!tabMenu || !header || !menu) return;
    const headerRect = header.getBoundingClientRect();
    const anchorRect = tabMenu.anchor.getBoundingClientRect();
    const maxLeft = headerRect.width - menu.offsetWidth - MENU_EDGE_GAP;
    setMenuLeft(Math.max(MENU_EDGE_GAP, Math.min(anchorRect.left - headerRect.left, maxLeft)));
    menu.querySelector<HTMLElement>('[role="menuitem"]')?.focus();
  }, [tabMenu]);

  useEffect(() => {
    if (tabMenu && !menuTab) setTabMenu(null);
  }, [tabMenu, menuTab]);

  const focusTab = (id: string) => queueMicrotask(() => tabRefs.current.get(id)?.focus());

  const isCompact = () => {
    const strip = stripRef.current;
    return Boolean(strip && getComputedStyle(strip).display === 'none');
  };

  /** Focuses a tab, or the switcher while narrow layouts hide the tabs. */
  const focusVisibleTab = (id?: string) => {
    if (isCompact()) {
      switcherRef.current?.focus();
      return;
    }
    const target =
      (id === undefined ? undefined : tabRefs.current.get(id)) ??
      tabRefs.current.get(activeIdRef.current) ??
      tabRefs.current.values().next().value;
    target?.focus();
  };

  // Focus on the active tab's control follows the layout between the tabs and the switcher,
  // which can change after a selection settles. Clicking or focusing elsewhere releases it.
  const focusFollowsLayoutRef = useRef(false);
  const holdTabFocus = () => {
    focusFollowsLayoutRef.current = true;
  };
  const compactRef = useRef<boolean | undefined>(undefined);
  const followLayout = useEffectEvent(() => {
    const compact = isCompact();
    const changed = compactRef.current !== undefined && compact !== compactRef.current;
    compactRef.current = compact;
    if (!changed || !focusFollowsLayoutRef.current) return;
    const focused = document.activeElement;
    // A control hidden by the change has lost focus to the page, or still holds it unseen.
    if (focused && focused !== document.body && !headerRef.current?.contains(focused)) return;
    focusVisibleTab();
  });

  useEffect(() => {
    const header = headerRef.current;
    if (!header) return undefined;
    const release = (event: Event) => {
      const target = event.target instanceof Element ? event.target : null;
      const onTabControl =
        Boolean(target && switcherRef.current?.contains(target)) ||
        Boolean(target?.closest('[role="tab"]') && header.contains(target));
      if (!onTabControl) focusFollowsLayoutRef.current = false;
    };
    document.addEventListener('focusin', release);
    document.addEventListener('pointerdown', release);
    const observer =
      typeof ResizeObserver === 'undefined' ? undefined : new ResizeObserver(() => followLayout());
    observer?.observe(header);
    return () => {
      document.removeEventListener('focusin', release);
      document.removeEventListener('pointerdown', release);
      observer?.disconnect();
    };
  }, []);

  const closeTab = (id: string) => {
    const index = tabs.findIndex((tab) => tab.id === id);
    onClose(id);
    pendingFocusRef.current = (tabs[index + 1] ?? tabs[index - 1])?.id ?? null;
  };

  useEffect(() => {
    const pending = pendingFocusRef.current;
    if (pending === undefined) return;
    pendingFocusRef.current = undefined;
    focusVisibleTab(pending ?? undefined);
  });

  const openList = (opener: HTMLElement) => {
    setTabMenu(null);
    listOpenerRef.current = opener;
    setListOpen(true);
  };

  const closeList = (restoreFocus: boolean) => {
    setListOpen(false);
    const opener = listOpenerRef.current;
    if (restoreFocus && opener?.isConnected) opener.focus();
  };

  useEffect(() => {
    if (!listOpen) return;
    const panel = listPanelRef.current;
    (
      panel?.querySelector<HTMLElement>('[aria-current="page"]') ??
      panel?.querySelector<HTMLElement>('button')
    )?.focus();
  }, [listOpen]);

  /** Opens a tab's menu below `anchor`; focus returns to the tab, or to `returnFocus`. */
  const openTabMenu = (id: string, anchor: HTMLElement | null, returnFocus?: HTMLElement) => {
    const tabButton = tabRefs.current.get(id);
    const focusTarget = returnFocus ?? tabButton;
    if (!anchor || !focusTarget || disabled) return;
    setListOpen(false);
    setTabMenu({ id, anchor, returnFocus: focusTarget });
  };

  const toggleTabMenu = (id: string, anchor: HTMLElement | null, returnFocus?: HTMLElement) => {
    if (tabMenu?.id === id) setTabMenu(null);
    else openTabMenu(id, anchor, returnFocus);
  };

  const dismissTabMenu = (restoreFocus: boolean) => {
    if (restoreFocus && tabMenu?.returnFocus.isConnected) tabMenu.returnFocus.focus();
    setTabMenu(null);
  };

  const handleTabKeyDown = (event: KeyboardEvent<HTMLButtonElement>, index: number) => {
    const tab = tabs[index];
    if (event.key === 'ContextMenu' || (event.shiftKey && event.key === 'F10')) {
      event.preventDefault();
      openTabMenu(tab.id, event.currentTarget.parentElement);
      return;
    }
    if (event.ctrlKey || event.altKey || event.metaKey) return;
    if (event.key === 'Delete') {
      event.preventDefault();
      closeTab(tab.id);
      return;
    }
    const next =
      event.key === 'ArrowRight'
        ? (index + 1) % tabs.length
        : event.key === 'ArrowLeft'
          ? (index - 1 + tabs.length) % tabs.length
          : event.key === 'Home'
            ? 0
            : event.key === 'End'
              ? tabs.length - 1
              : -1;
    if (next < 0) return;
    event.preventDefault();
    onSelect(tabs[next].id);
    focusTab(tabs[next].id);
  };

  const handleMenuKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    const items = Array.from(
      event.currentTarget.querySelectorAll<HTMLElement>('[role="menuitem"]')
    );
    const index = items.indexOf(document.activeElement as HTMLElement);
    if (event.key === 'Escape') {
      event.preventDefault();
      event.stopPropagation();
      dismissTabMenu(true);
    } else if (event.key === 'Tab') {
      setTabMenu(null);
    } else if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
      event.preventDefault();
      const delta = event.key === 'ArrowDown' ? 1 : -1;
      items[(index + delta + items.length) % items.length]?.focus();
    } else if (event.key === 'Home' || event.key === 'End') {
      event.preventDefault();
      items[event.key === 'Home' ? 0 : items.length - 1]?.focus();
    }
  };

  const drop = (targetIndex: number) => {
    if (!onReorder || draggedId === null) return;
    const ids = tabs.map((tab) => tab.id).filter((id) => id !== draggedId);
    ids.splice(targetIndex, 0, draggedId);
    onReorder(ids);
    setDraggedId(null);
  };

  const menuItem = (
    label: string,
    icon: ReactNode,
    onActivate: () => void,
    options: { shortcut?: string; restoreFocus?: boolean } = {}
  ) => (
    <button
      aria-keyshortcuts={options.shortcut}
      className="chat-tab-menu-item"
      onClick={() => {
        dismissTabMenu(options.restoreFocus ?? true);
        onActivate();
      }}
      role="menuitem"
      tabIndex={-1}
      type="button"
    >
      {icon}
      <span className="chat-tab-menu-label">{label}</span>
      {options.shortcut ? (
        <span aria-hidden="true" className="chat-tab-menu-shortcut">
          Del
        </span>
      ) : null}
    </button>
  );

  const tabMenuElement =
    tabMenu && menuTab ? (
      <div
        aria-label={`${menuTab.title} actions`}
        className="chat-tab-menu"
        id={tabMenuId}
        onKeyDown={handleMenuKeyDown}
        ref={tabMenuRef}
        role="menu"
        style={{ left: menuLeft }}
      >
        <div className="chat-tab-menu-header" role="none">
          <span className="chat-tab-menu-title">{menuTab.title}</span>
          {menuTab.cwd ? (
            <span className="chat-tab-menu-path">
              <FolderOpen aria-hidden="true" size={12} strokeWidth={1.6} />
              {menuTab.cwd}
            </span>
          ) : null}
        </div>
        {menuTab.draft ? (
          onConfigOpen ? (
            menuItem(
              'Config',
              <SlidersHorizontal aria-hidden="true" size={14} strokeWidth={1.7} />,
              // Focus returns to the tab first, so the dialog restores it on close.
              () => onConfigOpen(menuTab.id)
            )
          ) : null
        ) : (
          <>
            {menuItem(
              'Copy conversation ID',
              <Copy aria-hidden="true" size={14} strokeWidth={1.7} />,
              () => {
                void copyToClipboard(menuTab.id);
              }
            )}
            {menuTab.cwd
              ? menuItem(
                  'Copy workspace path',
                  <Copy aria-hidden="true" size={14} strokeWidth={1.7} />,
                  () => {
                    void copyToClipboard(menuTab.cwd ?? '');
                  }
                )
              : null}
          </>
        )}
        <hr className="chat-tab-menu-separator" />
        {menuItem('Close tab', <X aria-hidden="true" size={14} />, () => closeTab(menuTab.id), {
          shortcut: 'Delete',
          restoreFocus: false,
        })}
      </div>
    ) : null;

  const listElement = listOpen ? (
    <div
      aria-label="Open conversations"
      className="chat-tabs-menu"
      id={listId}
      ref={listPanelRef}
      role="dialog"
    >
      <ul className="chat-tabs-menu-list">
        {tabs.map((tab) => (
          <li
            className={cn('chat-tabs-menu-item', tab.id === activeId && 'is-active')}
            key={tab.id}
          >
            <button
              aria-current={tab.id === activeId ? 'page' : undefined}
              className="chat-tabs-menu-select"
              onClick={() => {
                setListOpen(false);
                onSelect(tab.id);
                focusVisibleTab(tab.id);
              }}
              title={tabTooltip(tab)}
              type="button"
            >
              <TabStatusIcon reserve tab={tab} />
              <span className="chat-tabs-menu-title">{tab.title}</span>
              {tab.cwd ? <span className="chat-tabs-menu-cwd">{basename(tab.cwd)}</span> : null}
            </button>
          </li>
        ))}
      </ul>
      <button
        className="chat-tabs-menu-new"
        onClick={() => {
          // The opener takes focus first, so a dialog opened by onNew returns to it.
          closeList(true);
          onNew();
        }}
        type="button"
      >
        <Plus aria-hidden="true" size={14} />
        New chat
      </button>
    </div>
  ) : null;

  return (
    <section
      aria-label="Conversation tabs"
      className="chat-tabs-header"
      data-testid="chat-tabs-header"
      ref={headerRef}
    >
      <div className="chat-tabs-strip" ref={stripRef}>
        <div aria-label="Open conversations" className="chat-tabs-list" role="tablist">
          {tabs.map((tab, index) => {
            const active = tab.id === activeId;
            const menuOpen = tabMenu?.id === tab.id;
            return (
              <div
                className={cn(
                  'chat-tab',
                  active && 'is-active',
                  menuOpen && 'is-menu-open',
                  tab.draft && 'is-draft',
                  draggedId === tab.id && 'is-dragging'
                )}
                data-status={tab.status ?? 'idle'}
                key={tab.id}
                role="presentation"
              >
                <button
                  aria-keyshortcuts="Delete Shift+F10"
                  aria-selected={active}
                  className="chat-tab-select"
                  disabled={disabled}
                  draggable={Boolean(onReorder) && !disabled}
                  onAuxClick={(event) => {
                    if (event.button === 1) {
                      event.preventDefault();
                      closeTab(tab.id);
                    }
                  }}
                  onClick={() => onSelect(tab.id)}
                  onContextMenu={(event) => {
                    event.preventDefault();
                    openTabMenu(tab.id, event.currentTarget.parentElement);
                  }}
                  onDragEnd={() => setDraggedId(null)}
                  onDragOver={(event) => {
                    if (draggedId !== null) event.preventDefault();
                  }}
                  onDragStart={(event) => {
                    event.dataTransfer.setData('text/plain', tab.id);
                    setDraggedId(tab.id);
                  }}
                  onDrop={(event) => {
                    event.preventDefault();
                    drop(index);
                  }}
                  onKeyDown={(event) => handleTabKeyDown(event, index)}
                  onMouseDown={(event) => {
                    // Stop middle-click autoscroll so auxclick can close the tab.
                    if (event.button === 1) event.preventDefault();
                  }}
                  ref={(button) => {
                    if (button) tabRefs.current.set(tab.id, button);
                    else tabRefs.current.delete(tab.id);
                  }}
                  onFocus={holdTabFocus}
                  role="tab"
                  tabIndex={active ? 0 : -1}
                  title={tabTooltip(tab)}
                  type="button"
                >
                  <TabStatusIcon tab={tab} />
                  <span className="chat-tab-title">{tab.title}</span>
                </button>
                <button
                  aria-controls={menuOpen ? tabMenuId : undefined}
                  aria-expanded={menuOpen}
                  aria-haspopup="menu"
                  aria-label={`${tab.title} actions`}
                  className="chat-tab-actions"
                  disabled={disabled}
                  onClick={(event) => toggleTabMenu(tab.id, event.currentTarget.parentElement)}
                  tabIndex={-1}
                  title="Copy or close"
                  type="button"
                >
                  <ChevronDown aria-hidden="true" size={13} />
                </button>
              </div>
            );
          })}
        </div>
        <button
          aria-label="New chat tab"
          className="chat-tabs-icon-button"
          disabled={disabled}
          onClick={onNew}
          title="New chat tab (Ctrl/⌘+Shift+O)"
          type="button"
        >
          <Plus aria-hidden="true" size={15} />
        </button>
      </div>

      {/* biome-ignore lint/a11y/noStaticElementInteractions: Escape closes the list from its trigger or its rows. */}
      <div
        className="chat-tabs-trailing"
        onKeyDown={(event) => {
          if (event.key !== 'Escape' || !listOpen) return;
          event.preventDefault();
          event.stopPropagation();
          closeList(true);
        }}
        ref={listRef}
      >
        {/* Below ~34rem of main-panel width the strip collapses into this switcher. */}
        <button
          aria-controls={listOpen ? listId : undefined}
          aria-expanded={listOpen}
          aria-haspopup="dialog"
          aria-label={`Switch conversation tab, ${tabs.length} open`}
          className="chat-tabs-switcher"
          onClick={(event) => (listOpen ? closeList(false) : openList(event.currentTarget))}
          onFocus={holdTabFocus}
          ref={switcherRef}
          type="button"
        >
          {activeTab ? <TabStatusIcon tab={activeTab} /> : null}
          <span className="chat-tab-title">{activeTab?.title ?? 'No conversation'}</span>
          <span className="chat-tabs-count">
            {tabs.length}
            {attentionCount > 0 ? (
              <span aria-hidden="true" className="chat-tabs-count-dot" />
            ) : null}
          </span>
          <ChevronsUpDown aria-hidden="true" size={14} />
        </button>
        {activeTab ? (
          <button
            aria-controls={tabMenu?.id === activeTab.id ? tabMenuId : undefined}
            aria-expanded={tabMenu?.id === activeTab.id}
            aria-haspopup="menu"
            aria-label="Current tab actions"
            className="chat-tabs-icon-button chat-tabs-active-actions"
            disabled={disabled}
            onClick={(event) =>
              toggleTabMenu(activeTab.id, event.currentTarget, event.currentTarget)
            }
            title="Copy or close"
            type="button"
          >
            <ChevronDown aria-hidden="true" size={15} />
          </button>
        ) : null}
        <button
          aria-controls={listOpen ? listId : undefined}
          aria-expanded={listOpen}
          aria-haspopup="dialog"
          aria-label="All conversation tabs"
          className="chat-tabs-icon-button chat-tabs-menu-trigger"
          onClick={(event) => (listOpen ? closeList(false) : openList(event.currentTarget))}
          title="All conversation tabs"
          type="button"
        >
          <ChevronsUpDown aria-hidden="true" size={15} />
          {attentionCount > 0 ? <span aria-hidden="true" className="chat-tabs-count-dot" /> : null}
        </button>
        {listElement}
      </div>
      {tabMenuElement}
    </section>
  );
};

export default ConversationTabs;
