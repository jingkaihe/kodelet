import { useCallback, useEffect, useRef, useState } from 'react';

/** Tab key of the unsaved new chat at `/`; conversation IDs never start with a colon. */
export const NEW_CHAT_TAB_ID = ':new';

// Tabs belong to one browser window, so they live in session storage.
export const CONVERSATION_TABS_STORAGE_KEY = 'kodelet.chat.tabs.v1';
export const MAX_CONVERSATION_TABS = 100;
const MAX_ID_LENGTH = 128;
const MAX_TITLE_LENGTH = 200;
const MAX_CWD_LENGTH = 4096;
const CONVERSATION_ID_PATTERN = /^[A-Za-z0-9_.-]+$/;

export interface ConversationTabRecord {
  id: string;
  /** Last known title and workspace, for conversations missing from the sidebar list. */
  title?: string;
  cwd?: string;
}

export interface ConversationTabMeta {
  id: string;
  title?: string;
  cwd?: string;
}

interface TabsState {
  tabs: ConversationTabRecord[];
  /** Route the tabs were last synchronised with; new routes open next to it. */
  routeKey: string;
}

export const routeTabKey = (conversationId: string | null | undefined): string =>
  conversationId || NEW_CHAT_TAB_ID;

const isTabId = (id: unknown): id is string =>
  id === NEW_CHAT_TAB_ID ||
  (typeof id === 'string' && id.length <= MAX_ID_LENGTH && CONVERSATION_ID_PATTERN.test(id));

const optionalText = (value: unknown, maxLength: number): string | undefined =>
  typeof value === 'string' && value ? value.slice(0, maxLength) : undefined;

export const parseConversationTabs = (raw: string | null): ConversationTabRecord[] => {
  if (!raw) return [];
  try {
    const stored = JSON.parse(raw) as { tabs?: unknown };
    if (!Array.isArray(stored?.tabs)) return [];
    const seen = new Set<string>();
    const tabs: ConversationTabRecord[] = [];
    for (const entry of stored.tabs.slice(0, MAX_CONVERSATION_TABS)) {
      const id = (entry as { id?: unknown })?.id;
      if (!isTabId(id) || seen.has(id)) continue;
      seen.add(id);
      // Rebuild records to drop unknown fields.
      tabs.push({
        id,
        title: optionalText((entry as { title?: unknown }).title, MAX_TITLE_LENGTH),
        cwd: optionalText((entry as { cwd?: unknown }).cwd, MAX_CWD_LENGTH),
      });
    }
    return tabs;
  } catch {
    return [];
  }
};

const readTabs = (): ConversationTabRecord[] => {
  try {
    return parseConversationTabs(window.sessionStorage.getItem(CONVERSATION_TABS_STORAGE_KEY));
  } catch {
    return [];
  }
};

/** Opens `id` next to `afterId`, or at the end; an open tab keeps its place. */
export const openConversationTab = (
  tabs: ConversationTabRecord[],
  id: string,
  afterId?: string
): ConversationTabRecord[] => {
  if (tabs.some((tab) => tab.id === id)) return tabs;
  const index = afterId === undefined ? -1 : tabs.findIndex((tab) => tab.id === afterId);
  const next = [...tabs];
  next.splice(index < 0 ? next.length : index + 1, 0, { id });
  // Past the limit, the leftmost tabs give way; closing never stops a conversation.
  let excess = next.length - MAX_CONVERSATION_TABS;
  return excess > 0
    ? next.filter((tab) => {
        if (excess === 0 || tab.id === id) return true;
        excess -= 1;
        return false;
      })
    : next;
};

/**
 * Closes `id`. When it was active, also returns the tab to show next: its right
 * neighbour, else its left one. A window always keeps at least the new chat tab.
 */
export const closeConversationTab = (
  tabs: ConversationTabRecord[],
  id: string,
  activeId: string
): { tabs: ConversationTabRecord[]; nextActiveId?: string } => {
  const index = tabs.findIndex((tab) => tab.id === id);
  if (index < 0) return { tabs };
  const remaining = tabs.filter((tab) => tab.id !== id);
  const next = remaining.length > 0 ? remaining : [{ id: NEW_CHAT_TAB_ID }];
  if (id !== activeId) return { tabs: next };
  return { tabs: next, nextActiveId: next[Math.min(index, next.length - 1)].id };
};

