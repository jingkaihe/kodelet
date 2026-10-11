import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { useEffect } from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import apiService from '../../services/api';
import type { WorkspaceTarget } from '../../types';
import TerminalTabs, { getTerminalTabsKey, TERMINAL_TAB_TITLES_CHANNEL } from './TerminalTabs';

const target: WorkspaceTarget = {
  kind: 'runner',
  runnerId: 'runner-1',
  conversationId: 'conversation-1',
  cwd: '/workspace/project',
};
const unknownDirectory: WorkspaceTarget = { kind: 'local' };

const PopOutReporter = ({
  active,
  onChange,
}: {
  active: boolean;
  onChange: (active: boolean) => void;
}) => {
  useEffect(() => onChange(active), [active, onChange]);
  return null;
};

const Tabs = ({
  popOutActive = false,
  directory = '/workspace/build',
  title = 'nvim main.go',
  workspace = target,
  sessionId,
  homeDir,
}: {
  popOutActive?: boolean;
  directory?: string;
  title?: string;
  workspace?: WorkspaceTarget;
  sessionId?: string;
  homeDir?: string;
}) => (
  <TerminalTabs key={getTerminalTabsKey(workspace)} open target={workspace}>
    {({
      terminalId,
      connectingEnabled,
      tabBar,
      onPopOutChange,
      onDirectoryChange,
      onTitleChange,
      onSessionReady,
    }) => (
      <>
        {tabBar(popOutActive)}
        <PopOutReporter active={popOutActive} onChange={onPopOutChange} />
        <div
          data-connecting-enabled={String(connectingEnabled)}
          data-terminal-id={terminalId}
          data-testid="session"
        />
        <button onClick={() => onDirectoryChange(directory, homeDir)} type="button">
          Update directory
        </button>
        <button onClick={() => onTitleChange(title, homeDir)} type="button">
          Update title
        </button>
        <button
          onClick={() => {
            onSessionReady({
              type: 'ready',
              cwd: directory,
              name: 'bash',
              git: true,
              pid: 123,
              sessionId,
            });
            onDirectoryChange(directory, homeDir);
          }}
          type="button"
        >
          Session ready
        </button>
      </>
    )}
  </TerminalTabs>
);

const activeId = () => screen.getByTestId('session').getAttribute('data-terminal-id');
const tabNames = () => screen.getAllByRole('tab').map((tab) => tab.textContent);
const addTab = () => fireEvent.click(screen.getByRole('button', { name: 'New terminal tab' }));
const savedText = () =>
  Array.from({ length: window.localStorage.length }, (_, index) => {
    const key = window.localStorage.key(index) ?? '';
    return `${key}=${window.localStorage.getItem(key)}`;
  }).join('\n');

const renameTab = (name: string, key = 'Enter') => {
  fireEvent.click(screen.getByRole('button', { name: 'Rename terminal tab' }));
  const input = screen.getByRole('textbox', { name: 'Terminal tab name' });
  fireEvent.change(input, { target: { value: name } });
  fireEvent.keyDown(input, { key });
};

const confirmClose = async (name: string) => {
  fireEvent.click(screen.getByRole('button', { name: `Close ${name}` }));
  fireEvent.click(
    within(screen.getByRole('dialog', { name: 'Close tab?' })).getByRole('button', {
      name: 'Close',
    })
  );
  await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
};

