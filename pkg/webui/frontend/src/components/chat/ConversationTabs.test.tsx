import { act, fireEvent, render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { type ComponentProps, useState } from 'react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import ConversationTabs, {
  adjacentConversationTabId,
  type ConversationTab,
} from './ConversationTabs';

const tabs: ConversationTab[] = [
  { id: 'conv-a', title: 'Alpha', cwd: '/work/alpha' },
  { id: 'conv-b', title: 'Beta', cwd: '/work/beta', status: 'running' },
  { id: 'conv-c', title: 'Gamma', status: 'unread' },
  { id: 'conv-d', title: 'Delta', status: 'attention' },
  { id: 'conv-e', title: 'Epsilon', status: 'error' },
];

const renderTabs = (props: Partial<ComponentProps<typeof ConversationTabs>> = {}) => {
  const handlers = {
    onSelect: vi.fn(),
    onClose: vi.fn(),
    onNew: vi.fn(),
    onReorder: vi.fn(),
    onConfigOpen: vi.fn(),
  };
  render(<ConversationTabs activeId="conv-a" tabs={tabs} {...handlers} {...props} />);
  return handlers;
};

const tab = (name: string) => screen.getByRole('tab', { name: new RegExp(name) });

/** Closes and selects tabs the way the chat page does. */
const StatefulTabs = ({ initial }: { initial: ConversationTab[] }) => {
  const [state, setState] = useState({ tabs: initial, activeId: initial[0].id });
  return (
    <ConversationTabs
      activeId={state.activeId}
      onClose={(id) =>
        setState(({ tabs: current, activeId }) => {
          const index = current.findIndex((item) => item.id === id);
          const rest = current.filter((item) => item.id !== id);
          const next = rest.length > 0 ? rest : [{ id: 'new', title: 'New chat', draft: true }];
          return {
            tabs: next,
            activeId: activeId === id ? next[Math.min(index, next.length - 1)].id : activeId,
          };
        })
      }
      onNew={vi.fn()}
      onSelect={(id) => setState((current) => ({ ...current, activeId: id }))}
      tabs={state.tabs}
    />
  );
};

const setTabStripDisplay = (display: string) => {
  const strip = document.querySelector<HTMLElement>('.chat-tabs-strip');
  if (strip) strip.style.display = display;
};
const collapseTabStrip = () => setTabStripDisplay('none');
const expandTabStrip = () => setTabStripDisplay('');

/** Replaces ResizeObserver and returns a function that reports a header resize. */
const stubResizeObserver = () => {
  const callbacks: (() => void)[] = [];
  vi.stubGlobal(
    'ResizeObserver',
    class {
      constructor(callback: () => void) {
        callbacks.push(callback);
      }
      observe() {}
      disconnect() {}
    }
  );
  return () =>
    act(() => {
      for (const callback of callbacks) callback();
    });
};

