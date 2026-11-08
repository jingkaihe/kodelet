import { act, renderHook } from '@testing-library/react';
import { beforeEach, describe, expect, it } from 'vitest';
import {
  CONVERSATION_TABS_STORAGE_KEY,
  closeConversationTab,
  MAX_CONVERSATION_TABS,
  NEW_CHAT_TAB_ID,
  openConversationTab,
  parseConversationTabs,
  reorderConversationTabs,
  replaceConversationTab,
  useConversationTabs,
} from './useConversationTabs';

const ids = (tabs: { id: string }[]) => tabs.map((tab) => tab.id);

const renderTabs = (conversationId: string | null) =>
  renderHook(({ id }: { id: string | null }) => useConversationTabs(id), {
    initialProps: { id: conversationId },
  });

describe('conversation tab helpers', () => {
  it('opens a tab next to the one it was opened from and keeps open tabs in place', () => {
    const tabs = [{ id: 'a' }, { id: 'b' }, { id: 'c' }];

    expect(ids(openConversationTab(tabs, 'd', 'a'))).toEqual(['a', 'd', 'b', 'c']);
    expect(ids(openConversationTab(tabs, 'd'))).toEqual(['a', 'b', 'c', 'd']);
    expect(ids(openConversationTab(tabs, 'd', 'missing'))).toEqual(['a', 'b', 'c', 'd']);
    expect(openConversationTab(tabs, 'b', 'c')).toBe(tabs);
  });

  it('drops the leftmost tabs past the limit but never the opened one', () => {
    const tabs = Array.from({ length: MAX_CONVERSATION_TABS }, (_, index) => ({
      id: `conv-${index}`,
    }));

    const next = openConversationTab(tabs, 'latest', 'conv-0');

    expect(next).toHaveLength(MAX_CONVERSATION_TABS);
    expect(ids(next).slice(0, 2)).toEqual(['latest', 'conv-1']);
  });

  it('closes the active tab onto its right neighbour, else its left one', () => {
    const tabs = [{ id: 'a' }, { id: 'b' }, { id: 'c' }];

    expect(closeConversationTab(tabs, 'b', 'b')).toEqual({
      tabs: [{ id: 'a' }, { id: 'c' }],
      nextActiveId: 'c',
    });
    expect(closeConversationTab(tabs, 'c', 'c').nextActiveId).toBe('b');
    expect(closeConversationTab(tabs, 'a', 'c')).toEqual({ tabs: [{ id: 'b' }, { id: 'c' }] });
    expect(closeConversationTab(tabs, 'missing', 'a').tabs).toBe(tabs);
  });

  it('keeps a new chat tab after the last tab closes', () => {
    expect(closeConversationTab([{ id: 'a' }], 'a', 'a')).toEqual({
      tabs: [{ id: NEW_CHAT_TAB_ID }],
      nextActiveId: NEW_CHAT_TAB_ID,
    });
  });

  it('replaces a tab in place without duplicating the new ID', () => {
    const tabs = [{ id: 'a' }, { id: NEW_CHAT_TAB_ID, title: 'stale' }, { id: 'b' }];

    expect(replaceConversationTab(tabs, NEW_CHAT_TAB_ID, 'c')).toEqual([
      { id: 'a' },
      { id: 'c' },
      { id: 'b' },
    ]);
    expect(ids(replaceConversationTab(tabs, NEW_CHAT_TAB_ID, 'b'))).toEqual(['a', 'b']);
    expect(replaceConversationTab(tabs, 'missing', 'c')).toBe(tabs);
  });

  it('reorders tabs and keeps unlisted ones at the end', () => {
    const tabs = [{ id: 'a' }, { id: 'b' }, { id: 'c' }];

    expect(ids(reorderConversationTabs(tabs, ['c', 'a', 'unknown']))).toEqual(['c', 'a', 'b']);
  });

  it('ignores invalid, duplicate, and unknown stored data', () => {
    expect(parseConversationTabs('not json')).toEqual([]);
    expect(parseConversationTabs('{"tabs":"nope"}')).toEqual([]);
    expect(
      parseConversationTabs(
        JSON.stringify({
          tabs: [
            { id: 'a', title: 'Alpha', cwd: '/work', extra: true },
            { id: 'a' },
            { id: '../escape' },
            { id: NEW_CHAT_TAB_ID },
            { id: 7 },
            null,
          ],
        })
      )
    ).toEqual([
      { id: 'a', title: 'Alpha', cwd: '/work' },
      { id: NEW_CHAT_TAB_ID, title: undefined, cwd: undefined },
    ]);
  });
});

describe('useConversationTabs', () => {
  beforeEach(() => {
    window.sessionStorage.clear();
  });

  it('follows the route, opening each new conversation next to the previous tab', () => {
    const { result, rerender } = renderTabs('a');
    expect(ids(result.current.tabs)).toEqual(['a']);
    expect(result.current.activeId).toBe('a');

    rerender({ id: 'b' });
    rerender({ id: 'a' });
    rerender({ id: 'c' });
    rerender({ id: null });

    expect(ids(result.current.tabs)).toEqual(['a', 'c', NEW_CHAT_TAB_ID, 'b']);
    expect(result.current.activeId).toBe(NEW_CHAT_TAB_ID);
  });

  it('returns the next tab when the active one closes and does not reopen it', () => {
    const { result, rerender } = renderTabs('a');
    rerender({ id: 'b' });

    let next: string | undefined;
    act(() => {
      next = result.current.close('b');
    });

    expect(next).toBe('a');
    expect(ids(result.current.tabs)).toEqual(['a']);

    rerender({ id: 'a' });
    expect(ids(result.current.tabs)).toEqual(['a']);
  });

  it('restores reordered tabs and their remembered titles in the same window', () => {
    const first = renderTabs('a');
    first.rerender({ id: 'b' });
    act(() => first.result.current.remember([{ id: 'b', title: 'Beta', cwd: '/beta' }]));
    act(() => first.result.current.reorder(['b', 'a']));
    first.unmount();

    expect(
      JSON.parse(window.sessionStorage.getItem(CONVERSATION_TABS_STORAGE_KEY) || '{}')
    ).toEqual({ tabs: [{ id: 'b', title: 'Beta', cwd: '/beta' }, { id: 'a' }] });

    const { result } = renderTabs('c');
    expect(result.current.tabs).toEqual([
      { id: 'b', title: 'Beta', cwd: '/beta' },
      { id: 'a', title: undefined, cwd: undefined },
      { id: 'c' },
    ]);
  });

  it('keeps the same tabs when nothing remembered has changed', () => {
    const { result } = renderTabs('a');
    act(() => result.current.remember([{ id: 'a', title: 'Alpha' }]));
    const tabs = result.current.tabs;

    act(() =>
      result.current.remember([
        { id: 'a', title: 'Alpha' },
        { id: 'closed', title: 'X' },
      ])
    );

    expect(result.current.tabs).toBe(tabs);
  });
});
