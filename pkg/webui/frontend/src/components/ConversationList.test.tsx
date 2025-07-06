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
    created_at: '2023-01-03T00:00:00Z', // Test alternative property name
    updated_at: '2023-01-04T00:00:00Z',
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

  it('renders conversations list', () => {
    render(<ConversationList {...defaultProps} />);

    expect(screen.getByText('conv-123')).toBeInTheDocument();
    expect(screen.getByText('conv-456')).toBeInTheDocument();
    expect(screen.getByText('This is a test conversation preview')).toBeInTheDocument();
    expect(screen.getByText('Another test conversation summary')).toBeInTheDocument();
  });

  it('displays message counts', () => {
    render(<ConversationList {...defaultProps} />);

    expect(screen.getByText('5')).toBeInTheDocument();
    expect(screen.getByText('3')).toBeInTheDocument();
  });

  it('formats dates correctly', () => {
    render(<ConversationList {...defaultProps} />);

    // Should contain formatted dates
    expect(screen.getAllByText(/Created:/)).toHaveLength(2);
    expect(screen.getAllByText(/Updated:/)).toHaveLength(2);
  });

  it('displays model type when available', () => {
    render(<ConversationList {...defaultProps} />);

    expect(screen.getByText('gpt-4')).toBeInTheDocument();
  });

  it('shows load more button when hasMore is true', () => {
    render(<ConversationList {...defaultProps} hasMore={true} />);

    const loadMoreButton = screen.getByText('Load More');
    expect(loadMoreButton).toBeInTheDocument();
    expect(loadMoreButton).not.toBeDisabled();
  });

  it('calls onLoadMore when button is clicked', () => {
    const onLoadMore = vi.fn();
    render(<ConversationList {...defaultProps} hasMore={true} onLoadMore={onLoadMore} />);

    fireEvent.click(screen.getByText('Load More'));
    expect(onLoadMore).toHaveBeenCalledTimes(1);
  });

  it('disables load more button when loading', () => {
    render(<ConversationList {...defaultProps} hasMore={true} loading={true} />);

    const loadMoreButton = screen.getByRole('button', { name: /loading/i });
    expect(loadMoreButton).toBeDisabled();
  });

  it('shows loading spinner when loading more conversations', () => {
    render(<ConversationList {...defaultProps} loading={true} />);

    expect(screen.getByText('Loading more conversations...')).toBeInTheDocument();
  });

  it('handles delete action from dropdown menu', () => {
    const onDelete = vi.fn();
    render(<ConversationList {...defaultProps} onDelete={onDelete} />);

    // Open first dropdown
    const dropdownButtons = screen.getAllByRole('button', { name: /conversation actions/i });
    fireEvent.click(dropdownButtons[0]);

    // Click delete button
    const deleteButtons = screen.getAllByText('Delete');
    fireEvent.click(deleteButtons[0]);

    expect(onDelete).toHaveBeenCalledWith('conv-123');
  });

  it('prevents navigation when delete is clicked', () => {
    const onDelete = vi.fn();
    render(<ConversationList {...defaultProps} onDelete={onDelete} />);

    // Open dropdown
    const dropdownButtons = screen.getAllByRole('button', { name: /conversation actions/i });
    fireEvent.click(dropdownButtons[0]);

    // Create a mock event with preventDefault and stopPropagation
    const deleteButton = screen.getAllByText('Delete')[0];
    const mockEvent = new MouseEvent('click', { bubbles: true });
    const preventDefaultSpy = vi.spyOn(mockEvent, 'preventDefault');
    const stopPropagationSpy = vi.spyOn(mockEvent, 'stopPropagation');

    Object.defineProperty(deleteButton, 'onclick', {
      value: (e: MouseEvent) => {
        e.preventDefault();
        e.stopPropagation();
        onDelete('conv-123');
      },
    });

    deleteButton.dispatchEvent(mockEvent);

    expect(preventDefaultSpy).toHaveBeenCalled();
    expect(stopPropagationSpy).toHaveBeenCalled();
  });

  it('renders view links in dropdown', () => {
    render(<ConversationList {...defaultProps} />);

    // Open dropdown
    const dropdownButtons = screen.getAllByRole('button', { name: /conversation actions/i });
    fireEvent.click(dropdownButtons[0]);

    const viewLinks = screen.getAllByText('View');
    expect(viewLinks[0]).toHaveAttribute('href', '/c/conv-123');
  });

  it('handles conversations without preview or summary', () => {
    const conversationsWithoutPreview = [
      {
        ...mockConversations[0],
        preview: undefined,
        summary: undefined,
      },
    ];

    render(<ConversationList {...defaultProps} conversations={conversationsWithoutPreview} />);

    expect(screen.getByText('No preview available')).toBeInTheDocument();
  });

  it('renders empty list when no conversations', () => {
    render(<ConversationList {...defaultProps} conversations={[]} />);

    expect(screen.queryByText('conv-123')).not.toBeInTheDocument();
    expect(screen.queryByText('Load More')).not.toBeInTheDocument();
  });

  it('applies hover effect on conversation cards', () => {
    render(<ConversationList {...defaultProps} />);

    const cards = document.querySelectorAll('.card');
    expect(cards[0]).toHaveClass('hover:shadow-xl');
  });
});