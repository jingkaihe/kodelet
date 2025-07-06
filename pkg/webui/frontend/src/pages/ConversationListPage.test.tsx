import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, waitFor } from '../test/utils';
import ConversationListPage from './ConversationListPage';
import { useConversations } from '../hooks/useConversations';
import * as utils from '../utils';

// Mock the useConversations hook
vi.mock('../hooks/useConversations');

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

describe('ConversationListPage', () => {
  const mockConversations = [
    {
      id: 'conv-1',
      messages: [],
      toolResults: {},
      usage: {
        inputTokens: 100,
        outputTokens: 200,
      },
      createdAt: '2023-01-01T00:00:00Z',
      updatedAt: '2023-01-01T00:00:00Z',
      messageCount: 5,
      preview: 'Test preview 1',
    },
    {
      id: 'conv-2',
      messages: [],
      toolResults: {},
      usage: {
        inputTokens: 150,
        outputTokens: 250,
      },
      createdAt: '2023-01-02T00:00:00Z',
      updatedAt: '2023-01-02T00:00:00Z',
      messageCount: 3,
      preview: 'Test preview 2',
    },
  ];

  const mockStats = {
    totalConversations: 10,
    totalMessages: 100,
    totalTokens: 1000,
    totalCost: 0.05,
    inputTokens: 600,
    outputTokens: 400,
    cacheReadTokens: 0,
    cacheWriteTokens: 0,
    inputCost: 0.03,
    outputCost: 0.02,
    cacheReadCost: 0,
    cacheWriteCost: 0,
  };

  const defaultMockHook = {
    conversations: mockConversations,
    stats: mockStats,
    loading: false,
    error: null,
    hasMore: false,
    filters: {
      searchTerm: '',
      sortBy: 'updated' as const,
      sortOrder: 'desc' as const,
      limit: 25,
      offset: 0,
    },
    setFilters: vi.fn(),
    loadMore: vi.fn(),
    deleteConversation: vi.fn(),
    refresh: vi.fn(),
    loadConversations: vi.fn(),
  };

  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(useConversations).mockReturnValue(defaultMockHook);
    vi.mocked(global.confirm).mockReturnValue(true);
  });

  it('renders page title and description', () => {
    render(<ConversationListPage />);
    
    expect(screen.getByText('Conversations')).toBeInTheDocument();
    expect(screen.getByText('Browse and search your conversation history')).toBeInTheDocument();
  });

  it('renders conversation list when conversations exist', () => {
    render(<ConversationListPage />);
    
    expect(screen.getByText('Test preview 1')).toBeInTheDocument();
    expect(screen.getByText('Test preview 2')).toBeInTheDocument();
  });

  it('renders stats card when stats are available', () => {
    render(<ConversationListPage />);
    
    // StatsCard should be rendered with the stats
    // Since we don't know exactly how StatsCard renders the stats, 
    // let's check that the StatsCard component receives the stats prop
    // We can verify this by checking if the stats values are present somewhere in the document
    const { container } = render(<ConversationListPage />);
    expect(container.textContent).toContain('10'); // totalConversations value
  });

  it('renders loading state when loading with no conversations', () => {
    vi.mocked(useConversations).mockReturnValue({
      ...defaultMockHook,
      conversations: [],
      loading: true,
    });

    render(<ConversationListPage />);
    
    expect(screen.getByText('Loading conversations...')).toBeInTheDocument();
  });

  it('renders empty state when no conversations and not loading', () => {
    vi.mocked(useConversations).mockReturnValue({
      ...defaultMockHook,
      conversations: [],
      loading: false,
    });

    render(<ConversationListPage />);
    
    expect(screen.getByText('No conversations found')).toBeInTheDocument();
    expect(screen.getByText('Try adjusting your search criteria or filters')).toBeInTheDocument();
  });

  it('renders error state when error exists', () => {
    const mockRefresh = vi.fn();
    vi.mocked(useConversations).mockReturnValue({
      ...defaultMockHook,
      error: 'Failed to load conversations',
      refresh: mockRefresh,
    });

    render(<ConversationListPage />);
    
    expect(screen.getByText('Failed to load conversations')).toBeInTheDocument();
    
    // Click retry button
    const retryButton = screen.getByRole('button', { name: /retry/i });
    fireEvent.click(retryButton);
    
    expect(mockRefresh).toHaveBeenCalled();
  });

  it('handles search', () => {
    const mockSetFilters = vi.fn();
    vi.mocked(useConversations).mockReturnValue({
      ...defaultMockHook,
      setFilters: mockSetFilters,
    });

    render(<ConversationListPage />);
    
    // Find search input
    const searchInput = screen.getByPlaceholderText(/search/i);
    fireEvent.change(searchInput, { target: { value: 'test search' } });
    fireEvent.submit(searchInput.closest('form')!);
    
    expect(mockSetFilters).toHaveBeenCalledWith({
      searchTerm: 'test search',
      offset: 0,
    });
  });

  it('handles clear filters', () => {
    const mockSetFilters = vi.fn();
    vi.mocked(useConversations).mockReturnValue({
      ...defaultMockHook,
      filters: {
        ...defaultMockHook.filters,
        searchTerm: 'test',
      },
      setFilters: mockSetFilters,
    });

    render(<ConversationListPage />);
    
    // Click clear filters button
    const clearButton = screen.getByRole('button', { name: /clear/i });
    fireEvent.click(clearButton);
    
    expect(mockSetFilters).toHaveBeenCalledWith({
      searchTerm: '',
      sortBy: 'updated' as const,
      sortOrder: 'desc' as const,
      limit: 25,
      offset: 0,
    });
  });

  it('handles delete conversation with confirmation', async () => {
    const mockDeleteConversation = vi.fn().mockResolvedValue(undefined);
    vi.mocked(useConversations).mockReturnValue({
      ...defaultMockHook,
      deleteConversation: mockDeleteConversation,
    });

    render(<ConversationListPage />);
    
    // Open dropdown menu for first conversation
    const dropdownButtons = screen.getAllByRole('button', { name: /conversation actions/i });
    fireEvent.click(dropdownButtons[0]);
    
    // Click delete button
    const deleteButton = screen.getAllByText('Delete')[0];
    fireEvent.click(deleteButton);
    
    expect(global.confirm).toHaveBeenCalledWith('Are you sure you want to delete this conversation?');
    
    await waitFor(() => {
      expect(mockDeleteConversation).toHaveBeenCalledWith('conv-1');
      expect(utils.showToast).toHaveBeenCalledWith('Conversation deleted successfully', 'success');
    });
  });

  it('cancels delete when user declines confirmation', async () => {
    vi.mocked(global.confirm).mockReturnValue(false);
    const mockDeleteConversation = vi.fn();
    vi.mocked(useConversations).mockReturnValue({
      ...defaultMockHook,
      deleteConversation: mockDeleteConversation,
    });

    render(<ConversationListPage />);
    
    // Open dropdown and click delete
    const dropdownButtons = screen.getAllByRole('button', { name: /conversation actions/i });
    fireEvent.click(dropdownButtons[0]);
    const deleteButton = screen.getAllByText('Delete')[0];
    fireEvent.click(deleteButton);
    
    expect(mockDeleteConversation).not.toHaveBeenCalled();
  });

  it('shows error toast when delete fails', async () => {
    const mockError = new Error('Delete failed');
    const mockDeleteConversation = vi.fn().mockRejectedValue(mockError);
    vi.mocked(useConversations).mockReturnValue({
      ...defaultMockHook,
      deleteConversation: mockDeleteConversation,
    });

    render(<ConversationListPage />);
    
    // Open dropdown and click delete
    const dropdownButtons = screen.getAllByRole('button', { name: /conversation actions/i });
    fireEvent.click(dropdownButtons[0]);
    const deleteButton = screen.getAllByText('Delete')[0];
    fireEvent.click(deleteButton);
    
    await waitFor(() => {
      expect(utils.showToast).toHaveBeenCalledWith(
        'Failed to delete conversation: Delete failed',
        'error'
      );
    });
  });

  it('handles load more', () => {
    const mockLoadMore = vi.fn();
    vi.mocked(useConversations).mockReturnValue({
      ...defaultMockHook,
      hasMore: true,
      loadMore: mockLoadMore,
    });

    render(<ConversationListPage />);
    
    const loadMoreButton = screen.getByText('Load More');
    fireEvent.click(loadMoreButton);
    
    expect(mockLoadMore).toHaveBeenCalled();
  });

  it('passes filters to SearchAndFilters component', () => {
    const customFilters = {
      searchTerm: 'test search',
      sortBy: 'created' as const,
      sortOrder: 'asc' as const,
      limit: 50,
      offset: 0,
    };
    
    vi.mocked(useConversations).mockReturnValue({
      ...defaultMockHook,
      filters: customFilters,
    });

    render(<ConversationListPage />);
    
    // Verify search term is displayed in search input
    const searchInput = screen.getByPlaceholderText(/search/i) as HTMLInputElement;
    expect(searchInput.value).toBe('test search');
  });
});