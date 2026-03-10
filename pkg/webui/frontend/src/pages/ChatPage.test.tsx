import { beforeEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import ChatPage from './ChatPage';

const mockNavigate = vi.fn();
const mockGetConversations = vi.fn();
const mockGetConversation = vi.fn();
const mockGetChatSettings = vi.fn();
const mockStreamChat = vi.fn();
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
});
