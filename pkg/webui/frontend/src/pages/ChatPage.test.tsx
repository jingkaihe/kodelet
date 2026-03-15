import { beforeEach, describe, expect, it, vi } from 'vitest';
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import ChatPage from './ChatPage';
import type { ChatStreamEvent } from '../types';

const mockNavigate = vi.fn();
const mockGetConversations = vi.fn();
const mockGetConversation = vi.fn();
const mockGetChatSettings = vi.fn();
const mockStreamChat = vi.fn();
const mockStreamConversation = vi.fn();
const mockSteerConversation = vi.fn();
const mockStopConversation = vi.fn();
const mockDeleteConversation = vi.fn();
const mockForkConversation = vi.fn();
let routeParams: { id?: string } = {};

vi.mock('react-router-dom', async () => {
  const actual = await vi.importActual<typeof import('react-router-dom')>(
    'react-router-dom'
  );

  return {
    ...actual,
    useNavigate: () => mockNavigate,
    useParams: () => routeParams,
  };
});

vi.mock('../services/api', () => ({
  default: {
    getConversations: (...args: unknown[]) => mockGetConversations(...args),
    getConversation: (...args: unknown[]) => mockGetConversation(...args),
    getChatSettings: (...args: unknown[]) => mockGetChatSettings(...args),
    streamChat: (...args: unknown[]) => mockStreamChat(...args),
    streamConversation: (...args: unknown[]) => mockStreamConversation(...args),
    steerConversation: (...args: unknown[]) => mockSteerConversation(...args),
    stopConversation: (...args: unknown[]) => mockStopConversation(...args),
    deleteConversation: (...args: unknown[]) => mockDeleteConversation(...args),
    forkConversation: (...args: unknown[]) => mockForkConversation(...args),
  },
}));

