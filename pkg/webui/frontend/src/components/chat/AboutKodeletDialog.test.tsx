import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { Runner, ServerStatus } from '../../types';
import AboutKodeletDialog from './AboutKodeletDialog';

const mockGetServerStatus = vi.fn();

vi.mock('../../services/api', () => ({
  default: {
    getServerStatus: (...args: unknown[]) => mockGetServerStatus(...args),
  },
}));

const serverStatus = {
  version: '1.2.3',
  gitCommit: 'abcdef1234567890abcdef1234567890abcdef1234',
  buildTime: '2026-09-27T23:45:00Z',
  apiReady: true,
  embeddedRunner: { enabled: true, ready: true, runnerId: 'runner-1' },
} satisfies ServerStatus;

const makeRunner = (overrides: Partial<Runner> = {}): Runner => ({
  id: 'runner-1',
  displayName: 'Development runner',
  host: {
    instanceId: 'host-1',
    hostname: 'worker',
    os: 'linux',
    arch: 'amd64',
  },
  workspace: { path: '/workspace/kodelet', name: 'kodelet workspace' },
  kodeletVersion: '1.2.2',
  manifestChanged: false,
  status: 'idle',
  connected: true,
  workspaceGitDiff: true,
  workspaceTerminal: true,
  workspaceBrowser: true,
  workspaceDiscovery: true,
  workspaceCwd: true,
  generation: 1,
  ...overrides,
});

const capabilities = [
  'Git diffs',
  'Terminal',
  'Browser',
  'Slash commands',
  'Working directory',
] as const;

const expectCapability = (label: string, state: string) => {
  const definition = screen.getByText(label, { selector: 'dt' }).nextElementSibling;
  expect(definition?.tagName).toBe('DD');
  expect(definition).toHaveTextContent(state);
};