/** Replaces `fromId` in place, as when a new chat becomes a saved conversation. */
export const replaceConversationTab = (
  tabs: ConversationTabRecord[],
  fromId: string,
  toId: string
): ConversationTabRecord[] => {
  if (fromId === toId || !tabs.some((tab) => tab.id === fromId)) return tabs;
  return tabs
    .filter((tab) => tab.id !== toId)
    .map((tab) => (tab.id === fromId ? { id: toId } : tab));
};

export const reorderConversationTabs = (
  tabs: ConversationTabRecord[],
  ids: string[]
): ConversationTabRecord[] => {
  const byId = new Map(tabs.map((tab) => [tab.id, tab]));
  const ordered = ids.flatMap((id) => {
    const tab = byId.get(id);
    byId.delete(id);
    return tab ? [tab] : [];
  });
  // Tabs missing from `ids` keep their relative order at the end.
  return [...ordered, ...byId.values()];
};

const rememberConversationTabs = (
  tabs: ConversationTabRecord[],
  meta: ConversationTabMeta[]
): ConversationTabRecord[] => {
  const known = new Map(meta.map((entry) => [entry.id, entry]));
  let changed = false;
  const next = tabs.map((tab) => {
    const entry = known.get(tab.id);
    if (!entry) return tab;
    const title = optionalText(entry.title, MAX_TITLE_LENGTH) ?? tab.title;
    const cwd = optionalText(entry.cwd, MAX_CWD_LENGTH) ?? tab.cwd;
    if (title === tab.title && cwd === tab.cwd) return tab;
    changed = true;
    return { ...tab, title, cwd };
  });
  return changed ? next : tabs;
};

/**
 * Tracks the conversations open in this window. The route decides the active
 * tab: visiting a conversation opens its tab next to the one it was opened from.
 */
export const useConversationTabs = (conversationId: string | null) => {
  const routeKey = routeTabKey(conversationId);
  const [state, setState] = useState<TabsState>(() => ({
    tabs: openConversationTab(readTabs(), routeKey),
    routeKey,
  }));
  let current = state;
  if (state.routeKey !== routeKey) {
    // Adjust during render so the new route never renders without its tab.
    current = { tabs: openConversationTab(state.tabs, routeKey, state.routeKey), routeKey };
    setState(current);
  }
  const stateRef = useRef(current);
  stateRef.current = current;

  useEffect(() => {
    try {
      window.sessionStorage.setItem(
        CONVERSATION_TABS_STORAGE_KEY,
        JSON.stringify({ tabs: current.tabs })
      );
    } catch {
      // Tabs still work for this page when storage is unavailable.
    }
  }, [current.tabs]);

  const update = useCallback(
    (change: (tabs: ConversationTabRecord[]) => ConversationTabRecord[]) => {
      const tabs = change(stateRef.current.tabs);
      if (tabs === stateRef.current.tabs) return;
      stateRef.current = { ...stateRef.current, tabs };
      setState(stateRef.current);
    },
    []
  );

  /** Returns the tab to navigate to when the closed tab was active. */
  const close = useCallback(
    (id: string): string | undefined => {
      const result = closeConversationTab(stateRef.current.tabs, id, stateRef.current.routeKey);
      update(() => result.tabs);
      return result.nextActiveId;
    },
    [update]
  );

  const replace = useCallback(
    (fromId: string, toId: string) => update((tabs) => replaceConversationTab(tabs, fromId, toId)),
    [update]
  );

  const reorder = useCallback(
    (ids: string[]) => update((tabs) => reorderConversationTabs(tabs, ids)),
    [update]
  );

  const remember = useCallback(
    (meta: ConversationTabMeta[]) => update((tabs) => rememberConversationTabs(tabs, meta)),
    [update]
  );

  return {
    tabs: current.tabs,
    activeId: routeKey,
    close,
    replace,
    reorder,
    remember,
  };
};
