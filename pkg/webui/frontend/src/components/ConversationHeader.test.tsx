import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import ConversationHeader from './ConversationHeader';
import { Conversation } from '../types';

describe('ConversationHeader', () => {
  const mockConversation: Conversation = {
    id: 'conv-1234567890',
    messages: [],
    toolResults: {},
    usage: {},
    createdAt: '2023-01-01T00:00:00Z',
    updatedAt: '2023-01-01T00:00:00Z',
    messageCount: 0,
    summary: 'This is a test conversation summary',
  };

  const mockOnExport = vi.fn();
  const mockOnDelete = vi.fn();

  const defaultProps = {
    conversation: mockConversation,
    onExport: mockOnExport,
    onDelete: mockOnDelete,
  };

  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('renders conversation ID truncated', () => {
    render(<ConversationHeader {...defaultProps} />);
    
    expect(screen.getByText('conv-123...')).toBeInTheDocument();
  });

  it('renders conversation summary', () => {
    render(<ConversationHeader {...defaultProps} />);
    
    expect(screen.getByText('This is a test conversation summary')).toBeInTheDocument();
  });

  it('renders default text when no summary', () => {
    const conversationWithoutSummary = {
      ...mockConversation,
      summary: undefined,
    };
    
    render(<ConversationHeader {...defaultProps} conversation={conversationWithoutSummary} />);
    
    expect(screen.getByText('No summary available')).toBeInTheDocument();
  });

  it('handles export button click', () => {
    render(<ConversationHeader {...defaultProps} />);
    
    const exportButton = screen.getByRole('button', { name: /export conversation/i });
    fireEvent.click(exportButton);
    
    expect(mockOnExport).toHaveBeenCalledTimes(1);
  });

  it('handles delete button click', () => {
    render(<ConversationHeader {...defaultProps} />);
    
    const deleteButton = screen.getByRole('button', { name: /delete conversation/i });
    fireEvent.click(deleteButton);
    
    expect(mockOnDelete).toHaveBeenCalledTimes(1);
  });

  it('disables buttons when conversation has no ID', () => {
    const conversationWithoutId = {
      ...mockConversation,
      id: '',
    };
    
    render(<ConversationHeader {...defaultProps} conversation={conversationWithoutId} />);
    
    const exportButton = screen.getByRole('button', { name: /export conversation/i });
    const deleteButton = screen.getByRole('button', { name: /delete conversation/i });
    
    expect(exportButton).toBeDisabled();
    expect(deleteButton).toBeDisabled();
  });

  it('shows loading text when conversation has no ID', () => {
    const conversationWithoutId = {
      ...mockConversation,
      id: '',
    };
    
    render(<ConversationHeader {...defaultProps} conversation={conversationWithoutId} />);
    
    expect(screen.getByText('Loading...')).toBeInTheDocument();
  });

  it('renders both export and delete buttons with correct styling', () => {
    render(<ConversationHeader {...defaultProps} />);
    
    const exportButton = screen.getByRole('button', { name: /export conversation/i });
    const deleteButton = screen.getByRole('button', { name: /delete conversation/i });
    
    expect(exportButton).toHaveClass('btn-primary');
    expect(deleteButton).toHaveClass('btn-error');
  });

  it('renders SVG icons in buttons', () => {
    const { container } = render(<ConversationHeader {...defaultProps} />);
    
    const svgElements = container.querySelectorAll('svg');
    expect(svgElements).toHaveLength(2); // One for export, one for delete
  });

  it('buttons are enabled when conversation has ID', () => {
    render(<ConversationHeader {...defaultProps} />);
    
    const exportButton = screen.getByRole('button', { name: /export conversation/i });
    const deleteButton = screen.getByRole('button', { name: /delete conversation/i });
    
    expect(exportButton).not.toBeDisabled();
    expect(deleteButton).not.toBeDisabled();
  });
});