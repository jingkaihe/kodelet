import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent } from '../test/utils';
import ConversationList from './ConversationList';
import { Conversation } from '../types';

const mockConversations: Conversation[] = [
  {
    id: 'conv-123',
    messages: [],
    toolResults: {},
    usage: {},
    createdAt: '2023-01-01T00:00:00Z',
    updatedAt: '2023-01-02T00:00:00Z',
    messageCount: 5,
    preview: 'This is a test conversation preview',
  },
  {
    id: 'conv-456',
    messages: [],
    toolResults: {},
    usage: {},
    createdAt: '2023-01-03T00:00:00Z',
    updatedAt: '2023-01-04T00:00:00Z',
    messageCount: 3,
    summary: 'Another test conversation summary',
    modelType: 'gpt-4',
  },
];

describe('ConversationList', () => {
  const defaultProps = {
    conversations: mockConversations,
    loading: false,
    hasMore: false,
    onLoadMore: vi.fn(),
    onDelete: vi.fn(),
  };

  it('displays list of conversations with key information', () => {
    render(<ConversationList {...defaultProps} />);

    // Verify conversations are displayed
    expect(screen.getByText('conv-123')).toBeInTheDocument();
    expect(screen.getByText('conv-456')).toBeInTheDocument();
    
    // Verify previews/summaries
    expect(screen.getByText('This is a test conversation preview')).toBeInTheDocument();
    expect(screen.getByText('Another test conversation summary')).toBeInTheDocument();
    
    // Verify metadata
    expect(screen.getByText('5')).toBeInTheDocument();
    expect(screen.getByText('3')).toBeInTheDocument();
    expect(screen.getByText('gpt-4')).toBeInTheDocument();
  });

  it('handles empty conversation list', () => {
    render(<ConversationList {...defaultProps} conversations={[]} />);

    expect(screen.queryByText('conv-123')).not.toBeInTheDocument();
  });

  it('shows placeholder for conversations without preview', () => {
    const conversationsWithoutPreview = [{
      ...mockConversations[0],
      preview: undefined,
      summary: undefined,
    }];

    render(<ConversationList {...defaultProps} conversations={conversationsWithoutPreview} />);
    expect(screen.getByText('No preview available')).toBeInTheDocument();
  });

  it('provides pagination when more conversations available', () => {
    const onLoadMore = vi.fn();
    render(<ConversationList {...defaultProps} hasMore={true} onLoadMore={onLoadMore} />);

    const loadMoreButton = screen.getByText('Load More');
    fireEvent.click(loadMoreButton);
    
    expect(onLoadMore).toHaveBeenCalledTimes(1);
  });

  it('shows loading state during pagination', () => {
    render(<ConversationList {...defaultProps} hasMore={true} loading={true} />);

    expect(screen.getByRole('button', { name: /loading/i })).toBeDisabled();
    expect(screen.getByText('Loading more conversations...')).toBeInTheDocument();
  });

  it('allows user to delete conversation', () => {
    const onDelete = vi.fn();
    render(<ConversationList {...defaultProps} onDelete={onDelete} />);

    // Open dropdown menu
    const dropdownButton = screen.getAllByRole('button', { name: /conversation actions/i })[0];
    fireEvent.click(dropdownButton);

    // Click delete
    fireEvent.click(screen.getAllByText('Delete')[0]);
    
    expect(onDelete).toHaveBeenCalledWith('conv-123');
  });

  it('provides navigation to view conversation details', () => {
    render(<ConversationList {...defaultProps} />);

    // Open dropdown menu
    const dropdownButton = screen.getAllByRole('button', { name: /conversation actions/i })[0];
    fireEvent.click(dropdownButton);

    const viewLink = screen.getAllByText('View')[0];
    expect(viewLink).toHaveAttribute('href', '/c/conv-123');
  });
});