describe('ChatPage', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    routeParams = {};
    window.localStorage.clear();
    window.HTMLElement.prototype.scrollIntoView = vi.fn();
    mockGetChatSettings.mockResolvedValue({
      currentProfile: 'work',
      profiles: [
        { name: 'default', scope: 'built-in' },
        { name: 'work', scope: 'repo' },
        { name: 'premium', scope: 'global' },
      ],
    });
    mockGetConversations.mockResolvedValue({
      conversations: [],
      hasMore: false,
      total: 0,
      limit: 40,
      offset: 0,
    });
    mockSteerConversation.mockResolvedValue({
      success: true,
      conversation_id: 'conv-123',
      queued: false,
    });
    mockStreamConversation.mockRejectedValue(new Error('conversation is not actively streaming'));
    mockStopConversation.mockResolvedValue({
      success: true,
      conversation_id: 'conv-123',
      stopped: true,
    });
    mockDeleteConversation.mockResolvedValue(undefined);
    mockForkConversation.mockResolvedValue({
      success: true,
      conversation_id: 'conv-copy-123',
    });
  });

  const getGreeting = (): string => {
    const hour = new Date().getHours();
    if (hour < 12) {
      return 'Good morning';
    }
    if (hour < 18) {
      return 'Good afternoon';
    }
    return 'Good evening';
  };

  it('toggles the sidebar shell from the panel controls', async () => {
    render(<ChatPage />);

    await waitFor(() => expect(mockGetConversations).toHaveBeenCalled());
    expect(screen.getAllByText(getGreeting())).toHaveLength(1);
    expect(screen.getByTestId('chat-sidebar-shell')).toBeInTheDocument();
    expect(screen.getByTestId('sidebar-hide-button')).toHaveClass('sidebar-toggle-button');

    fireEvent.click(screen.getByTestId('sidebar-hide-button'));
    expect(screen.queryByTestId('chat-sidebar-shell')).not.toBeInTheDocument();
    expect(screen.getByTestId('sidebar-collapsed-rail')).toBeInTheDocument();

    fireEvent.click(screen.getByTestId('sidebar-attached-toggle'));
    expect(screen.getByTestId('chat-sidebar-shell')).toBeInTheDocument();
  });

  it('resizes the sidebar width with the drag handle', async () => {
    render(<ChatPage />);

    await waitFor(() => expect(mockGetConversations).toHaveBeenCalled());

    const sidebarShell = screen.getByTestId('chat-sidebar-shell');
    expect(sidebarShell.style.getPropertyValue('--sidebar-width')).toBe('320px');

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

  it('includes pasted image attachments in the streamed chat request', async () => {
    mockStreamChat.mockResolvedValue(undefined);

    const fileReaderResult = 'data:image/png;base64,aGVsbG8=';
    const originalFileReader = window.FileReader;

    class MockFileReader {
      result: string | ArrayBuffer | null = null;
      error: DOMException | null = null;
      onload: null | (() => void) = null;
      onerror: null | (() => void) = null;

      readAsDataURL() {
        this.result = fileReaderResult;
        this.onload?.();
      }
    }

    // @ts-expect-error test shim
    window.FileReader = MockFileReader;

    render(<ChatPage />);

    await waitFor(() => expect(mockGetConversations).toHaveBeenCalled());
    await waitFor(() => expect(mockGetChatSettings).toHaveBeenCalled());

    const textarea = screen.getByPlaceholderText('Ask kodelet anything...');
    fireEvent.change(textarea, { target: { value: 'describe this image' } });

    const file = new File(['hello'], 'clipboard.png', { type: 'image/png' });
    fireEvent.paste(textarea, {
      clipboardData: {
        items: [
          {
            kind: 'file',
            type: 'image/png',
            getAsFile: () => file,
          },
        ],
      },
      preventDefault: vi.fn(),
    });

    await waitFor(() => expect(screen.getByAltText('clipboard.png')).toBeInTheDocument());

    fireEvent.click(screen.getByRole('button', { name: 'Send' }));

    await waitFor(() => expect(mockStreamChat).toHaveBeenCalled());
    expect(mockStreamChat).toHaveBeenCalledWith(
      expect.objectContaining({
        message: 'describe this image',
        profile: 'work',
        content: expect.arrayContaining([
          expect.objectContaining({ type: 'text', text: 'describe this image' }),
          expect.objectContaining({
            type: 'image',
            source: expect.objectContaining({
              data: 'aGVsbG8=',
              media_type: 'image/png',
            }),
          }),
        ]),
      }),
      expect.any(Object)
    );

    window.FileReader = originalFileReader;
  });

  it('allows selecting a profile for a new conversation', async () => {
    mockStreamChat.mockResolvedValue(undefined);

    render(<ChatPage />);

    await waitFor(() => expect(mockGetChatSettings).toHaveBeenCalled());

    fireEvent.change(screen.getByLabelText('Profile'), {
      target: { value: 'premium' },
    });

    fireEvent.change(screen.getByPlaceholderText('Ask kodelet anything...'), {
      target: { value: 'hello' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Send' }));

    await waitFor(() => expect(mockStreamChat).toHaveBeenCalled());
    expect(mockStreamChat).toHaveBeenCalledWith(
      expect.objectContaining({ profile: 'premium' }),
      expect.any(Object)
    );
  });

  it('locks the profile selector for existing conversations', async () => {
    routeParams = { id: 'conv-123' };
    mockGetConversation.mockResolvedValue({
      id: 'conv-123',
      createdAt: '2023-01-01T00:00:00Z',
      updatedAt: '2023-01-02T00:00:00Z',
      messageCount: 1,
      profile: 'premium',
      profileLocked: true,
      messages: [
        {
          role: 'user',
          content: 'hello',
        },
      ],
      toolResults: {},
    });
    mockStreamChat.mockResolvedValue(undefined);

    render(<ChatPage />);

    await waitFor(() => expect(mockGetConversation).toHaveBeenCalledWith('conv-123'));

    expect(screen.getByTestId('profile-static-pill')).toBeInTheDocument();
    expect(screen.queryByLabelText('Profile')).not.toBeInTheDocument();
    expect(screen.getByText('Profile premium · locked')).toBeInTheDocument();

    fireEvent.change(screen.getByPlaceholderText('Ask kodelet anything...'), {
      target: { value: 'continue' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Send' }));

    await waitFor(() => expect(mockStreamChat).toHaveBeenCalled());
    expect(mockStreamChat).toHaveBeenCalledWith(
      expect.not.objectContaining({ profile: expect.anything() }),
      expect.any(Object)
    );
  });

  it('re-subscribes to an active conversation stream when reopening a conversation', async () => {
    routeParams = { id: 'conv-123' };
    mockGetConversation.mockResolvedValue({
      id: 'conv-123',
      createdAt: '2024-01-01T00:00:00Z',
      updatedAt: '2024-01-01T00:00:00Z',
      messageCount: 1,
      messages: [],
      toolResults: {},
    });
    mockStreamConversation.mockImplementation(async (_id, options) => {
      options.onEvent({ kind: 'conversation', conversation_id: 'conv-123' });
      options.onEvent({ kind: 'text-delta', conversation_id: 'conv-123', delta: 'hello' });
      options.onEvent({ kind: 'done', conversation_id: 'conv-123' });
    });

    render(<ChatPage />);

    await waitFor(() => expect(mockGetConversation).toHaveBeenCalledWith('conv-123'));
    await waitFor(() => expect(mockStreamConversation).toHaveBeenCalledWith('conv-123', expect.any(Object)));
    await waitFor(() => expect(screen.getByText('hello')).toBeInTheDocument());
  });

  it('queues steering while a conversation is streaming', async () => {
    routeParams = { id: 'conv-123' };
    mockGetConversation.mockResolvedValue({
      id: 'conv-123',
      createdAt: '2023-01-01T00:00:00Z',
      updatedAt: '2023-01-02T00:00:00Z',
      messageCount: 1,
      profile: 'premium',
      profileLocked: true,
      messages: [
        {
          role: 'user',
          content: 'hello',
        },
      ],
      toolResults: {},
    });

    let streamOptions: { onEvent: (event: ChatStreamEvent) => void } | null = null;
    mockStreamChat.mockImplementation(async (_request, options) => {
      streamOptions = options as { onEvent: (event: ChatStreamEvent) => void };
      return new Promise(() => undefined);
    });

    render(<ChatPage />);

    await waitFor(() => expect(mockGetConversation).toHaveBeenCalledWith('conv-123'));

    fireEvent.change(screen.getByPlaceholderText('Ask kodelet anything...'), {
      target: { value: 'continue' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Send' }));

    await waitFor(() => expect(mockStreamChat).toHaveBeenCalled());
    expect(screen.getByRole('button', { name: 'Stop' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Steer' })).toBeInTheDocument();

    await act(async () => {
      streamOptions?.onEvent({
        kind: 'tool-use',
        tool_call_id: 'tool-1',
        tool_name: 'search',
        input: '{}',
      });
    });

    fireEvent.change(screen.getByPlaceholderText('Steer the active conversation…'), {
      target: { value: 'Focus on tests' },
    });

    await waitFor(() => expect(screen.getByRole('button', { name: 'Steer' })).toBeEnabled());

    fireEvent.click(screen.getByRole('button', { name: 'Steer' }));

    await waitFor(() =>
      expect(mockSteerConversation).toHaveBeenCalledWith('conv-123', 'Focus on tests')
    );

    await act(async () => {
      streamOptions?.onEvent({ kind: 'conversation', conversation_id: 'conv-123' });
    });
  });

  it('allows sidebar navigation while a conversation is streaming', async () => {
    mockGetConversations.mockResolvedValue({
      conversations: [
        {
          id: 'conv-123',
          createdAt: '2024-01-01T00:00:00Z',
          updatedAt: '2024-01-01T00:00:00Z',
          messageCount: 1,
          summary: 'Active conversation',
        },
        {
          id: 'conv-456',
          createdAt: '2024-01-01T00:00:00Z',
          updatedAt: '2024-01-01T00:00:00Z',
          messageCount: 1,
          summary: 'Other conversation',
        },
      ],
      hasMore: false,
      total: 2,
      limit: 40,
      offset: 0,
    });

    mockStreamChat.mockImplementation(async (_request, options) => {
      options.onEvent({ kind: 'conversation', conversation_id: 'conv-123' });
      return new Promise(() => undefined);
    });

    render(<ChatPage />);

    await waitFor(() => expect(mockGetConversations).toHaveBeenCalled());

    fireEvent.change(screen.getByPlaceholderText('Ask kodelet anything...'), {
      target: { value: 'hello' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Send' }));

    await waitFor(() => expect(mockStreamChat).toHaveBeenCalled());

    fireEvent.click(screen.getByTestId('sidebar-hide-button'));
    expect(screen.queryByTestId('chat-sidebar-shell')).not.toBeInTheDocument();

    fireEvent.click(screen.getByTestId('sidebar-attached-toggle'));
    fireEvent.click(screen.getAllByRole('button', { name: /Other conversation/i })[0]);

    expect(mockNavigate).toHaveBeenCalledWith('/c/conv-456');
  });

  it('forks a conversation from the sidebar menu', async () => {
    mockGetConversations.mockResolvedValue({
      conversations: [
        {
          id: 'conv-123',
          createdAt: '2024-01-01T00:00:00Z',
          updatedAt: '2024-01-01T00:00:00Z',
          messageCount: 1,
          summary: 'Enabled resumable webUI conversation',
        },
      ],
      hasMore: false,
      total: 1,
      limit: 40,
      offset: 0,
    });

    render(<ChatPage />);

    await waitFor(() => expect(mockGetConversations).toHaveBeenCalled());

    fireEvent.click(
      screen.getByRole('button', {
        name: /More actions for Enabled resumable webUI conversation/i,
      })
    );
    fireEvent.click(screen.getByRole('menuitem', { name: 'Copy' }));

    await waitFor(() => expect(mockForkConversation).toHaveBeenCalledWith('conv-123'));
    await waitFor(() => expect(mockNavigate).toHaveBeenCalledWith('/c/conv-copy-123'));
  });

  it('deletes the active conversation from the sidebar menu', async () => {
    routeParams = { id: 'conv-123' };
    mockGetConversations.mockResolvedValue({
      conversations: [
        {
          id: 'conv-123',
          createdAt: '2024-01-01T00:00:00Z',
          updatedAt: '2024-01-01T00:00:00Z',
          messageCount: 1,
          summary: 'Enabled resumable webUI conversation',
        },
      ],
      hasMore: false,
      total: 1,
      limit: 40,
      offset: 0,
    });
    mockGetConversation.mockResolvedValue({
      id: 'conv-123',
      createdAt: '2024-01-01T00:00:00Z',
      updatedAt: '2024-01-01T00:00:00Z',
      messageCount: 1,
      summary: 'Enabled resumable webUI conversation',
      messages: [],
      toolResults: {},
    });

    render(<ChatPage />);

    await waitFor(() => expect(mockGetConversation).toHaveBeenCalledWith('conv-123'));

    fireEvent.click(
      screen.getByRole('button', {
        name: /More actions for Enabled resumable webUI conversation/i,
      })
    );
    fireEvent.click(screen.getByRole('menuitem', { name: 'Delete' }));

    await waitFor(() => expect(mockDeleteConversation).toHaveBeenCalledWith('conv-123'));
    await waitFor(() => expect(mockNavigate).toHaveBeenCalledWith('/'));
  });

  it('does not queue steering before the stream shows another backend turn is possible', async () => {
    routeParams = { id: 'conv-123' };
    mockGetConversation.mockResolvedValue({
      id: 'conv-123',
      createdAt: '2023-01-01T00:00:00Z',
      updatedAt: '2023-01-02T00:00:00Z',
      messageCount: 1,
      profile: 'premium',
      profileLocked: true,
      messages: [
        {
          role: 'user',
          content: 'hello',
        },
      ],
      toolResults: {},
    });

    mockStreamChat.mockImplementation(async () => new Promise(() => undefined));

    render(<ChatPage />);

    await waitFor(() => expect(mockGetConversation).toHaveBeenCalledWith('conv-123'));

    fireEvent.change(screen.getByPlaceholderText('Ask kodelet anything...'), {
      target: { value: 'continue' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Send' }));

    await waitFor(() => expect(mockStreamChat).toHaveBeenCalled());

    const steerButton = screen.getByRole('button', { name: 'Steer' });
    expect(steerButton).toBeDisabled();

    const textarea = screen.getByPlaceholderText(
      'Steering becomes available if the agent starts another turn…'
    );
    fireEvent.change(textarea, { target: { value: 'Focus on tests' } });
    fireEvent.keyDown(textarea, { key: 'Enter', shiftKey: false, preventDefault: vi.fn() });

    expect(mockSteerConversation).not.toHaveBeenCalled();
  });

  it('stops an active streaming conversation', async () => {
    const abortSpy = vi.fn();
    const originalAbortController = global.AbortController;
    let rejectStream: ((reason?: unknown) => void) | null = null;

    class MockAbortController {
      signal = {} as AbortSignal;
      abort = abortSpy;
    }

    global.AbortController = MockAbortController as unknown as typeof AbortController;

    mockStreamChat.mockImplementation(
      async (_request, options) =>
        new Promise((_, reject) => {
          options.onEvent({ kind: 'conversation', conversation_id: 'conv-123' } as ChatStreamEvent);
          rejectStream = reject;
        })
    );

    render(<ChatPage />);

    await waitFor(() => expect(mockGetConversations).toHaveBeenCalled());

    fireEvent.change(screen.getByPlaceholderText('Ask kodelet anything...'), {
      target: { value: 'hello' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Send' }));

    await waitFor(() => expect(mockStreamChat).toHaveBeenCalled());
    fireEvent.click(screen.getByRole('button', { name: 'Stop' }));

    expect(abortSpy).toHaveBeenCalled();
    await waitFor(() =>
      expect(mockStopConversation).toHaveBeenCalledWith('conv-123')
    );
    expect(screen.getByRole('button', { name: 'Stop' })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Send' })).not.toBeInTheDocument();

    await act(async () => {
      rejectStream?.(new DOMException('The operation was aborted', 'AbortError'));
    });

    await waitFor(() => expect(screen.getByRole('button', { name: 'Send' })).toBeInTheDocument());

    global.AbortController = originalAbortController;
  });
});
