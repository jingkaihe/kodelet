import { act, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import App from './App';
import {
  getTerminalPopOutWindowName,
  readTerminalPopOutRecordForTarget,
  TERMINAL_POP_OUT_STORAGE_KEY,
} from './components/workspace/terminalPopOut';
import TerminalPage from './pages/TerminalPage';
import type { TerminalReadyEvent, WorkspaceTarget } from './types';

const mockGetConversation = vi.fn();
const mockTerminalModal =
  vi.fn<
    (props: {
      cwdLabel: string;
      target: WorkspaceTarget;
      onReady?: (event: TerminalReadyEvent) => void;
    }) => void
  >();

vi.mock('./services/api', () => ({
  default: {
    getConversation: (...args: unknown[]) => mockGetConversation(...args),
  },
}));

vi.mock('./pages/ChatPage', () => ({
  default: () => <div data-testid="chat-page">Chat page</div>,
}));

vi.mock('./pages/UserLoginPage', () => ({
  default: () => <div data-testid="user-login-page">User login page</div>,
}));

vi.mock('./pages/RunnerEnrollmentPage', () => ({
  default: () => <div data-testid="runner-enrollment-page">Runner enrollment page</div>,
}));

vi.mock('./pages/SignedOutPage', () => ({
  default: () => <div data-testid="signed-out-page">Signed out page</div>,
}));

vi.mock('./components/workspace/TerminalModal', () => ({
  default: (props: Parameters<typeof mockTerminalModal>[0]) => {
    mockTerminalModal(props);
    const { cwdLabel, target } = props;
    return (
      <div
        data-conversation-id={target.kind === 'runner' ? target.conversationId : undefined}
        data-cwd-label={cwdLabel}
        data-runner-id={target.kind === 'runner' ? target.runnerId : undefined}
        data-target-kind={target.kind}
        data-testid="terminal-modal"
      >
        Terminal
      </div>
    );
  },
}));

beforeEach(() => {
  mockGetConversation.mockReset();
  mockTerminalModal.mockReset();
  mockGetConversation.mockResolvedValue({
    id: 'conv-123',
    runnerId: 'runner-1',
    cwd: '/runner/project',
  });
});

afterEach(() => {
  vi.unstubAllGlobals();
  window.localStorage.removeItem(TERMINAL_POP_OUT_STORAGE_KEY);
  window.sessionStorage.clear();
  window.history.replaceState({}, '', '/');
  window.name = '';
  document.documentElement.classList.remove('terminal-popout-active');
  document.body.classList.remove('terminal-popout-active');
});

describe('App', () => {
  it('renders without crashing', async () => {
    const { container } = render(<App />);

    await waitFor(() => {
      expect(container.querySelector('.h-full')).toBeInTheDocument();
    });
  });

  it('provides base application structure', async () => {
    const { container } = render(<App />);

    await waitFor(() => {
      const wrapper = container.firstElementChild;
      expect(wrapper).toHaveClass('h-full', 'min-h-0', 'overflow-hidden');
    });
  });

  it('routes the device sign-in approval page', async () => {
    window.history.replaceState({}, '', '/auth/device');
    render(<App />);

    expect(await screen.findByTestId('user-login-page')).toBeInTheDocument();
  });

  it('routes the runner enrollment approval page', async () => {
    window.history.replaceState({}, '', '/runner/enroll');
    render(<App />);

    expect(await screen.findByTestId('runner-enrollment-page')).toBeInTheDocument();
  });

  it('routes the public signed-out page', async () => {
    window.history.replaceState({}, '', '/auth/signed-out');
    render(<App />);

    expect(await screen.findByTestId('signed-out-page')).toBeInTheDocument();
  });

  it('tracks the mobile visual viewport', async () => {
    const viewportEvents = new EventTarget();
    const visualViewport = {
      width: 393,
      height: 512,
      offsetLeft: 0,
      offsetTop: 18,
      scale: 1,
      addEventListener: viewportEvents.addEventListener.bind(viewportEvents),
      removeEventListener: viewportEvents.removeEventListener.bind(viewportEvents),
    };
    vi.stubGlobal('visualViewport', visualViewport);

    const { container } = render(<App />);
    const app = container.firstElementChild;

    await waitFor(() => {
      expect(app).toHaveStyle({
        '--app-viewport-top': '18px',
        '--app-viewport-left': '0px',
        '--app-viewport-width': '393px',
        '--app-viewport-height': '512px',
      });
    });

    visualViewport.height = 306;
    visualViewport.offsetTop = 24;
    viewportEvents.dispatchEvent(new Event('resize'));

    await waitFor(() => {
      expect(app).toHaveStyle({
        '--app-viewport-top': '24px',
        '--app-viewport-height': '306px',
      });
    });
  });

  it('leaves the layout viewport intact during pinch zoom', async () => {
    const viewportEvents = new EventTarget();
    const visualViewport = {
      width: 240,
      height: 360,
      offsetLeft: 70,
      offsetTop: 120,
      scale: 1.6,
      addEventListener: viewportEvents.addEventListener.bind(viewportEvents),
      removeEventListener: viewportEvents.removeEventListener.bind(viewportEvents),
    };
    vi.stubGlobal('visualViewport', visualViewport);
    vi.stubGlobal('innerWidth', 393);
    vi.stubGlobal('innerHeight', 852);

    const { container } = render(<App />);
    const app = container.firstElementChild;

    await waitFor(() => {
      expect(app).toHaveStyle({
        '--app-viewport-top': '0px',
        '--app-viewport-left': '0px',
        '--app-viewport-width': '393px',
        '--app-viewport-height': '852px',
      });
    });
  });
});

describe('TerminalPage', () => {
  it('bootstraps a saved pop-out using its directory rather than a stale URL hint', async () => {
    window.history.replaceState(
      {},
      '',
      '/terminal?runnerId=runner-1&conversationId=conv-123&cwd=%2Fother-workspace'
    );

    const { rerender, unmount } = render(<TerminalPage />);

    expect(screen.getByRole('status')).toHaveTextContent('Resolving remote terminal…');
    const terminal = await screen.findByTestId('terminal-modal');
    expect(terminal).toHaveAttribute('data-conversation-id', 'conv-123');
    expect(terminal).toHaveAttribute('data-runner-id', 'runner-1');
    expect(terminal).toHaveAttribute('data-target-kind', 'runner');
    expect(terminal).toHaveAttribute('data-cwd-label', '/runner/project');
    expect(document.documentElement).toHaveClass('terminal-popout-active');
    expect(document.body).toHaveClass('terminal-popout-active');
    const target: WorkspaceTarget = {
      kind: 'runner',
      runnerId: 'runner-1',
      conversationId: 'conv-123',
      cwd: '/runner/project',
    };
    expect(mockTerminalModal.mock.lastCall?.[0].target).toEqual(target);
    expect(readTerminalPopOutRecordForTarget(target)).toEqual(expect.objectContaining({ target }));
    expect(new URLSearchParams(window.location.search).get('cwd')).toBe(target.cwd);
    expect(window.name).toBe(getTerminalPopOutWindowName(target));
    rerender(<TerminalPage />);
    expect(mockGetConversation).toHaveBeenCalledExactlyOnceWith('conv-123');

    unmount();
    expect(readTerminalPopOutRecordForTarget(target)).toBeNull();
    expect(document.documentElement).not.toHaveClass('terminal-popout-active');
    expect(document.body).not.toHaveClass('terminal-popout-active');
  });

  it('rejects a mismatched conversation and runner before claiming pop-out ownership', async () => {
    mockGetConversation.mockResolvedValue({
      id: 'conv-mismatch',
      runnerId: 'runner-affinity',
    });
    window.history.replaceState(
      {},
      '',
      '/terminal?runnerId=runner-other&conversationId=conv-mismatch'
    );

    render(<TerminalPage />);

    expect(
      await screen.findByText('The terminal runner does not match this conversation.')
    ).toBeInTheDocument();
    expect(screen.queryByTestId('terminal-modal')).not.toBeInTheDocument();
    expect(window.localStorage.getItem(TERMINAL_POP_OUT_STORAGE_KEY)).toBeNull();
  });

  it('resolves a conversation-only remote pop-out without opening a local terminal', async () => {
    mockGetConversation.mockResolvedValue({
      id: 'conv-remote',
      runnerId: 'runner-remote',
      cwd: '/runner/remote-project',
    });
    window.history.replaceState({}, '', '/terminal?conversationId=conv-remote');

    const { unmount } = render(<TerminalPage />);

    expect(screen.getByRole('status')).toHaveTextContent('Resolving remote terminal…');
    const terminal = await screen.findByTestId('terminal-modal');
    expect(mockGetConversation).toHaveBeenCalledWith('conv-remote');
    expect(terminal).toHaveAttribute('data-target-kind', 'runner');
    expect(terminal).toHaveAttribute('data-runner-id', 'runner-remote');
    expect(terminal).toHaveAttribute('data-conversation-id', 'conv-remote');
    expect(
      readTerminalPopOutRecordForTarget({
        kind: 'runner',
        runnerId: 'runner-remote',
        conversationId: 'conv-remote',
        cwd: '/runner/remote-project',
      })
    ).toEqual(
      expect.objectContaining({
        target: {
          kind: 'runner',
          runnerId: 'runner-remote',
          conversationId: 'conv-remote',
          cwd: '/runner/remote-project',
        },
      })
    );

    unmount();
  });

  it('retains canonical draft ownership and the conversation identity after saving and reloading', async () => {
    mockGetConversation.mockRejectedValue(Object.assign(new Error('Not found'), { status: 404 }));
    window.history.replaceState(
      {},
      '',
      '/terminal?runnerId=runner-1&conversationId=draft-id&cwd=..%2Fproject'
    );
    const { rerender, unmount } = render(<TerminalPage />);
    const terminal = await screen.findByTestId('terminal-modal');
    const requestTarget = mockTerminalModal.mock.lastCall?.[0].target;
    expect(requestTarget).toEqual({
      kind: 'runner',
      runnerId: 'runner-1',
      conversationId: 'draft-id',
      cwd: '../project',
    });
    expect(window.localStorage.getItem(TERMINAL_POP_OUT_STORAGE_KEY)).toBeNull();

    act(() =>
      mockTerminalModal.mock.lastCall?.[0].onReady?.({
        type: 'ready',
        cwd: '/runner/project',
        name: 'bash',
        git: false,
        pid: 1,
      })
    );
    const target: WorkspaceTarget = {
      kind: 'runner',
      runnerId: 'runner-1',
      conversationId: 'draft-id',
      cwd: '/runner/project',
    };
    const firstRecord = readTerminalPopOutRecordForTarget(target);
    expect(firstRecord).toEqual(expect.objectContaining({ target, state: 'active' }));
    expect(terminal).toHaveAttribute('data-cwd-label', '/runner/project');
    expect(new URLSearchParams(window.location.search).get('cwd')).toBe('/runner/project');
    expect(window.name).toBe(getTerminalPopOutWindowName(target));
    rerender(<TerminalPage />);
    expect(mockTerminalModal.mock.lastCall?.[0].target).toBe(requestTarget);
    expect(mockGetConversation).toHaveBeenCalledExactlyOnceWith('draft-id');

    window.dispatchEvent(new Event('beforeunload'));
    unmount();
    expect(readTerminalPopOutRecordForTarget(target)).toEqual(
      expect.objectContaining({ id: firstRecord?.id, state: 'closing' })
    );

    mockGetConversation.mockResolvedValue({
      id: 'draft-id',
      runnerId: 'runner-1',
      cwd: '/runner/project',
    });
    const { unmount: unmountReloaded } = render(<TerminalPage />);
    await screen.findByTestId('terminal-modal');
    expect(mockTerminalModal.mock.lastCall?.[0].target).toEqual(target);
    await waitFor(() =>
      expect(readTerminalPopOutRecordForTarget(target)).toEqual(
        expect.objectContaining({ id: firstRecord?.id, state: 'active', target })
      )
    );
    expect(window.name).toBe(getTerminalPopOutWindowName(target));
    unmountReloaded();
    expect(readTerminalPopOutRecordForTarget(target)).toBeNull();
  });

  it.each([
    { query: 'cwd=%2Frunner%2Fproject', status: 404 },
    { query: 'runnerId=runner-1&cwd=%2Frunner%2Fproject', status: 404 },
    { query: 'conversationId=draft-id&cwd=%2Frunner%2Fproject', status: 404 },
    { query: 'runnerId=runner-1&conversationId=draft-id', status: 404 },
    {
      query: 'runnerId=runner-1&conversationId=draft-id&cwd=%2Frunner%2Fproject',
      status: 500,
    },
  ])('does not infer draft ownership from incomplete identity or other failures ($query, $status)', async ({
    query,
    status,
  }) => {
    mockGetConversation.mockRejectedValue(Object.assign(new Error('Failed'), { status }));
    window.history.replaceState({}, '', `/terminal?${query}`);
    render(<TerminalPage />);

    await screen.findByRole('alert');
    expect(screen.queryByTestId('terminal-modal')).not.toBeInTheDocument();
    expect(window.localStorage.getItem(TERMINAL_POP_OUT_STORAGE_KEY)).toBeNull();
  });

  it('rejects a conversation-only pop-out without remote affinity', async () => {
    mockGetConversation.mockResolvedValue({ id: 'conv-local' });
    window.history.replaceState({}, '', '/terminal?conversationId=conv-local');

    render(<TerminalPage />);

    expect(
      await screen.findByText('This conversation has no remote runner terminal.')
    ).toBeInTheDocument();
    expect(screen.queryByTestId('terminal-modal')).not.toBeInTheDocument();
    expect(window.localStorage.getItem(TERMINAL_POP_OUT_STORAGE_KEY)).toBeNull();
  });

  it('releases ownership while cached and reclaims it when restored', async () => {
    window.history.replaceState({}, '', '/terminal?conversationId=conv-123');
    const { unmount } = render(<TerminalPage />);
    await screen.findByTestId('terminal-modal');
    const target: WorkspaceTarget = {
      kind: 'runner',
      runnerId: 'runner-1',
      conversationId: 'conv-123',
      cwd: '/runner/project',
    };
    const record = readTerminalPopOutRecordForTarget(target);
    expect(record).toEqual(expect.objectContaining({ state: 'active' }));

    const pageHide = new Event('pagehide');
    Object.defineProperty(pageHide, 'persisted', { value: true });
    window.dispatchEvent(pageHide);

    expect(readTerminalPopOutRecordForTarget(target)).toEqual(
      expect.objectContaining({
        id: record?.id,
        state: 'closing',
      })
    );

    const pageShow = new Event('pageshow');
    Object.defineProperty(pageShow, 'persisted', { value: true });
    window.dispatchEvent(pageShow);

    expect(readTerminalPopOutRecordForTarget(target)).toEqual(
      expect.objectContaining({
        id: record?.id,
        state: 'active',
      })
    );

    unmount();
  });
});