describe('ConversationTabs', () => {
  it('shows each open conversation with its background status', () => {
    renderTabs();

    expect(screen.getAllByRole('tab')).toHaveLength(5);
    for (const name of [
      'Alpha',
      'Running: Beta',
      'New activity: Gamma',
      'Needs input: Delta',
      'Failed: Epsilon',
    ]) {
      expect(screen.getByRole('tab', { name })).toBeInTheDocument();
    }
    expect(tab('Alpha')).toHaveAttribute('aria-selected', 'true');
    expect(tab('Alpha')).toHaveAttribute('tabindex', '0');
    expect(tab('Beta')).toHaveAttribute('tabindex', '-1');
    expect(tab('Beta')).toHaveAttribute('title', 'Beta\nRunning\n/work/beta');
  });

  it('selects tabs by click and moves with the arrow, Home and End keys', () => {
    const { onSelect } = renderTabs();

    fireEvent.click(tab('Gamma'));
    expect(onSelect).toHaveBeenLastCalledWith('conv-c');

    fireEvent.keyDown(tab('Alpha'), { key: 'ArrowRight' });
    expect(onSelect).toHaveBeenLastCalledWith('conv-b');
    fireEvent.keyDown(tab('Alpha'), { key: 'ArrowLeft' });
    expect(onSelect).toHaveBeenLastCalledWith('conv-e');
    fireEvent.keyDown(tab('Alpha'), { key: 'End' });
    expect(onSelect).toHaveBeenLastCalledWith('conv-e');
    fireEvent.keyDown(tab('Gamma'), { key: 'Home' });
    expect(onSelect).toHaveBeenLastCalledWith('conv-a');
    // Modified keys belong to the page shortcuts.
    fireEvent.keyDown(tab('Alpha'), { key: 'ArrowRight', altKey: true });
    expect(onSelect).toHaveBeenCalledTimes(5);
  });

  it('closes tabs with Delete and middle-click without asking', () => {
    const { onClose } = renderTabs();

    fireEvent.keyDown(tab('Beta'), { key: 'Delete' });
    expect(onClose).toHaveBeenLastCalledWith('conv-b');

    fireEvent(tab('Gamma'), new MouseEvent('auxclick', { bubbles: true, button: 1 }));
    expect(onClose).toHaveBeenLastCalledWith('conv-c');
    expect(onClose).toHaveBeenCalledTimes(2);
  });

  it('copies the ID or closes from a saved conversation menu', async () => {
    const user = userEvent.setup();
    const writeText = vi.spyOn(navigator.clipboard, 'writeText');
    const { onClose } = renderTabs();

    await user.click(screen.getByRole('button', { name: 'Beta actions' }));
    const menu = screen.getByRole('menu', { name: 'Beta actions' });
    expect(menu).toHaveTextContent('Beta/work/beta');
    expect(
      within(menu)
        .getAllByRole('menuitem')
        .map((item) => item.textContent)
    ).toEqual(['Copy conversation ID', 'Close tab']);
    expect(within(menu).getByRole('menuitem', { name: 'Copy conversation ID' })).toHaveFocus();

    await user.keyboard('{ArrowDown}');
    expect(within(menu).getByRole('menuitem', { name: 'Close tab' })).toHaveFocus();
    await user.keyboard('{ArrowDown}');
    expect(within(menu).getByRole('menuitem', { name: 'Copy conversation ID' })).toHaveFocus();
    await user.keyboard('{Enter}');
    expect(writeText).toHaveBeenLastCalledWith('conv-b');
    expect(screen.queryByRole('menu')).not.toBeInTheDocument();
    expect(tab('Beta')).toHaveFocus();

    await user.click(screen.getByRole('button', { name: 'Beta actions' }));
    await user.click(screen.getByRole('menuitem', { name: 'Close tab' }));
    expect(onClose).toHaveBeenCalledExactlyOnceWith('conv-b');
  });

  it('opens the menu from the keyboard or right-click and closes it with Escape', async () => {
    const user = userEvent.setup();
    renderTabs();

    fireEvent.keyDown(tab('Alpha'), { key: 'F10', shiftKey: true });
    expect(screen.getByRole('menu', { name: 'Alpha actions' })).toBeInTheDocument();
    await user.keyboard('{End}');
    expect(screen.getByRole('menuitem', { name: 'Close tab' })).toHaveFocus();
    await user.keyboard('{Escape}');
    expect(screen.queryByRole('menu')).not.toBeInTheDocument();
    expect(tab('Alpha')).toHaveFocus();

    fireEvent.contextMenu(tab('Gamma'));
    expect(screen.getByRole('menu', { name: 'Gamma actions' })).toBeInTheDocument();
    await user.click(document.body);
    expect(screen.queryByRole('menu')).not.toBeInTheDocument();
  });

  it('offers only config and close for a new chat', async () => {
    const user = userEvent.setup();
    const { onConfigOpen } = renderTabs({
      activeId: 'new',
      tabs: [...tabs.slice(0, 2), { id: 'new', title: 'New chat', cwd: '/work/new', draft: true }],
    });

    await user.click(screen.getByRole('button', { name: 'New chat actions' }));
    const menu = screen.getByRole('menu', { name: 'New chat actions' });
    expect(menu).toHaveTextContent('/work/new');
    expect(
      within(menu)
        .getAllByRole('menuitem')
        .map((item) => item.textContent)
    ).toEqual(['Config', 'Close tab']);

    await user.click(within(menu).getByRole('menuitem', { name: 'Config' }));
    expect(onConfigOpen).toHaveBeenCalledExactlyOnceWith('new');
    // The tab takes focus first, so the settings dialog can return to it.
    expect(tab('New chat')).toHaveFocus();
  });

  it('lists every tab, flags background tabs that need attention, and opens new chats', async () => {
    const user = userEvent.setup();
    const { onSelect, onNew } = renderTabs();

    const trigger = screen.getByRole('button', { name: 'All conversation tabs' });
    expect(trigger.querySelector('.chat-tabs-count-dot')).not.toBeNull();
    expect(
      screen.getByRole('button', { name: 'Switch conversation tab, 5 open' })
    ).toHaveTextContent('Alpha5');

    await user.click(trigger);
    const list = screen.getByRole('dialog', { name: 'Open conversations' });
    expect(within(list).getByRole('button', { name: /Alpha/ })).toHaveAttribute(
      'aria-current',
      'page'
    );
    await user.click(within(list).getByRole('button', { name: /Delta/ }));
    expect(onSelect).toHaveBeenCalledExactlyOnceWith('conv-d');
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();

    await user.click(trigger);
    await user.click(screen.getByRole('button', { name: 'New chat' }));
    expect(onNew).toHaveBeenCalledOnce();
  });

  it('does not flag attention when only the active tab needs it', () => {
    renderTabs({ activeId: 'conv-d', tabs: tabs.slice(0, 4) });

    expect(
      screen
        .getByRole('button', { name: 'All conversation tabs' })
        .querySelector('.chat-tabs-count-dot')
    ).toBeNull();
  });

  it('reorders tabs by dragging', () => {
    const { onReorder } = renderTabs();
    const dataTransfer = { setData: vi.fn() };

    fireEvent.dragStart(tab('Epsilon'), { dataTransfer });
    fireEvent.dragOver(tab('Beta'));
    fireEvent.drop(tab('Beta'));

    expect(onReorder).toHaveBeenCalledExactlyOnceWith([
      'conv-a',
      'conv-e',
      'conv-b',
      'conv-c',
      'conv-d',
    ]);
  });
});

