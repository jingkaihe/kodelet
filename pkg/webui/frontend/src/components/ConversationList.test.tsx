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
    provider: 'OpenAI',
    platform: 'fireworks',
    api_mode: 'chat_completions',
  },
];

describe('ConversationList', () => {
  const defaultProps = {
    conversations: mockConversations,
    loading: false,
    currentPage: 1,
    totalPages: 1,
    onPageChange: vi.fn(),
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
    expect(screen.getByText('OpenAI')).toBeInTheDocument();
    expect(screen.getByText('fireworks')).toBeInTheDocument();
    expect(screen.getByText('chat_completions')).toBeInTheDocument();
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

  it('provides pagination when multiple pages available', () => {
    const onPageChange = vi.fn();
    render(<ConversationList {...defaultProps} totalPages={3} onPageChange={onPageChange} />);

    const nextButton = screen.getByRole('button', { name: /next page/i });
    fireEvent.click(nextButton);

    expect(onPageChange).toHaveBeenCalledWith(2);
  });

  it('shows loading state during pagination', () => {
    render(<ConversationList {...defaultProps} totalPages={3} loading={true} />);

    // Check if pagination buttons are disabled during loading
    const nextButton = screen.getByRole('button', { name: /next page/i });
    expect(nextButton).toBeDisabled();
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