describe('TerminalTabs', () => {
  beforeEach(() => {
    window.localStorage.clear();
    vi.spyOn(apiService, 'closeTerminal').mockResolvedValue(undefined);
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('uses sequential Shell names while the directory is unknown and preserves tab identity', () => {
    render(<Tabs workspace={unknownDirectory} />);
    expect(activeId()).toBe('');
    expect(screen.getByRole('tab', { name: 'Shell 1' })).toHaveAttribute('aria-selected', 'true');

    addTab();
    const secondId = activeId();
    expect(secondId).toMatch(/^[a-zA-Z0-9_-]+$/);
    addTab();
    const thirdId = activeId();
    expect(thirdId).not.toBe(secondId);
    expect(tabNames()).toEqual(['Shell 1', 'Shell 2', 'Shell 3']);

    fireEvent.click(screen.getByRole('tab', { name: 'Shell 2' }));
    expect(activeId()).toBe(secondId);
    expect(screen.getByRole('tab', { name: 'Shell 3' })).toHaveAttribute('aria-selected', 'false');
    fireEvent.click(screen.getByRole('tab', { name: 'Shell 1' }));
    expect(activeId()).toBe('');
    fireEvent.click(screen.getByRole('tab', { name: 'Shell 3' }));
    expect(activeId()).toBe(thirdId);
    expect(apiService.closeTerminal).not.toHaveBeenCalled();
  });

  it('names tabs from the full target directory without visible shortcut badges', () => {
    render(<Tabs />);
    addTab();
    expect(tabNames()).toEqual(['/workspace/project', '/workspace/project']);
    const tabs = screen.getAllByRole('tab', { name: '/workspace/project' });
    tabs.forEach((tab, index) => {
      expect(tab).toHaveAttribute('aria-keyshortcuts', `Alt+${index + 1}`);
      expect(tab).toHaveTextContent(/^\/workspace\/project$/);
    });
  });

  it('saves a trimmed name with Enter and cancels edits with Escape', async () => {
    const user = userEvent.setup();
    render(<Tabs />);
    await user.dblClick(screen.getByRole('tab', { name: '/workspace/project' }));
    const input = screen.getByRole('textbox', { name: 'Terminal tab name' });
    expect(input).toHaveFocus();
    await user.clear(input);
    await user.type(input, '  Build logs  {Enter}');
    expect(screen.getByRole('tab', { name: 'Build logs' })).toBeInTheDocument();
    expect(screen.queryByRole('textbox')).not.toBeInTheDocument();

    await user.click(screen.getByRole('button', { name: 'Rename terminal tab' }));
    await user.clear(screen.getByRole('textbox'));
    await user.type(screen.getByRole('textbox'), 'Discard this{Escape}');
    expect(screen.getByRole('tab', { name: 'Build logs' })).toBeInTheDocument();
    expect(screen.queryByRole('tab', { name: 'Discard this' })).not.toBeInTheDocument();
    expect(activeId()).toBe('');
  });

  it('uses the full directory without replacing custom names and restores it on empty rename', () => {
    const { rerender } = render(<Tabs />);
    fireEvent.click(screen.getByRole('button', { name: 'Update directory' }));
    expect(screen.getByRole('tab', { name: '/workspace/build' })).toBeInTheDocument();
    renameTab('Server');
    rerender(<Tabs directory="/workspace/server logs/" />);
    fireEvent.click(screen.getByRole('button', { name: 'Update directory' }));
    expect(screen.getByRole('tab', { name: 'Server' })).toBeInTheDocument();

    renameTab('  ');
    expect(screen.getByRole('tab', { name: '/workspace/server logs' })).toBeInTheDocument();
    expect(activeId()).toBe('');
  });

  it('replaces the fallback name when the directory becomes available, only for that tab', () => {
    render(<Tabs workspace={unknownDirectory} directory="/resolved/workspace/project" />);
    addTab();
    expect(tabNames()).toEqual(['Shell 1', 'Shell 2']);
    fireEvent.click(screen.getByRole('button', { name: 'Update directory' }));
    expect(tabNames()).toEqual(['Shell 1', '/resolved/workspace/project']);
  });

  it.each([
    ['/home/user', '/home/user', '~'],
    ['/home/user/workspace/kodelet', '/home/user', '~/workspace/kodelet'],
    ['/Users/user/Workspace/kodelet/', '/Users/user/', '~/Workspace/kodelet'],
    ['/home/user-other/project', '/home/user', '/home/user-other/project'],
    ['/srv/project', '/home/user', '/srv/project'],
    ['/', '/home/user', '/'],
    ['/home/user/project', undefined, '/home/user/project'],
    ['/home/user/my project/café', '/home/user', '~/my project/café'],
  ])('displays directory %s relative only to the runner home %s', (directory, homeDir, expected) => {
    render(<Tabs directory={directory} homeDir={homeDir} />);
    fireEvent.click(screen.getByRole('button', { name: 'Update directory' }));
    expect(tabNames()).toEqual([expected]);
    renameTab('Custom');
    fireEvent.click(screen.getByRole('button', { name: 'Update directory' }));
    expect(tabNames()).toEqual(['Custom']);
    renameTab('');
    expect(tabNames()).toEqual([expected]);
  });

  it('abbreviates absolute shell title paths but leaves command arguments unchanged', () => {
    const { rerender } = render(
      <Tabs title="user@runner:/home/user/workspace/kodelet" homeDir="/home/user" />
    );
    fireEvent.click(screen.getByRole('button', { name: 'Update title' }));
    expect(tabNames()).toEqual(['~/workspace/kodelet']);
    rerender(<Tabs title="nvim /home/user/file.txt" homeDir="/home/user" />);
    fireEvent.click(screen.getByRole('button', { name: 'Update title' }));
    expect(tabNames()).toEqual(['nvim /home/user/file.txt']);
  });

  it.each([
    ['user@runner:~/workspace/kodelet', '~/workspace/kodelet'],
    ['user@runner:~', '~'],
    ['user@runner:/tmp/project', '/tmp/project'],
    ['user@runner:~/my project/café:logs', '~/my project/café:logs'],
    ['  user@runner:~/workspace  ', '~/workspace'],
    ['user@ubuntu: ~/workspace', '~/workspace'],
    ['(chroot)user@debian: /srv', '/srv'],
    ['nvim main.go', 'nvim main.go'],
    ['ssh user@runner:/tmp', 'ssh user@runner:/tmp'],
    ['user@runner:pytest', 'user@runner:pytest'],
  ])('shows shell paths without a user/host prefix, preserving program titles: %s', (title, expected) => {
    render(<Tabs title={title} />);
    fireEvent.click(screen.getByRole('button', { name: 'Update title' }));
    expect(tabNames()).toEqual([expected]);
  });

  it('does not strip user/host prefixes from manually named tabs', () => {
    render(<Tabs title="user@runner:~/workspace" />);
    renameTab('user@runner:~/custom');
    fireEvent.click(screen.getByRole('button', { name: 'Update title' }));
    expect(tabNames()).toEqual(['user@runner:~/custom']);
    renameTab('');
    expect(tabNames()).toEqual(['~/workspace']);
  });

  it.each([
    '/canonical/project',
    target.cwd,
  ])('persists names, selection and IDs after readiness canonicalizes the directory, reopening %s', (cwd) => {
    const { unmount } = render(<Tabs directory="/canonical/project" />);
    addTab();
    const secondId = activeId();
    renameTab('Build');
    fireEvent.click(screen.getByRole('button', { name: 'Session ready' }));

    const canonicalKey = getTerminalTabsKey({ ...target, cwd: '/canonical/project' });
    expect(JSON.parse(window.localStorage.getItem(getTerminalTabsKey(target)) || 'null')).toEqual({
      canonicalKey,
    });
    expect(JSON.parse(window.localStorage.getItem(canonicalKey) || 'null')).toMatchObject({
      activeId: secondId,
      tabs: [{ id: '' }, { id: secondId, directory: '/canonical/project', customTitle: 'Build' }],
    });
    unmount();

    render(<Tabs workspace={{ ...target, cwd }} />);
    expect(tabNames()).toEqual([cwd, 'Build']);
    expect(screen.getByRole('tab', { name: 'Build' })).toHaveAttribute('aria-selected', 'true');
    expect(activeId()).toBe(secondId);
    addTab();
    expect(tabNames()).toEqual([cwd, 'Build', cwd]);
    expect(screen.getAllByRole('tab')[2]).toHaveAttribute('aria-selected', 'true');
    expect(JSON.parse(window.localStorage.getItem(getTerminalTabsKey(target)) || 'null')).toEqual({
      canonicalKey,
    });
  });

  it('keeps different directories in the same conversation isolated when switching targets', () => {
    const directoryA = { ...target, cwd: '/workspace/dirA' };
    const directoryB = { ...target, cwd: '/workspace/dirB' };
    expect(getTerminalTabsKey(directoryA)).not.toBe(getTerminalTabsKey(directoryB));
    const { rerender } = render(<Tabs workspace={directoryA} />);
    addTab();
    const secondIdA = activeId();
    renameTab('Build A');

    rerender(<Tabs workspace={directoryB} />);
    expect(tabNames()).toEqual(['/workspace/dirB']);
    expect(activeId()).toBe('');
    addTab();
    const secondIdB = activeId();
    renameTab('Build B');
    expect(secondIdB).not.toBe(secondIdA);

    rerender(<Tabs workspace={directoryA} />);
    expect(tabNames()).toEqual(['/workspace/dirA', 'Build A']);
    expect(activeId()).toBe(secondIdA);
    rerender(<Tabs workspace={directoryB} />);
    expect(tabNames()).toEqual(['/workspace/dirB', 'Build B']);
    expect(activeId()).toBe(secondIdB);
    expect(apiService.closeTerminal).not.toHaveBeenCalled();
  });

  it('uses the canonical directory when closing a session after readiness', async () => {
    render(<Tabs directory="/canonical/project" />);
    fireEvent.click(screen.getByRole('button', { name: 'Session ready' }));
    await confirmClose('/canonical/project');
    expect(apiService.closeTerminal).toHaveBeenCalledExactlyOnceWith(
      { ...target, cwd: '/canonical/project' },
      '',
      undefined
    );
  });

  it.each([
    false,
    true,
  ])('synchronizes pop-out storage without echoing after canonicalization=%s', (ready) => {
    render(<Tabs directory="/canonical/project" />);
    if (ready) fireEvent.click(screen.getByRole('button', { name: 'Session ready' }));
    const storageKey = getTerminalTabsKey(
      ready ? { ...target, cwd: '/canonical/project' } : target
    );
    const setItem = vi.spyOn(Storage.prototype, 'setItem');
    const nextState = {
      tabs: [
        { id: '', number: 1, directory: '/workspace/project', customTitle: '' },
        { id: 'popout-tab', number: 2, customTitle: 'Remote build' },
      ],
      activeId: 'popout-tab',
      nextNumber: 3,
    };
    const receive = (key: string, newValue: string) => {
      act(() => {
        window.dispatchEvent(new StorageEvent('storage', { key, newValue }));
      });
    };

    receive(getTerminalTabsKey({ ...target, cwd: '/another/project' }), JSON.stringify(nextState));
    expect(tabNames()).toEqual([ready ? '/canonical/project' : '/workspace/project']);
    receive(storageKey, JSON.stringify(nextState));
    expect(tabNames()).toEqual(['/workspace/project', 'Remote build']);
    expect(activeId()).toBe('popout-tab');
    expect(setItem).not.toHaveBeenCalled();
    receive(storageKey, '{invalid json');
    expect(activeId()).toBe('popout-tab');
  });

  it('shows program titles without saving them, and saves directories', () => {
    render(<Tabs title="mysql -uroot -pS3cret app" />);
    fireEvent.click(screen.getByRole('button', { name: 'Update title' }));
    expect(tabNames()).toEqual(['mysql -uroot -pS3cret app']);
    addTab();
    renameTab('Build');
    expect(savedText()).not.toContain('S3cret');

    fireEvent.click(screen.getByRole('tab', { name: 'mysql -uroot -pS3cret app' }));
    fireEvent.click(screen.getByRole('button', { name: 'Update directory' }));
    expect(tabNames()).toEqual(['/workspace/build', 'Build']);
    expect(savedText()).toContain('"directory":"/workspace/build"');
  });

  it('removes program titles saved by earlier versions', () => {
    window.localStorage.setItem(
      getTerminalTabsKey(target),
      JSON.stringify({
        tabs: [{ id: '', number: 1, title: 'mysql -pS3cret', customTitle: 'Database' }],
        activeId: '',
        nextNumber: 2,
      })
    );
    render(<Tabs />);
    expect(tabNames()).toEqual(['Database']);
    expect(savedText()).not.toContain('S3cret');
    expect(savedText()).toContain('"customTitle":"Database"');
  });

  it('shares program titles with other windows for the same tabs only', async () => {
    const otherWindow = new BroadcastChannel(TERMINAL_TAB_TITLES_CHANNEL);
    const received: unknown[] = [];
    otherWindow.onmessage = (event) => received.push(event.data);
    try {
      render(<Tabs />);
      fireEvent.click(screen.getByRole('button', { name: 'Update title' }));
      await waitFor(() =>
        expect(received).toContainEqual({
          key: getTerminalTabsKey(target),
          id: '',
          title: 'nvim main.go',
        })
      );

      otherWindow.postMessage({
        key: getTerminalTabsKey({ ...target, cwd: '/elsewhere' }),
        id: '',
        title: 'ignored',
      });
      otherWindow.postMessage({ key: getTerminalTabsKey(target), id: '', title: 'cargo build' });
      await waitFor(() => expect(tabNames()).toEqual(['cargo build']));
      expect(savedText()).not.toContain('cargo build');
    } finally {
      otherWindow.close();
    }
  });

  describe('saved state cleanup', () => {
    const day = 24 * 60 * 60 * 1000;
    const keyFor = (cwd: string) => getTerminalTabsKey({ ...target, cwd });
    const save = (key: string, updatedAt?: number) =>
      window.localStorage.setItem(
        key,
        JSON.stringify({
          tabs: [{ id: '', number: 1, customTitle: '' }],
          activeId: '',
          nextNumber: 2,
          ...(updatedAt === undefined ? {} : { updatedAt }),
        })
      );
    const savedAt = (key: string) =>
      JSON.parse(window.localStorage.getItem(key) || 'null')?.updatedAt;

    it('removes unused, broken and orphaned entries but keeps current and recent tabs', () => {
      const now = Date.now();
      save(getTerminalTabsKey(target), now - 40 * day);
      save(keyFor('/expired'), now - 31 * day);
      window.localStorage.setItem(
        keyFor('/expired-alias'),
        JSON.stringify({ canonicalKey: keyFor('/expired') })
      );
      save(keyFor('/recent'), now - day);
      window.localStorage.setItem(
        keyFor('/recent-alias'),
        JSON.stringify({ canonicalKey: keyFor('/recent') })
      );
      save(keyFor('/legacy'));
      window.localStorage.setItem(keyFor('/broken'), '{broken');
      window.localStorage.setItem('unrelated', 'kept');

      render(<Tabs />);

      for (const cwd of ['/expired', '/expired-alias', '/broken']) {
        expect(window.localStorage.getItem(keyFor(cwd))).toBeNull();
      }
      expect(window.localStorage.getItem(keyFor('/recent-alias'))).not.toBeNull();
      expect(savedAt(keyFor('/recent'))).toBe(now - day);
      expect(savedAt(keyFor('/legacy'))).toBeGreaterThanOrEqual(now);
      expect(savedAt(getTerminalTabsKey(target))).toBeGreaterThanOrEqual(now);
      expect(window.localStorage.getItem('unrelated')).toBe('kept');
    });

    it('keeps only the 50 most recently used entries', () => {
      const now = Date.now();
      for (let index = 0; index < 52; index++) save(keyFor(`/project-${index}`), now - index);
      render(<Tabs />);
      expect(window.localStorage.getItem(keyFor('/project-49'))).not.toBeNull();
      expect(window.localStorage.getItem(keyFor('/project-50'))).toBeNull();
      expect(window.localStorage.getItem(keyFor('/project-51'))).toBeNull();
    });

    it('stamps every save with its time', () => {
      const now = Date.now();
      render(<Tabs />);
      addTab();
      expect(savedAt(getTerminalTabsKey(target))).toBeGreaterThanOrEqual(now);
    });
  });

  it.each([
    '{broken',
    'null',
    '{"tabs":[]}',
  ])('recovers safely from invalid persisted state: %s', (stored) => {
    window.localStorage.setItem(getTerminalTabsKey(target), stored);
    render(<Tabs />);
    expect(tabNames()).toEqual(['/workspace/project']);
    addTab();
    expect(tabNames()).toEqual(['/workspace/project', '/workspace/project']);
  });

  it('cancels tab close without terminating or changing the active shell', () => {
    render(<Tabs />);
    addTab();
    const secondId = activeId();
    renameTab('Build');
    fireEvent.click(screen.getByRole('button', { name: 'Close Build' }));
    const dialog = screen.getByRole('dialog', { name: 'Close tab?' });
    expect(dialog).toHaveTextContent(
      'Closing this tab will stop the shell and any running processes.'
    );
    expect(dialog).not.toHaveTextContent('Build');
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(apiService.closeTerminal).not.toHaveBeenCalled();
    expect(activeId()).toBe(secondId);
    expect(tabNames()).toEqual(['/workspace/project', 'Build']);
  });

  it('closes the selected session, selects a neighbor and never reuses a fallback number', async () => {
    render(<Tabs workspace={unknownDirectory} />);
    addTab();
    const secondId = activeId();
    addTab();
    const thirdId = activeId();
    fireEvent.click(screen.getByRole('tab', { name: 'Shell 2' }));
    await confirmClose('Shell 2');
    expect(apiService.closeTerminal).toHaveBeenCalledExactlyOnceWith(
      unknownDirectory,
      secondId,
      undefined
    );
    expect(tabNames()).toEqual(['Shell 1', 'Shell 3']);
    expect(screen.getByRole('tab', { name: 'Shell 3' })).toHaveAttribute(
      'aria-keyshortcuts',
      'Alt+2'
    );
    expect(activeId()).toBe(thirdId);
    addTab();
    expect(tabNames()).toEqual(['Shell 1', 'Shell 3', 'Shell 4']);
    expect(screen.getByRole('tab', { name: 'Shell 4' })).toHaveAttribute(
      'aria-keyshortcuts',
      'Alt+3'
    );
  });

  it('closes an inactive tab without replacing the active session', async () => {
    render(<Tabs />);
    addTab();
    const secondId = activeId();
    renameTab('Build');
    await confirmClose('/workspace/project');
    expect(apiService.closeTerminal).toHaveBeenCalledExactlyOnceWith(target, '', undefined);
    expect(activeId()).toBe(secondId);
    expect(tabNames()).toEqual(['Build']);
  });

  it('retains exact session identity and canonical scope for close after reload and directory changes', async () => {
    const { rerender, unmount } = render(
      <Tabs directory="/canonical/project" sessionId="terminal-original" />
    );
    fireEvent.click(screen.getByRole('button', { name: 'Session ready' }));
    rerender(<Tabs directory="/other/path" sessionId="terminal-original" />);
    fireEvent.click(screen.getByRole('button', { name: 'Update directory' }));
    expect(tabNames()).toEqual(['/other/path']);
    unmount();
    render(<Tabs />);
    await confirmClose('/other/path');
    expect(apiService.closeTerminal).toHaveBeenCalledExactlyOnceWith(
      { ...target, cwd: '/canonical/project' },
      '',
      'terminal-original'
    );
  });

  it('disables reconnection while closing and preserves the tab on failure for retry', async () => {
    let rejectClose: (reason: Error) => void = () => {};
    vi.mocked(apiService.closeTerminal).mockImplementationOnce(
      () =>
        new Promise<void>((_, reject) => {
          rejectClose = reject;
        })
    );
    render(<Tabs />);
    fireEvent.click(screen.getByRole('button', { name: 'Close /workspace/project' }));
    fireEvent.click(screen.getByRole('button', { name: 'Close' }));
    expect(screen.getByTestId('session')).toHaveAttribute('data-connecting-enabled', 'false');
    expect(screen.getByRole('button', { name: 'New terminal tab' })).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Cancel' })).toBeDisabled();

    await act(async () => {
      rejectClose(new Error('Runner is offline'));
    });
    expect(screen.getByRole('dialog')).toHaveTextContent('Runner is offline');
    expect(tabNames()).toEqual(['/workspace/project']);
    expect(activeId()).toBe('');
    expect(screen.getByTestId('session')).toHaveAttribute('data-connecting-enabled', 'true');
    fireEvent.click(screen.getByRole('button', { name: 'Close' }));
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
    expect(apiService.closeTerminal).toHaveBeenCalledTimes(2);
  });

  it('persists an empty state after closing the last tab and starts a fresh identity on add', async () => {
    const { unmount } = render(<Tabs />);
    await confirmClose('/workspace/project');
    expect(screen.queryByRole('tab')).not.toBeInTheDocument();
    expect(screen.queryByTestId('session')).not.toBeInTheDocument();
    expect(screen.getByText(/No terminal tabs/)).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Rename terminal tab' })).toBeDisabled();
    unmount();

    render(<Tabs />);
    expect(screen.queryByRole('tab')).not.toBeInTheDocument();
    addTab();
    expect(tabNames()).toEqual(['/workspace/project']);
    expect(activeId()).not.toBe('');
  });

  it('supports accessible arrow, Home and End navigation', async () => {
    render(<Tabs workspace={unknownDirectory} />);
    addTab();
    addTab();
    const thirdId = activeId();
    fireEvent.keyDown(screen.getByRole('tab', { name: 'Shell 3' }), { key: 'ArrowRight' });
    await waitFor(() => expect(screen.getByRole('tab', { name: 'Shell 1' })).toHaveFocus());
    expect(activeId()).toBe('');
    fireEvent.keyDown(screen.getByRole('tab', { name: 'Shell 1' }), { key: 'ArrowLeft' });
    expect(activeId()).toBe(thirdId);
    fireEvent.keyDown(screen.getByRole('tab', { name: 'Shell 3' }), { key: 'Home' });
    await waitFor(() => expect(screen.getByRole('tab', { name: 'Shell 1' })).toHaveFocus());
    fireEvent.keyDown(screen.getByRole('tab', { name: 'Shell 1' }), { key: 'End' });
    await waitFor(() => expect(screen.getByRole('tab', { name: 'Shell 3' })).toHaveFocus());
  });

  it('persists dragged tab order and identity while keeping Alt shortcuts positional and invisible', () => {
    const { unmount } = render(<Tabs />);
    addTab();
    addTab();
    const thirdId = activeId();
    renameTab('Server');
    fireEvent.dragStart(screen.getByRole('tab', { name: 'Server' }), {
      dataTransfer: { setData: vi.fn() },
    });
    fireEvent.drop(screen.getAllByRole('tab')[0]);
    expect(tabNames()).toEqual(['Server', '/workspace/project', '/workspace/project']);
    expect(screen.getByRole('tab', { name: 'Server' })).toHaveAttribute(
      'aria-keyshortcuts',
      'Alt+1'
    );
    expect(screen.getByRole('tab', { name: 'Server' })).toHaveTextContent(/^Server$/);
    fireEvent.keyDown(screen.getByTestId('session'), { key: '2', altKey: true });
    expect(activeId()).toBe('');
    fireEvent.keyDown(screen.getByTestId('session'), { key: '1', altKey: true });
    expect(activeId()).toBe(thirdId);
    unmount();
    render(<Tabs />);
    expect(tabNames()).toEqual(['Server', '/workspace/project', '/workspace/project']);
    expect(activeId()).toBe(thirdId);
  });

  it('handles tab shortcuts without consuming ordinary terminal input', () => {
    render(<Tabs />);
    addTab();
    const secondId = activeId();
    expect(
      fireEvent.keyDown(screen.getByTestId('session'), {
        key: 'ArrowLeft',
        ctrlKey: true,
        altKey: true,
      })
    ).toBe(true);
    expect(
      fireEvent.keyDown(screen.getByTestId('session'), { key: '1', ctrlKey: true, altKey: true })
    ).toBe(true);
    for (const key of ['1', 'ArrowLeft']) {
      expect(fireEvent.keyDown(screen.getByTestId('session'), { key })).toBe(true);
    }
    expect(fireEvent.keyDown(screen.getByTestId('session'), { key: '8', altKey: true })).toBe(true);
    expect(activeId()).toBe(secondId);
    expect(tabNames()).toHaveLength(2);
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  });

  it('handles macOS Option-transformed digit keys without showing shortcut labels', () => {
    render(<Tabs />);
    addTab();
    const secondId = activeId();
    expect(
      fireEvent.keyDown(screen.getByTestId('session'), { key: '¡', code: 'Digit1', altKey: true })
    ).toBe(false);
    expect(activeId()).toBe('');
    fireEvent.keyDown(screen.getByTestId('session'), { key: '™', code: 'Digit2', altKey: true });
    expect(activeId()).toBe(secondId);
  });

  it('selects all eight positions with Alt+1 through Alt+8 and enforces the tab limit', () => {
    render(<Tabs />);
    const ids = [activeId()];
    for (let index = 1; index < 8; index++) {
      addTab();
      ids.push(activeId());
    }
    ids.forEach((id, index) => {
      const key = String(index + 1);
      expect(fireEvent.keyDown(screen.getByTestId('session'), { key, altKey: true })).toBe(false);
      expect(activeId()).toBe(id);
      expect(screen.getAllByRole('tab')[index]).toHaveAttribute('aria-keyshortcuts', `Alt+${key}`);
      expect(screen.getAllByRole('tab')[index]).toHaveAttribute('aria-selected', 'true');
    });
    expect(tabNames()).toHaveLength(8);
    expect(screen.getByRole('button', { name: 'New terminal tab' })).toBeDisabled();
    addTab();
    expect(tabNames()).toHaveLength(8);
  });

  it('disables tab mutations and shortcuts while a pop-out owns the terminal', () => {
    const { rerender } = render(<Tabs />);
    addTab();
    const secondId = activeId();
    renameTab('Build');
    rerender(<Tabs popOutActive />);
    for (const tab of screen.getAllByRole('tab')) expect(tab).toBeDisabled();
    for (const name of ['New terminal tab', 'Rename terminal tab', 'Close /workspace/project']) {
      expect(screen.getByRole('button', { name })).toBeDisabled();
    }
    fireEvent.click(screen.getByRole('tab', { name: '/workspace/project' }));
    addTab();
    fireEvent.click(screen.getByRole('button', { name: 'Close /workspace/project' }));
    fireEvent.keyDown(screen.getByTestId('session'), { key: '1', altKey: true });
    expect(tabNames()).toHaveLength(2);
    expect(activeId()).toBe(secondId);
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(apiService.closeTerminal).not.toHaveBeenCalled();

    rerender(<Tabs />);
    expect(screen.getByRole('button', { name: 'New terminal tab' })).toBeEnabled();
    addTab();
    expect(tabNames()).toHaveLength(3);
  });
});
