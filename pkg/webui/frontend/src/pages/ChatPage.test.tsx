import { beforeEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import ChatPage from './ChatPage';

const mockNavigate = vi.fn();
const mockGetConversations = vi.fn();
const mockGetConversation = vi.fn();
const mockStreamChat = vi.fn();

vi.mock('react-router-dom', async () => {
  const actual = await vi.importActual<typeof import('react-router-dom')>(
    'react-router-dom'
  );

  return {
    ...actual,
    useNavigate: () => mockNavigate,
    useParams: () => ({}),
  };
});

vi.mock('../services/api', () => ({
  default: {
    getConversations: (...args: unknown[]) => mockGetConversations(...args),
    getConversation: (...args: unknown[]) => mockGetConversation(...args),
    streamChat: (...args: unknown[]) => mockStreamChat(...args),
  },
}));

describe('ChatPage', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    window.localStorage.clear();
    window.HTMLElement.prototype.scrollIntoView = vi.fn();
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
});
