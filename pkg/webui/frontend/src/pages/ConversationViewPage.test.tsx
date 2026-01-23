import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, waitFor } from '../test/utils';
import { useParams } from 'react-router-dom';
import ConversationViewPage from './ConversationViewPage';
import { useConversation } from '../hooks/useConversation';
import { Message, ToolResult } from '../types';
import * as utils from '../utils';

// Mock react-router-dom
vi.mock('react-router-dom', async () => {
  const actual = await vi.importActual('react-router-dom');
  return {
    ...actual,
    useParams: vi.fn(),
  };
});

// Mock the useConversation hook
vi.mock('../hooks/useConversation');

// Mock showToast
vi.mock('../utils', async () => {
  const actual = await vi.importActual('../utils');
  return {
    ...actual,
    showToast: vi.fn(),
  };
});

// Mock window.confirm
global.confirm = vi.fn();

describe('ConversationViewPage', () => {
  const mockConversation = {
    id: 'conv-123',
    messages: [
      {
        role: 'user' as const,
        content: 'Hello',
        toolCalls: [],
      },
      {
        role: 'assistant' as const,
        content: 'Hi there!',
        toolCalls: [],
      },
    ],
    createdAt: '2023-01-01T00:00:00Z',
    updatedAt: '2023-01-01T00:00:00Z',
    messageCount: 2,
    toolResults: {},
    usage: {
      inputCost: 0.001,
      outputCost: 0.002,
    },
  };

  const defaultMockHook = {
    conversation: mockConversation,
    loading: false,
    error: null,
    deleteConversation: vi.fn(),
    exportConversation: vi.fn(),
    refresh: vi.fn(),
  };

  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(useParams).mockReturnValue({ id: 'conv-123' });
    vi.mocked(useConversation).mockReturnValue(defaultMockHook);
    vi.mocked(global.confirm).mockReturnValue(true);
  });

  it('renders loading state', () => {
    vi.mocked(useConversation).mockReturnValue({
      ...defaultMockHook,
      conversation: null,
      loading: true,
    });

    render(<ConversationViewPage />);

    expect(screen.getByText('Loading conversation...')).toBeInTheDocument();
  });

  it('renders error state with retry option', () => {
    const mockRefresh = vi.fn();
    vi.mocked(useConversation).mockReturnValue({
      ...defaultMockHook,
      conversation: null,
      error: 'Failed to load conversation',
      refresh: mockRefresh,
    });

    render(<ConversationViewPage />);

    expect(screen.getByText('Failed to load conversation')).toBeInTheDocument();
    expect(screen.getByText('← Back to Conversations')).toBeInTheDocument();

    const retryButton = screen.getByRole('button', { name: /retry/i });
    fireEvent.click(retryButton);

    expect(mockRefresh).toHaveBeenCalled();
  });

  it('renders not found state when conversation is null', () => {
    vi.mocked(useConversation).mockReturnValue({
      ...defaultMockHook,
      conversation: null,
      loading: false,
    });

    render(<ConversationViewPage />);

    expect(screen.getByText('Conversation not found')).toBeInTheDocument();
    expect(screen.getByText("The conversation you're looking for doesn't exist or has been deleted")).toBeInTheDocument();
    expect(screen.getByText('← Back to Conversations')).toBeInTheDocument();
  });

  it('renders conversation with messages', () => {
    render(<ConversationViewPage />);

    // Check breadcrumb
    expect(screen.getByText('Conversations')).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: 'conv-123' })).toBeInTheDocument();

    // Check messages are rendered
    expect(screen.getByText('Hello')).toBeInTheDocument();
    expect(screen.getByText('Hi there!')).toBeInTheDocument();
  });

  it('renders empty state when conversation has no messages', () => {
    vi.mocked(useConversation).mockReturnValue({
      ...defaultMockHook,
      conversation: {
        ...mockConversation,
        messages: [],
      },
    });

    render(<ConversationViewPage />);

    expect(screen.getByText('No messages found')).toBeInTheDocument();
    expect(screen.getByText('This conversation appears to be empty')).toBeInTheDocument();
  });

  it('handles export conversation', () => {
    const mockExport = vi.fn();
    vi.mocked(useConversation).mockReturnValue({
      ...defaultMockHook,
      exportConversation: mockExport,
    });

    render(<ConversationViewPage />);

    // Find and click export button (in ConversationHeader)
    const exportButton = screen.getByRole('button', { name: /export/i });
    fireEvent.click(exportButton);

    expect(mockExport).toHaveBeenCalled();
  });

  it('handles delete conversation with confirmation', async () => {
    const mockDelete = vi.fn().mockResolvedValue(undefined);
    vi.mocked(useConversation).mockReturnValue({
      ...defaultMockHook,
      deleteConversation: mockDelete,
    });

    render(<ConversationViewPage />);

    // Find and click delete button (in ConversationHeader)
    const deleteButton = screen.getByRole('button', { name: /delete/i });
    fireEvent.click(deleteButton);

    expect(global.confirm).toHaveBeenCalledWith('Are you sure you want to delete this conversation?');

    await waitFor(() => {
      expect(mockDelete).toHaveBeenCalled();
      expect(utils.showToast).toHaveBeenCalledWith('Conversation deleted successfully', 'success');
    });
  });

  it('cancels delete when user declines confirmation', async () => {
    vi.mocked(global.confirm).mockReturnValue(false);
    const mockDelete = vi.fn();
    vi.mocked(useConversation).mockReturnValue({
      ...defaultMockHook,
      deleteConversation: mockDelete,
    });

    render(<ConversationViewPage />);

    const deleteButton = screen.getByRole('button', { name: /delete/i });
    fireEvent.click(deleteButton);

    expect(mockDelete).not.toHaveBeenCalled();
  });

  it('shows error toast when delete fails', async () => {
    const mockError = new Error('Delete failed');
    const mockDelete = vi.fn().mockRejectedValue(mockError);
    vi.mocked(useConversation).mockReturnValue({
      ...defaultMockHook,
      deleteConversation: mockDelete,
    });

    render(<ConversationViewPage />);

    const deleteButton = screen.getByRole('button', { name: /delete/i });
    fireEvent.click(deleteButton);

    await waitFor(() => {
      expect(utils.showToast).toHaveBeenCalledWith(
        'Failed to delete conversation: Delete failed',
        'error'
      );
    });
  });

  it('uses empty string when id param is undefined', () => {
    vi.mocked(useParams).mockReturnValue({});

    render(<ConversationViewPage />);

    expect(useConversation).toHaveBeenCalledWith('');
  });

  it('passes conversation to child components', () => {
    render(<ConversationViewPage />);

    // ConversationHeader should receive the conversation
    // ConversationMetadata should receive the conversation
    // MessageList should receive messages and toolResults

    // Verify that components are rendered (they will use the conversation data)
    expect(screen.getByRole('button', { name: /export/i })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /delete/i })).toBeInTheDocument();
  });

  it('handles conversation with null messages gracefully', () => {
    vi.mocked(useConversation).mockReturnValue({
      ...defaultMockHook,
      conversation: {
        ...mockConversation,
        messages: null as unknown as Message[],
        toolResults: null as unknown as Record<string, ToolResult>,
      },
    });

    render(<ConversationViewPage />);

    // Should show empty state
    expect(screen.getByText('No messages found')).toBeInTheDocument();
  });
});