describe('ConversationTabs keyboard focus', () => {
  it('moves focus into the tab list and returns it on Escape', async () => {
    const user = userEvent.setup();
    renderTabs();
    const trigger = screen.getByRole('button', { name: 'All conversation tabs' });

    trigger.focus();
    await user.keyboard('{Enter}');
    const list = screen.getByRole('dialog', { name: 'Open conversations' });
    expect(within(list).getByRole('button', { name: /Alpha/ })).toHaveFocus();
    await user.keyboard('{Escape}');
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(trigger).toHaveFocus();

    // Escape also works while focus is still on the trigger.
    await user.keyboard('{Enter}');
    trigger.focus();
    await user.keyboard('{Escape}');
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(trigger).toHaveFocus();
  });

  it('focuses the chosen tab, or the switcher while the tabs are collapsed', async () => {
    const user = userEvent.setup();
    render(<StatefulTabs initial={tabs} />);

    await user.click(screen.getByRole('button', { name: 'All conversation tabs' }));
    await user.click(
      within(screen.getByRole('dialog', { name: 'Open conversations' })).getByRole('button', {
        name: /Gamma/,
      })
    );
    expect(tab('Gamma')).toHaveFocus();
    expect(tab('Gamma')).toHaveAttribute('aria-selected', 'true');

    collapseTabStrip();
    const switcher = screen.getByRole('button', { name: /Switch conversation tab/ });
    await user.click(switcher);
    await user.click(
      within(screen.getByRole('dialog', { name: 'Open conversations' })).getByRole('button', {
        name: /Beta/,
      })
    );
    expect(switcher).toHaveFocus();
  });

  it('focuses the neighbouring tab after a close, then the new chat after the last one', async () => {
    const user = userEvent.setup();
    render(<StatefulTabs initial={tabs.slice(0, 2)} />);

    tab('Beta').focus();
    await user.keyboard('{Delete}');
    expect(tab('Alpha')).toHaveFocus();

    await user.keyboard('{Delete}');
    expect(screen.getAllByRole('tab')).toHaveLength(1);
    expect(tab('New chat')).toHaveFocus();
  });

  it('focuses the switcher after closing a tab while the tabs are collapsed', async () => {
    const user = userEvent.setup();
    render(<StatefulTabs initial={tabs.slice(0, 2)} />);
    collapseTabStrip();

    await user.click(screen.getByRole('button', { name: 'Current tab actions' }));
    await user.click(screen.getByRole('menuitem', { name: 'Close tab' }));

    expect(screen.getByRole('button', { name: /Switch conversation tab, 1 open/ })).toHaveFocus();
  });
});

describe('ConversationTabs focus across layout changes', () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('moves focus between the switcher and the active tab as the tabs collapse and expand', async () => {
    const user = userEvent.setup();
    const resize = stubResizeObserver();
    render(<StatefulTabs initial={tabs} />);
    resize();
    collapseTabStrip();
    resize();
    const switcher = screen.getByRole('button', { name: /Switch conversation tab/ });

    await user.click(switcher);
    await user.click(
      within(screen.getByRole('dialog', { name: 'Open conversations' })).getByRole('button', {
        name: /Beta/,
      })
    );
    expect(switcher).toHaveFocus();

    // The destination's layout leaves room for the tabs again.
    expandTabStrip();
    resize();
    expect(tab('Beta')).toHaveFocus();

    collapseTabStrip();
    resize();
    expect(switcher).toHaveFocus();
  });

  it('leaves focus alone once the user clicks or focuses something else', async () => {
    const user = userEvent.setup();
    const resize = stubResizeObserver();
    render(
      <>
        <StatefulTabs initial={tabs} />
        <button type="button">Elsewhere</button>
      </>
    );
    resize();
    const switcher = screen.getByRole('button', { name: /Switch conversation tab/ });

    tab('Alpha').focus();
    await user.click(document.body);
    collapseTabStrip();
    resize();
    expect(switcher).not.toHaveFocus();

    switcher.focus();
    screen.getByRole('button', { name: 'Elsewhere' }).focus();
    expandTabStrip();
    resize();
    expect(screen.getByRole('button', { name: 'Elsewhere' })).toHaveFocus();
  });
});

describe('adjacentConversationTabId', () => {
  it('wraps around both ends', () => {
    expect(adjacentConversationTabId(tabs, 'conv-e', 1)).toBe('conv-a');
    expect(adjacentConversationTabId(tabs, 'conv-a', -1)).toBe('conv-e');
    expect(adjacentConversationTabId(tabs, 'missing', 1)).toBe('conv-b');
    expect(adjacentConversationTabId([], 'conv-a', 1)).toBeUndefined();
  });
});
