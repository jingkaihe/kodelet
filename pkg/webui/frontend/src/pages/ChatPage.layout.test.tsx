import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, assert, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  ChatPage,
  flushAsyncUpdates,
  makeRunner,
  mockGetAnthropicProviderStatus,
  mockGetAuthPrincipal,
  mockGetCodexProviderStatus,
  mockGetConversation,
  mockGetConversations,
  mockGetCopilotProviderStatus,
  mockGetRunners,
  renderChatWithRunner,
  setRouteParams,
  setupChatPageTests,
  waitForTerminalAccess,
} from './ChatPage.testSupport';

describe('ChatPage layout and accessibility', () => {
  setupChatPageTests();

  it('toggles the sidebar shell from the panel controls', async () => {
    render(<ChatPage />);

    await waitFor(() => expect(mockGetConversations).toHaveBeenCalled());
    expect(
      screen.getByRole('heading', {
        level: 1,
        name: 'Hello! What would you like me to work on?',
      })
    ).toBeInTheDocument();
    expect(screen.getByTestId('chat-sidebar-shell')).toBeInTheDocument();
    expect(screen.getByTestId('sidebar-new-chat-button').querySelector('svg')).toHaveClass(
      'lucide-square-pen'
    );
    expect(screen.queryByLabelText('Filter conversations by workspace')).not.toBeInTheDocument();
    const sidebarHideButton = screen.getByTestId('sidebar-hide-button');
    expect(sidebarHideButton).toHaveClass('sidebar-toggle-button');
    expect(sidebarHideButton.querySelector('svg')).toHaveClass('lucide-panel-left');

    fireEvent.click(sidebarHideButton);
    expect(screen.queryByTestId('chat-sidebar-shell')).not.toBeInTheDocument();
    expect(screen.getByTestId('sidebar-collapsed-rail')).toBeInTheDocument();
    expect(screen.getByTestId('sidebar-attached-toggle').querySelector('svg')).toHaveClass(
      'lucide-panel-left'
    );
    expect(screen.getByTestId('sidebar-collapsed-search').querySelector('svg')).toHaveClass(
      'lucide-search'
    );
    expect(screen.getByTestId('sidebar-collapsed-new-chat').querySelector('svg')).toHaveClass(
      'lucide-square-pen'
    );

    fireEvent.click(screen.getByTestId('sidebar-attached-toggle'));
    expect(screen.getByTestId('chat-sidebar-shell')).toBeInTheDocument();
  });

  it('opens the search dialog and new chat from the collapsed sidebar rail', async () => {
    render(<ChatPage />);

    await waitFor(() => expect(mockGetConversations).toHaveBeenCalled());
    fireEvent.click(screen.getByTestId('sidebar-hide-button'));

    fireEvent.click(screen.getByTestId('sidebar-collapsed-search'));
    expect(screen.queryByTestId('chat-sidebar-shell')).not.toBeInTheDocument();
    expect(screen.getByRole('dialog', { name: 'Search conversations' })).toBeInTheDocument();
    await waitFor(() =>
      expect(screen.getByRole('searchbox', { name: 'Search conversations' })).toHaveFocus()
    );

    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: 'Close conversation search' }));
      await Promise.resolve();
    });
    const newChatButton = screen.getByTestId('sidebar-collapsed-new-chat');
    await waitFor(() => expect(newChatButton).toBeEnabled());
    fireEvent.click(newChatButton);

    expect(screen.getByTestId('new-chat-dialog')).toBeInTheDocument();
  });

  it('returns focus to the mobile sidebar toggle after closing conversation search', async () => {
    vi.stubGlobal(
      'matchMedia',
      vi.fn().mockImplementation((query: string) => ({
        matches: query === '(max-width: 1023px)',
        media: query,
        onchange: null,
        addEventListener: vi.fn(),
        removeEventListener: vi.fn(),
        addListener: vi.fn(),
        removeListener: vi.fn(),
        dispatchEvent: vi.fn(),
      }))
    );

    render(<ChatPage />);

    await waitFor(() => expect(mockGetConversations).toHaveBeenCalled());
    fireEvent.click(screen.getByTestId('sidebar-attached-toggle-mobile'));
    fireEvent.click(screen.getByTestId('sidebar-search-toggle'));
    await waitFor(() =>
      expect(screen.getByRole('searchbox', { name: 'Search conversations' })).toHaveFocus()
    );

    fireEvent.click(screen.getByRole('button', { name: 'Close conversation search' }));

    await waitFor(() => expect(screen.getByTestId('sidebar-attached-toggle-mobile')).toHaveFocus());
  });

  it('shows the account control for an authenticated OIDC session', async () => {
    mockGetAuthPrincipal.mockResolvedValue({
      id: 'https://issuer.example.com|jingkai-he',
      issuer: 'https://issuer.example.com',
      subject: 'jingkai-he',
      name: 'Jingkai He',
      email: 'jingkai@example.com',
      roles: ['user'],
    });

    render(<ChatPage />);

    expect(
      await screen.findByRole('button', { name: 'Jingkai He account menu' })
    ).toHaveTextContent('J He');
  });

  it('opens provider settings from the administrator account menu', async () => {
    mockGetAuthPrincipal.mockResolvedValue({
      id: 'https://issuer.example.com|admin',
      issuer: 'https://issuer.example.com',
      subject: 'admin',
      name: 'Admin User',
      roles: ['admin'],
    });
    render(<ChatPage />);

    fireEvent.click(await screen.findByRole('button', { name: 'Admin User account menu' }));
    const providerSettings = screen.getByRole('menuitem', { name: 'Provider settings' });
    fireEvent.click(providerSettings);

    expect(await screen.findByRole('dialog', { name: 'Provider settings' })).toBeInTheDocument();
    expect(mockGetCodexProviderStatus).toHaveBeenCalledOnce();
    expect(mockGetCopilotProviderStatus).toHaveBeenCalledOnce();
    expect(mockGetAnthropicProviderStatus).toHaveBeenCalledOnce();

    fireEvent.click(screen.getByRole('button', { name: 'Close provider settings' }));
    expect(screen.queryByRole('dialog', { name: 'Provider settings' })).not.toBeInTheDocument();
  });

  it('starts with the sidebar closed on mobile and closes it before opening a new chat', async () => {
    window.localStorage.setItem('kodelet.chat.sidebar.visible', 'true');
    vi.stubGlobal(
      'matchMedia',
      vi.fn().mockImplementation((query: string) => ({
        matches: query === '(max-width: 1023px)',
        media: query,
        onchange: null,
        addEventListener: vi.fn(),
        removeEventListener: vi.fn(),
        addListener: vi.fn(),
        removeListener: vi.fn(),
        dispatchEvent: vi.fn(),
      }))
    );

    render(<ChatPage />);

    await waitFor(() => expect(mockGetConversations).toHaveBeenCalled());
    expect(screen.queryByTestId('chat-sidebar-shell')).not.toBeInTheDocument();
    const mobileSidebarToggle = screen.getByTestId('sidebar-attached-toggle-mobile');
    expect(mobileSidebarToggle).toBeInTheDocument();
    expect(mobileSidebarToggle.querySelector('svg')).toHaveClass('lucide-panel-left');
    expect(window.localStorage.getItem('kodelet.chat.sidebar.visible')).toBe('true');

    await act(async () => {
      fireEvent.click(screen.getByTestId('sidebar-attached-toggle-mobile'));
    });
    expect(screen.getByRole('dialog', { name: 'Conversations' })).toBeInTheDocument();
    expect(screen.getByTestId('chat-sidebar-shell')).toHaveAttribute('tabindex', '-1');
    expect(screen.getByTestId('sidebar-hide-button')).toBeEnabled();
    await waitFor(() => expect(screen.getByTestId('sidebar-hide-button')).toHaveFocus());
    const themePicker = screen.getByRole('button', { name: 'Choose theme' });
    fireEvent.click(themePicker);
    const selectedTheme = screen.getByRole('menuitemradio', { checked: true });
    expect(selectedTheme).toHaveFocus();
    fireEvent.keyDown(selectedTheme, { key: 'Escape' });
    expect(screen.queryByRole('menu', { name: 'Color theme' })).not.toBeInTheDocument();
    expect(screen.getByRole('dialog', { name: 'Conversations' })).toBeInTheDocument();
    expect(themePicker).toHaveFocus();
    fireEvent.click(screen.getByTestId('sidebar-hide-button'));
    await waitFor(() => expect(screen.getByTestId('sidebar-attached-toggle-mobile')).toHaveFocus());
    fireEvent.click(screen.getByTestId('sidebar-attached-toggle-mobile'));

    await act(async () => {
      fireEvent.click(screen.getByTestId('sidebar-new-chat-button'));
      await Promise.resolve();
    });
    expect(screen.queryByTestId('chat-sidebar-shell')).not.toBeInTheDocument();
    expect(screen.getByTestId('new-chat-dialog')).toBeInTheDocument();
  });

  it('isolates the mobile workspace sheet without stealing terminal keys', async () => {
    vi.stubGlobal(
      'matchMedia',
      vi.fn().mockImplementation((query: string) => ({
        matches: query === '(max-width: 1023px)' || query === '(max-width: 1180px)',
        media: query,
        onchange: null,
        addEventListener: vi.fn(),
        removeEventListener: vi.fn(),
        addListener: vi.fn(),
        removeListener: vi.fn(),
        dispatchEvent: vi.fn(),
      }))
    );

    await renderChatWithRunner();
    await waitFor(() => expect(mockGetConversations).toHaveBeenCalled());
    await waitForTerminalAccess();

    expect(within(screen.getByTestId('workspace-tools-rail')).getAllByRole('button')).toHaveLength(
      1
    );
    fireEvent.click(screen.getByTestId('workspace-tools-toggle'));
    await screen.findByTestId('terminal-panel');

    const workspaceShell = screen.getByTestId('workspace-tools-shell');
    const workspaceToggle = screen.getByTestId('workspace-tools-toggle');
    expect(workspaceToggle.querySelector('svg')).toHaveClass('lucide-panel-right');
    const terminalTab = screen.getByTestId('workspace-tools-terminal-tab');
    const diffTab = screen.getByTestId('workspace-tools-diff-tab');
    const terminalHost = screen.getByTestId('terminal-host');
    const chatMain = document.querySelector('main.chat-main-panel');
    expect(workspaceShell).toHaveAttribute('role', 'dialog');
    expect(workspaceShell).toHaveAttribute('aria-modal', 'true');
    expect(chatMain).toHaveAttribute('inert');
    expect(chatMain).toHaveAttribute('aria-hidden', 'true');

    workspaceToggle.focus();
    fireEvent.keyDown(window, { key: 'Tab' });
    expect(terminalTab).toHaveFocus();

    terminalTab.focus();
    fireEvent.keyDown(window, { key: 'Tab', shiftKey: true });
    expect(workspaceToggle).toHaveFocus();

    terminalHost.focus();
    expect(fireEvent.keyDown(terminalHost, { key: 'Tab' })).toBe(true);
    expect(terminalHost).toHaveFocus();

    fireEvent.keyDown(terminalHost, { key: 'F6' });
    expect(workspaceToggle).toHaveFocus();

    terminalHost.focus();
    expect(fireEvent.keyDown(terminalHost, { key: 'Tab', shiftKey: true })).toBe(true);
    expect(terminalHost).toHaveFocus();

    fireEvent.keyDown(terminalHost, { key: 'F6', shiftKey: true });
    expect(diffTab).toHaveFocus();
  });

  it('lets keyboard users leave the terminal on wide desktop', async () => {
    vi.stubGlobal(
      'matchMedia',
      vi.fn().mockImplementation((query: string) => ({
        matches: false,
        media: query,
        onchange: null,
        addEventListener: vi.fn(),
        removeEventListener: vi.fn(),
        addListener: vi.fn(),
        removeListener: vi.fn(),
        dispatchEvent: vi.fn(),
      }))
    );

    await renderChatWithRunner();
    await waitFor(() => expect(mockGetConversations).toHaveBeenCalled());
    await waitForTerminalAccess();
    fireEvent.click(screen.getByTestId('workspace-tools-toggle'));
    await screen.findByTestId('terminal-panel');

    const terminalHost = screen.getByTestId('terminal-host');
    const workspaceToggle = screen.getByTestId('workspace-tools-toggle');
    const terminalTab = screen.getByTestId('workspace-tools-terminal-tab');
    const diffTab = screen.getByTestId('workspace-tools-diff-tab');
    expect(screen.getByTestId('workspace-tools-shell')).not.toHaveAttribute('role', 'dialog');

    terminalHost.focus();
    expect(fireEvent.keyDown(terminalHost, { key: 'Tab' })).toBe(true);
    expect(terminalHost).toHaveFocus();

    fireEvent.keyDown(terminalHost, { key: 'F6' });
    expect(workspaceToggle).toHaveFocus();

    terminalHost.focus();
    fireEvent.keyDown(terminalHost, { key: 'F6', shiftKey: true });
    expect(diffTab).toHaveFocus();

    fireEvent.click(diffTab);
    await waitFor(() => expect(screen.queryByTestId('terminal-host')).not.toBeInTheDocument());
    fireEvent.click(terminalTab);
    const reopenedTerminalHost = await screen.findByTestId('terminal-host');
    reopenedTerminalHost.focus();
    fireEvent.keyDown(reopenedTerminalHost, { key: 'F6' });
    expect(workspaceToggle).toHaveFocus();
  });

  it('preserves transcript text selection while runner polling updates status', async () => {
    vi.useFakeTimers();
    setRouteParams({ id: 'conv-remote' });
    const runner = makeRunner();
    mockGetRunners
      .mockResolvedValueOnce({ runners: [runner] })
      .mockResolvedValueOnce({ runners: [{ ...runner }] })
      .mockResolvedValue({ runners: [{ ...runner, status: 'busy' }] });
    mockGetConversation.mockResolvedValue({
      id: 'conv-remote',
      createdAt: '2026-09-10T00:00:00Z',
      updatedAt: '2026-09-10T00:00:00Z',
      messageCount: 2,
      cwd: '/runner/kodelet',
      runnerId: runner.id,
      runner,
      messages: [
        { role: 'user', content: 'Keep this question selected.' },
        { role: 'assistant', content: 'Keep this answer selected.' },
      ],
      toolResults: {},
    });

    const selection = window.getSelection();
    try {
      assert(selection);
      render(<ChatPage />);
      await flushAsyncUpdates();
      await flushAsyncUpdates();

      fireEvent.click(screen.getByTestId('transcript-meta-strip'));
      const details = within(screen.getByTestId('transcript-meta-details'));
      expect(details.getByText('Runner').nextElementSibling).toHaveTextContent('kodelet-gpu');
      const startNode = screen.getByText('Keep this question selected.').firstChild;
      const endNode = screen.getByText('Keep this answer selected.').firstChild;
      assert(startNode);
      assert(endNode);
      const range = document.createRange();
      range.setStart(startNode, 5);
      range.setEnd(endNode, 16);
      selection.removeAllRanges();
      selection.addRange(range);
      const selectedText = selection.toString();
      expect(selectedText).not.toBe('');
      expect(mockGetRunners).toHaveBeenCalledTimes(1);

      for (const [index, status] of ['idle', '1 active'].entries()) {
        await act(async () => {
          await vi.advanceTimersByTimeAsync(5000);
        });

        expect(mockGetRunners).toHaveBeenCalledTimes(index + 2);
        expect(details.getByText('Status').nextElementSibling).toHaveTextContent(status);
        expect(selection.toString()).toBe(selectedText);
        expect(selection.anchorNode).toBe(startNode);
        expect(selection.focusNode).toBe(endNode);
        expect(startNode.isConnected).toBe(true);
        expect(endNode.isConnected).toBe(true);
      }
    } finally {
      selection?.removeAllRanges();
      vi.useRealTimers();
    }
  });

  it('removes a desktop sidebar from the accessibility tree when the workspace becomes modal', async () => {
    let overlayMatches = false;
    const overlayListeners = new Set<(event: MediaQueryListEvent) => void>();
    const overlayMediaQuery = {
      get matches() {
        return overlayMatches;
      },
      media: '(max-width: 1180px)',
      onchange: null,
      addEventListener: vi.fn((_type: string, listener: (event: MediaQueryListEvent) => void) => {
        overlayListeners.add(listener);
      }),
      removeEventListener: vi.fn(
        (_type: string, listener: (event: MediaQueryListEvent) => void) => {
          overlayListeners.delete(listener);
        }
      ),
      addListener: vi.fn(),
      removeListener: vi.fn(),
      dispatchEvent: vi.fn(),
    };
    vi.stubGlobal(
      'matchMedia',
      vi.fn().mockImplementation((query: string) =>
        query === '(max-width: 1180px)'
          ? overlayMediaQuery
          : {
              matches: false,
              media: query,
              onchange: null,
              addEventListener: vi.fn(),
              removeEventListener: vi.fn(),
              addListener: vi.fn(),
              removeListener: vi.fn(),
              dispatchEvent: vi.fn(),
            }
      )
    );

    await renderChatWithRunner();
    await waitFor(() => expect(mockGetConversations).toHaveBeenCalled());
    await waitForTerminalAccess();
    expect(screen.getByTestId('chat-sidebar-shell')).not.toHaveAttribute('inert');

    fireEvent.click(screen.getByTestId('workspace-tools-toggle'));
    const terminalPanel = await screen.findByTestId('terminal-panel');
    const workspaceShell = screen.getByTestId('workspace-tools-shell');
    expect(workspaceShell).not.toHaveAttribute('role');
    expect(workspaceShell).not.toHaveAttribute('aria-modal');
    expect(screen.getByTestId('chat-sidebar-shell')).not.toHaveAttribute('inert');

    overlayMatches = true;
    act(() => {
      for (const listener of overlayListeners) {
        listener({ matches: true } as MediaQueryListEvent);
      }
    });

    expect(screen.getByTestId('chat-sidebar-shell')).toHaveAttribute('inert');
    expect(screen.getByTestId('chat-sidebar-shell')).toHaveAttribute('aria-hidden', 'true');
    expect(screen.getByTestId('workspace-tools-shell')).toBe(workspaceShell);
    expect(workspaceShell).toHaveAttribute('role', 'dialog');
    expect(workspaceShell).toHaveAttribute('aria-modal', 'true');
    expect(screen.getByTestId('terminal-panel')).toBe(terminalPanel);
  });

  it('resizes the sidebar width from the transparent edge handle', async () => {
    render(<ChatPage />);

    await waitFor(() => expect(mockGetConversations).toHaveBeenCalled());

    const sidebarShell = screen.getByTestId('chat-sidebar-shell');
    expect(sidebarShell.style.getPropertyValue('--sidebar-width')).toBe('320px');
    expect(screen.getByTestId('chat-sidebar-resizer')).toHaveClass('sidebar-resize-edge');

    fireEvent.mouseDown(screen.getByTestId('chat-sidebar-resizer'), {
      clientX: 320,
    });

    await waitFor(() => expect(document.body.style.cursor).toBe('col-resize'));

    fireEvent.mouseMove(window, { clientX: 420 });
    fireEvent.mouseUp(window);

    await waitFor(() =>
      expect(
        screen.getByTestId('chat-sidebar-shell').style.getPropertyValue('--sidebar-width')
      ).toBe('420px')
    );
  });

  it('resizes the sidebar with the keyboard and exposes its persisted bounds', async () => {
    render(<ChatPage />);
    await flushAsyncUpdates();

    const resizer = screen.getByRole('separator', { name: 'Resize sidebar' });
    expect(resizer).toHaveAttribute('tabindex', '0');
    expect(resizer).toHaveAttribute('aria-controls', 'chat-sidebar');
    expect(resizer).toHaveAttribute('aria-orientation', 'vertical');
    expect(resizer).toHaveAttribute('aria-valuemin', '260');
    expect(resizer).toHaveAttribute('aria-valuemax', '520');
    resizer.focus();

    for (const [key, width] of [
      ['ArrowLeft', 310],
      ['ArrowRight', 320],
      ['Home', 260],
      ['ArrowLeft', 260],
      ['End', 520],
      ['ArrowRight', 520],
    ] as const) {
      expect(fireEvent.keyDown(resizer, { key })).toBe(false);
      expect(resizer).toHaveFocus();
      expect(resizer).toHaveAttribute('aria-valuenow', String(width));
      expect(resizer).toHaveAttribute('aria-valuetext', `${width} pixels`);
      expect(screen.getByTestId('chat-sidebar-shell')).toHaveStyle({
        '--sidebar-width': `${width}px`,
      });
      expect(window.localStorage.getItem('kodelet.chat.sidebar.width')).toBe(String(width));
    }

    expect(fireEvent.keyDown(resizer, { key: 'Tab' })).toBe(true);
  });

  describe('workspace panel resizing', () => {
    let viewportWidth: number;
    let measure: ReturnType<typeof vi.spyOn>;
    let observers: Set<(entries: ResizeObserverEntry[]) => void>;
    let mediaListeners: Map<string, Set<(event: MediaQueryListEvent) => void>>;
    const matches = (query: string) => viewportWidth <= (query.includes('1180') ? 1180 : 1023);
    const resizeViewport = (width: number) => {
      act(() => {
        viewportWidth = width;
        vi.stubGlobal('innerWidth', width);
        fireEvent(window, new Event('resize'));
        for (const [query, listeners] of mediaListeners) {
          for (const listener of listeners)
            listener({ matches: matches(query) } as MediaQueryListEvent);
        }
        for (const observer of observers) observer([]);
      });
    };
    const openWorkspace = async () => {
      const result = await renderChatWithRunner();
      fireEvent.click(screen.getByTestId('workspace-tools-toggle'));
      await screen.findByTestId('terminal-panel');
      return result;
    };

    beforeEach(() => {
      viewportWidth = 1600;
      observers = new Set();
      mediaListeners = new Map();
      vi.stubGlobal('innerWidth', viewportWidth);
      vi.stubGlobal(
        'PointerEvent',
        class extends MouseEvent {
          pointerId: number;
          constructor(type: string, params: PointerEventInit = {}) {
            super(type, params);
            this.pointerId = params.pointerId ?? 1;
          }
        }
      );
      vi.stubGlobal(
        'ResizeObserver',
        class {
          constructor(private callback: (entries: ResizeObserverEntry[]) => void) {}
          observe() {
            observers.add(this.callback);
          }
          disconnect() {
            observers.delete(this.callback);
          }
        }
      );
      vi.stubGlobal(
        'matchMedia',
        vi.fn((query: string) => {
          const listeners = new Set<(event: MediaQueryListEvent) => void>();
          mediaListeners.set(query, listeners);
          return {
            get matches() {
              return matches(query);
            },
            media: query,
            addEventListener: (_: string, listener: (event: MediaQueryListEvent) => void) =>
              listeners.add(listener),
            removeEventListener: (_: string, listener: (event: MediaQueryListEvent) => void) =>
              listeners.delete(listener),
          };
        })
      );
      measure = vi
        .spyOn(HTMLElement.prototype, 'getBoundingClientRect')
        .mockImplementation(function (this: HTMLElement) {
          const width =
            this.dataset.testid === 'chat-layout'
              ? viewportWidth
              : this.dataset.testid === 'chat-sidebar-shell'
                ? Number.parseFloat(this.style.getPropertyValue('--sidebar-width'))
                : this.classList.contains('sidebar-collapsed-rail')
                  ? 44
                  : 0;
          return DOMRect.fromRect({ width, height: 900 });
        });
    });

    afterEach(() => measure.mockRestore());

    it('drags within bounds without remounting its contents and persists on release', async () => {
      const { unmount } = await openWorkspace();
      const separator = screen.getByRole('separator', { name: 'Resize workspace panel' });
      const shell = screen.getByTestId('workspace-tools-shell');
      const terminal = screen.getByTestId('terminal-panel');
      const setPointerCapture = vi.fn();
      const releasePointerCapture = vi.fn();
      Object.assign(separator, {
        setPointerCapture,
        hasPointerCapture: () => true,
        releasePointerCapture,
      });
      expect(separator).toHaveAttribute('aria-valuenow', '652');
      fireEvent.pointerDown(separator, { clientX: 940, button: 0, pointerId: 7 });
      expect(setPointerCapture).toHaveBeenCalledWith(7);
      expect(separator).toHaveFocus();
      expect(document.body.style.cursor).toBe('col-resize');
      expect(document.querySelector('.workspace-resize-shield')).toBeInTheDocument();
      fireEvent.pointerMove(window, { clientX: 10, pointerId: 8 });
      expect(shell).toHaveStyle({ '--workspace-width': '652px' });
      fireEvent.pointerMove(window, { clientX: 840, pointerId: 7 });
      expect(shell).toHaveStyle({ '--workspace-width': '752px' });
      fireEvent.pointerMove(window, { clientX: -5000, pointerId: 7 });
      expect(shell).toHaveStyle({ '--workspace-width': '880px' });
      fireEvent.pointerMove(window, { clientX: 5000, pointerId: 7 });
      expect(shell).toHaveStyle({ '--workspace-width': '360px' });
      fireEvent.pointerMove(window, { clientX: 980, pointerId: 7 });
      expect(shell).toHaveStyle({ '--workspace-width': '612px' });
      expect(window.localStorage.getItem('kodelet.chat.workspace.width')).toBeNull();
      fireEvent.pointerUp(window, { pointerId: 7 });
      expect(screen.getByTestId('terminal-panel')).toBe(terminal);
      expect(window.localStorage.getItem('kodelet.chat.workspace.width')).toBe('612');
      expect(releasePointerCapture).toHaveBeenCalledWith(7);
      expect(document.body.style.cursor).toBe('');
      expect(document.querySelector('.workspace-resize-shield')).not.toBeInTheDocument();
      unmount();
      await openWorkspace();
      expect(screen.getByTestId('workspace-tools-shell')).toHaveStyle({
        '--workspace-width': '612px',
      });
    });

    it('supports keyboard resizing and exposes bounds that reserve room for the sidebar and chat', async () => {
      await openWorkspace();
      const separator = screen.getByRole('separator', { name: 'Resize workspace panel' });
      expect(separator).toHaveAttribute('aria-controls', 'workspace-tools');
      expect(separator).toHaveAttribute('aria-orientation', 'vertical');
      expect(separator).toHaveAttribute('aria-valuemin', '360');
      expect(separator).toHaveAttribute('aria-valuemax', '880');
      separator.focus();
      for (const [key, width] of [
        ['ArrowLeft', 662],
        ['ArrowRight', 652],
        ['Home', 360],
        ['ArrowRight', 360],
        ['End', 880],
        ['ArrowLeft', 880],
      ] as const) {
        expect(fireEvent.keyDown(separator, { key })).toBe(false);
        expect(separator).toHaveFocus();
        expect(separator).toHaveAttribute('aria-valuenow', String(width));
        expect(separator).toHaveAttribute('aria-valuetext', `${width} pixels`);
      }
      expect(screen.getByTestId('workspace-tools-shell')).toHaveStyle({
        '--workspace-width': '880px',
      });
      expect(window.localStorage.getItem('kodelet.chat.workspace.width')).toBe('880');
      expect(fireEvent.keyDown(separator, { key: 'Tab' })).toBe(true);
    });

    it('clamps to viewport and sidebar changes without replacing the preferred width', async () => {
      window.localStorage.setItem('kodelet.chat.workspace.width', '1000');
      await openWorkspace();
      const separator = screen.getByRole('separator', { name: 'Resize workspace panel' });
      expect(separator).toHaveAttribute('aria-valuenow', '880');
      resizeViewport(1200);
      expect(separator).toHaveAttribute('aria-valuenow', '480');
      resizeViewport(2000);
      expect(separator).toHaveAttribute('aria-valuenow', '1000');
      resizeViewport(1600);
      fireEvent.keyDown(screen.getByRole('separator', { name: 'Resize sidebar' }), { key: 'End' });
      act(() => {
        for (const observer of observers) observer([]);
      });
      expect(separator).toHaveAttribute('aria-valuemax', '680');
      expect(separator).toHaveAttribute('aria-valuenow', '680');
      fireEvent.click(screen.getByTestId('sidebar-hide-button'));
      expect(separator).toHaveAttribute('aria-valuemax', '1156');
      expect(separator).toHaveAttribute('aria-valuenow', '1000');
      fireEvent.click(screen.getByTestId('sidebar-attached-toggle'));
      expect(separator).toHaveAttribute('aria-valuenow', '680');
      expect(window.localStorage.getItem('kodelet.chat.workspace.width')).toBe('1000');
    });

    it('cancels interrupted drags without persisting or leaving handlers active', async () => {
      await openWorkspace();
      const separator = screen.getByRole('separator', { name: 'Resize workspace panel' });
      for (const reason of ['pointercancel', 'lostpointercapture', 'blur', 'Escape']) {
        fireEvent.pointerDown(separator, { clientX: 940, button: 0 });
        fireEvent.pointerMove(window, { clientX: 840 });
        expect(separator).toHaveAttribute('aria-valuenow', '752');
        if (reason === 'Escape') fireEvent.keyDown(window, { key: 'Escape' });
        else if (reason === 'pointercancel') fireEvent.pointerCancel(window);
        else fireEvent(reason === 'lostpointercapture' ? separator : window, new Event(reason));
        expect(screen.getByTestId('workspace-tools-shell'), reason).toHaveStyle({
          '--workspace-width': '652px',
        });
        expect(document.body.style.cursor, reason).toBe('');
        expect(document.body.style.userSelect, reason).toBe('');
        expect(window.localStorage.getItem('kodelet.chat.workspace.width'), reason).toBeNull();
        fireEvent.pointerMove(window, { clientX: 500 });
        expect(separator, reason).toHaveAttribute('aria-valuenow', '652');
      }
    });

    it('removes drag capture and restores body styles when unmounted', async () => {
      const { unmount } = await openWorkspace();
      const separator = screen.getByRole('separator', { name: 'Resize workspace panel' });
      const releasePointerCapture = vi.fn();
      Object.assign(separator, {
        setPointerCapture: vi.fn(),
        hasPointerCapture: () => true,
        releasePointerCapture,
      });
      document.body.style.cursor = 'crosshair';
      document.body.style.userSelect = 'text';
      fireEvent.pointerDown(separator, { clientX: 940, button: 0 });
      unmount();
      expect(releasePointerCapture).toHaveBeenCalledWith(1);
      expect(document.body.style.cursor).toBe('crosshair');
      expect(document.body.style.userSelect).toBe('text');
      document.body.style.cursor = '';
      document.body.style.userSelect = '';
      expect(observers.size).toBe(0);
    });

    it('cancels an active drag when entering the mobile overlay and restores desktop sizing', async () => {
      window.localStorage.setItem('kodelet.chat.workspace.width', '700');
      await openWorkspace();
      const terminal = screen.getByTestId('terminal-panel');
      fireEvent.pointerDown(screen.getByRole('separator', { name: 'Resize workspace panel' }), {
        clientX: 900,
        button: 0,
      });
      fireEvent.pointerMove(window, { clientX: 800 });
      resizeViewport(1100);
      expect(
        screen.queryByRole('separator', { name: 'Resize workspace panel' })
      ).not.toBeInTheDocument();
      expect(screen.getByTestId('workspace-tools-shell')).toHaveAttribute('aria-modal', 'true');
      expect(document.body.style.cursor).toBe('');
      expect(document.querySelector('.workspace-resize-shield')).not.toBeInTheDocument();
      expect(screen.getByTestId('terminal-panel')).toBe(terminal);
      resizeViewport(1600);
      expect(screen.getByRole('separator', { name: 'Resize workspace panel' })).toHaveAttribute(
        'aria-valuenow',
        '700'
      );
      expect(window.localStorage.getItem('kodelet.chat.workspace.width')).toBe('700');
    });
  });
});