describe('AboutKodeletDialog', () => {
  beforeEach(() => {
    mockGetServerStatus.mockReset().mockResolvedValue(serverStatus);
  });

  it('loads the server version separately from the runner version and shows live capabilities', async () => {
    const onClose = vi.fn();
    const { rerender } = render(
      <AboutKodeletDialog onClose={onClose} runner={makeRunner()} terminalAuthorized />
    );

    expect(screen.getByRole('dialog', { name: 'About Kodelet' })).toBeInTheDocument();
    expect(screen.queryByRole('heading', { name: 'About Kodelet' })).not.toBeInTheDocument();
    expect(screen.getByRole('img', { name: 'Kodelet' }).closest('header')).toContainElement(
      screen.getByRole('button', { name: 'Close about Kodelet' })
    );
    expect(await screen.findByText('1.2.3', { selector: 'dd' })).toBeInTheDocument();
    expect(mockGetServerStatus).toHaveBeenCalledOnce();
    const buildCommit = screen.getByText('Build commit', { selector: 'dt' }).nextElementSibling;
    expect(buildCommit?.textContent).toBe(serverStatus.gitCommit.slice(0, 7));
    expect(buildCommit).toHaveAttribute('title', serverStatus.gitCommit);
    const buildDate = screen.getByText('Build date', { selector: 'dt' }).nextElementSibling;
    const date = new Date(serverStatus.buildTime);
    expect(buildDate).toHaveTextContent(
      date.toLocaleDateString(undefined, {
        year: 'numeric',
        month: 'short',
        day: 'numeric',
        timeZone: 'UTC',
      })
    );
    expect(buildDate?.querySelector('time')).toHaveAttribute('datetime', date.toISOString());
    expect(buildDate?.querySelector('time')).toHaveAttribute('title', serverStatus.buildTime);
    expect(screen.getByText('1.2.2', { selector: 'dd' })).toBeInTheDocument();
    expect(screen.getByText('Development runner')).toBeInTheDocument();
    for (const capability of capabilities) {
      expectCapability(capability, 'Enabled');
    }

    rerender(
      <AboutKodeletDialog
        onClose={onClose}
        runner={makeRunner({ status: 'busy' })}
        terminalAuthorized
      />
    );

    for (const capability of capabilities) {
      expectCapability(capability, 'Enabled');
    }
    expect(mockGetServerStatus).toHaveBeenCalledOnce();
  });

  it('shows Unknown for missing or unknown build metadata and unparseable dates', async () => {
    for (const metadata of [
      { gitCommit: undefined, buildTime: undefined },
      { gitCommit: 'unknown', buildTime: 'unknown' },
      { gitCommit: undefined, buildTime: 'not a date' },
    ]) {
      mockGetServerStatus.mockResolvedValueOnce({ ...serverStatus, ...metadata });
      const { unmount } = render(<AboutKodeletDialog onClose={vi.fn()} terminalAuthorized />);

      await screen.findByText('1.2.3', { selector: 'dd' });
      for (const label of ['Build commit', 'Build date']) {
        const definition = screen.getByText(label, { selector: 'dt' }).nextElementSibling;
        expect(definition).toHaveTextContent(/^Unknown$/);
        expect(definition?.querySelector('time')).toBeNull();
      }
      unmount();
    }
  });

  it('requires terminal permission for both terminal and browser capabilities', async () => {
    const onClose = vi.fn();
    const runner = makeRunner();
    const { rerender } = render(
      <AboutKodeletDialog onClose={onClose} runner={runner} terminalAuthorized={false} />
    );

    await screen.findByText('1.2.3', { selector: 'dd' });
    for (const capability of capabilities) {
      expectCapability(
        capability,
        capability === 'Terminal' || capability === 'Browser' ? 'Not enabled' : 'Enabled'
      );
    }

    rerender(<AboutKodeletDialog onClose={onClose} runner={runner} terminalAuthorized />);

    expectCapability('Terminal', 'Enabled');
    expectCapability('Browser', 'Enabled');
  });

  it('treats false and missing flags as not enabled and falls back to workspace name and unknown version', async () => {
    render(
      <AboutKodeletDialog
        onClose={vi.fn()}
        runner={makeRunner({
          displayName: undefined,
          kodeletVersion: undefined,
          workspaceGitDiff: false,
          workspaceTerminal: undefined,
          workspaceBrowser: false,
          workspaceDiscovery: undefined,
          workspaceCwd: false,
        })}
        terminalAuthorized
      />
    );

    await screen.findByText('1.2.3', { selector: 'dd' });
    expect(screen.getByText('kodelet workspace')).toBeInTheDocument();
    expect(screen.getByText('Unknown', { selector: 'dd' })).toBeInTheDocument();
    for (const capability of capabilities) {
      expectCapability(capability, 'Not enabled');
    }
  });

  it('shows unavailable capabilities when no runner is selected', async () => {
    render(<AboutKodeletDialog onClose={vi.fn()} terminalAuthorized />);

    await screen.findByText('1.2.3', { selector: 'dd' });
    for (const capability of capabilities) {
      expectCapability(capability, 'Unavailable');
    }
  });

  it.each([
    { label: 'disconnected', connected: false, status: 'idle' as const },
    { label: 'offline', connected: false, status: 'offline' as const },
    { label: 'incompatible', connected: true, status: 'incompatible' as const },
  ])('shows unavailable capabilities for a $label runner', async ({ connected, status }) => {
    render(
      <AboutKodeletDialog
        onClose={vi.fn()}
        runner={makeRunner({ connected, status })}
        terminalAuthorized
      />
    );

    await screen.findByText('1.2.3', { selector: 'dd' });
    for (const capability of capabilities) {
      expectCapability(capability, 'Unavailable');
    }
  });

  it('shows loading while the server version is pending, then replaces it with the version', async () => {
    let resolveStatus!: (status: ServerStatus) => void;
    mockGetServerStatus.mockReturnValueOnce(
      new Promise<ServerStatus>((resolve) => {
        resolveStatus = resolve;
      })
    );
    render(<AboutKodeletDialog onClose={vi.fn()} terminalAuthorized={false} />);

    expect(screen.getByText('Loading version…')).toBeInTheDocument();
    expect(screen.queryByText('1.2.3')).not.toBeInTheDocument();

    await act(async () => resolveStatus(serverStatus));

    expect(screen.getByText('1.2.3', { selector: 'dd' })).toBeInTheDocument();
    expect(screen.queryByText('Loading version…')).not.toBeInTheDocument();
  });

  it('shows a fetch failure and retries without losing the runner capabilities', async () => {
    const user = userEvent.setup();
    mockGetServerStatus.mockRejectedValueOnce(new Error('Server unavailable'));
    render(<AboutKodeletDialog onClose={vi.fn()} runner={makeRunner()} terminalAuthorized />);

    expect(await screen.findByRole('alert')).toHaveTextContent(
      'Could not load the server version.'
    );
    expectCapability('Git diffs', 'Enabled');
    await user.click(screen.getByRole('button', { name: 'Try again' }));

    expect(await screen.findByText('1.2.3', { selector: 'dd' })).toBeInTheDocument();
    expect(mockGetServerStatus).toHaveBeenCalledTimes(2);
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Try again' })).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Close about Kodelet' })).toHaveFocus();
  });

  it('focuses the close button, traps Tab in both directions and dismisses with Escape', async () => {
    const user = userEvent.setup();
    const onClose = vi.fn();
    render(<AboutKodeletDialog onClose={onClose} terminalAuthorized={false} />);

    const close = screen.getByRole('button', { name: 'Close about Kodelet' });
    await waitFor(() => expect(close).toHaveFocus());
    await user.tab();
    expect(close).toHaveFocus();
    await user.tab({ shift: true });
    expect(close).toHaveFocus();
    await user.keyboard('{Escape}');
    expect(onClose).toHaveBeenCalledOnce();
  });

  it('wraps keyboard focus between the close and retry buttons after a fetch failure', async () => {
    const user = userEvent.setup();
    mockGetServerStatus.mockRejectedValueOnce(new Error('Server unavailable'));
    render(<AboutKodeletDialog onClose={vi.fn()} terminalAuthorized={false} />);

    const retry = await screen.findByRole('button', { name: 'Try again' });
    const close = screen.getByRole('button', { name: 'Close about Kodelet' });
    await waitFor(() => expect(close).toHaveFocus());
    await user.tab({ shift: true });
    expect(retry).toHaveFocus();
    await user.tab();
    expect(close).toHaveFocus();
    await user.tab();
    expect(retry).toHaveFocus();
  });

  it('dismisses with the close button', async () => {
    const user = userEvent.setup();
    const onClose = vi.fn();
    render(<AboutKodeletDialog onClose={onClose} terminalAuthorized={false} />);

    await user.click(screen.getByRole('button', { name: 'Close about Kodelet' }));

    expect(onClose).toHaveBeenCalledOnce();
  });

  it('restores focus to a connected trigger and removes its keyboard handler on unmount', async () => {
    render(<button type="button">Open about Kodelet</button>);
    const trigger = screen.getByRole('button', { name: 'Open about Kodelet' });
    trigger.focus();
    const onClose = vi.fn();
    const { unmount } = render(<AboutKodeletDialog onClose={onClose} terminalAuthorized={false} />);

    await waitFor(() =>
      expect(screen.getByRole('button', { name: 'Close about Kodelet' })).toHaveFocus()
    );
    unmount();

    await waitFor(() => expect(trigger).toHaveFocus());
    fireEvent.keyDown(trigger, { key: 'Escape' });
    expect(onClose).not.toHaveBeenCalled();
  });

  it('restores focus to the sidebar account trigger when the original menu item unmounts', async () => {
    const accountButton = (
      <button aria-haspopup="menu" className="sidebar-account-trigger" type="button">
        Account menu
      </button>
    );
    const { rerender: rerenderSidebar } = render(
      <>
        {accountButton}
        <button role="menuitem" type="button">
          About Kodelet
        </button>
      </>
    );
    screen.getByRole('menuitem', { name: 'About Kodelet' }).focus();
    const { unmount } = render(<AboutKodeletDialog onClose={vi.fn()} terminalAuthorized={false} />);

    await waitFor(() =>
      expect(screen.getByRole('button', { name: 'Close about Kodelet' })).toHaveFocus()
    );
    rerenderSidebar(accountButton);
    unmount();

    await waitFor(() => expect(screen.getByRole('button', { name: 'Account menu' })).toHaveFocus());
  });
});
