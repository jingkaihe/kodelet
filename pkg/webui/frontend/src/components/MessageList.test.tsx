import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import MessageList from './MessageList';
import { Message, ToolResult } from '../types';
import * as utils from '../utils';

// Mock marked
vi.mock('marked', () => ({
  marked: {
    parse: (text: string) => `<p>${text}</p>`,
  },
}));

// Mock copyToClipboard
vi.mock('../utils', async () => {
  const actual = await vi.importActual('../utils');
  return {
    ...actual,
    copyToClipboard: vi.fn(),
  };
});

// Mock ToolRenderer
vi.mock('./ToolRenderer', () => ({
  default: ({ toolResult }: { toolResult: ToolResult }) => (
    <div data-testid="tool-renderer">Tool Result: {toolResult.toolName}</div>
  ),
}));

describe('MessageList', () => {
  const mockMessages: Message[] = [
    {
      role: 'user',
      content: 'Hello world',
      toolCalls: [],
    },
    {
      role: 'assistant',
      content: 'Hi there! How can I help you?',
      toolCalls: [
        {
          id: 'tool-1',
          function: {
            name: 'search',
            arguments: '{"query": "test"}',
          },
        },
      ],
      thinkingText: 'Let me think about this...',
    },
  ];

  const mockToolResults: Record<string, ToolResult> = {
    'tool-1': {
      toolName: 'search',
      success: true,
      error: undefined,
      timestamp: '2023-01-01T00:00:00Z',
    },
  };

  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('renders messages correctly', () => {
    render(<MessageList messages={mockMessages} toolResults={{}} />);
    
    expect(screen.getByText('Hello world')).toBeInTheDocument();
    expect(screen.getByText('Hi there! How can I help you?')).toBeInTheDocument();
  });

  it('displays user and assistant labels correctly', () => {
    render(<MessageList messages={mockMessages} toolResults={{}} />);
    
    expect(screen.getByText('You')).toBeInTheDocument();
    expect(screen.getByText('Assistant')).toBeInTheDocument();
  });

  it('shows message numbers', () => {
    render(<MessageList messages={mockMessages} toolResults={{}} />);
    
    expect(screen.getByText('Message 1')).toBeInTheDocument();
    expect(screen.getByText('Message 2')).toBeInTheDocument();
  });

  it('handles copy message functionality', () => {
    render(<MessageList messages={mockMessages} toolResults={{}} />);
    
    const copyButtons = screen.getAllByRole('button', { name: /copy message/i });
    fireEvent.click(copyButtons[0]);
    
    expect(utils.copyToClipboard).toHaveBeenCalledWith('Hello world');
  });

  it('renders thinking text with toggle', () => {
    render(<MessageList messages={mockMessages} toolResults={{}} />);
    
    // Thinking block should be visible by default
    expect(screen.getByText('💭 Thinking')).toBeInTheDocument();
    expect(screen.getByText('Let me think about this...')).toBeInTheDocument();
    
    // Toggle to hide
    const toggleButton = screen.getByRole('button', { name: /toggle thinking block/i });
    fireEvent.click(toggleButton);
    
    // Thinking text should be hidden
    expect(screen.queryByText('Let me think about this...')).not.toBeInTheDocument();
    
    // Toggle to show again
    fireEvent.click(toggleButton);
    expect(screen.getByText('Let me think about this...')).toBeInTheDocument();
  });

  it('trims leading and trailing whitespace from thinking text', () => {
    const messageWithWhitespace: Message = {
      role: 'assistant',
      content: 'Test response',
      toolCalls: [],
      thinkingText: '\n\n  This has leading and trailing whitespace  \n\n',
    };

    render(<MessageList messages={[messageWithWhitespace]} toolResults={{}} />);
    
    // Should display trimmed text
    expect(screen.getByText('This has leading and trailing whitespace')).toBeInTheDocument();
    // Should not display the original text with whitespace
    expect(screen.queryByText('\n\n  This has leading and trailing whitespace  \n\n')).not.toBeInTheDocument();
  });

  it('trims newlines from thinking text', () => {
    const messageWithNewlines: Message = {
      role: 'assistant',
      content: 'Test response',
      toolCalls: [],
      thinkingText: '\n\nI need to analyze this carefully.\n\nLet me break it down:\n1. First step\n2. Second step\n\n',
    };

    render(<MessageList messages={[messageWithNewlines]} toolResults={{}} />);
    
    // Should display trimmed text (leading/trailing newlines removed, internal preserved)
    // Check that the text starts with the expected content without leading newlines
    expect(screen.getByText(/^I need to analyze this carefully\./)).toBeInTheDocument();
    
    // Check that it contains the list items
    expect(screen.getByText(/1\. First step/)).toBeInTheDocument();
    expect(screen.getByText(/2\. Second step/)).toBeInTheDocument();
  });

  it('handles thinking text that is only whitespace', () => {
    const messageWithOnlyWhitespace: Message = {
      role: 'assistant',
      content: 'Test response',
      toolCalls: [],
      thinkingText: '\n\n   \t   \n\n',
    };

    render(<MessageList messages={[messageWithOnlyWhitespace]} toolResults={{}} />);
    
    // Should display empty string after trimming in the pre element
    const preElement = screen.getByText((content, element) => {
      return element?.tagName.toLowerCase() === 'pre' && content === '';
    });
    
    expect(preElement).toBeInTheDocument();
  });

  it('does not affect thinking text without leading/trailing whitespace', () => {
    const messageWithoutWhitespace: Message = {
      role: 'assistant',
      content: 'Test response',
      toolCalls: [],
      thinkingText: 'Clean thinking text with no whitespace',
    };

    render(<MessageList messages={[messageWithoutWhitespace]} toolResults={{}} />);
    
    // Should display the original text unchanged
    expect(screen.getByText('Clean thinking text with no whitespace')).toBeInTheDocument();
  });

  it('renders tool calls with toggle', () => {
    render(<MessageList messages={mockMessages} toolResults={mockToolResults} />);
    
    // Tool calls section should be visible
    expect(screen.getByText('Tool Calls:')).toBeInTheDocument();
    expect(screen.getByText('search')).toBeInTheDocument();
    expect(screen.getByText('tool-1')).toBeInTheDocument();
    
    // Tool call details should be expanded by default
    expect(screen.getByText('Arguments')).toBeInTheDocument();
    expect(screen.getByText('Result')).toBeInTheDocument();
  });

  it('toggles tool call details', () => {
    render(<MessageList messages={mockMessages} toolResults={mockToolResults} />);
    
    const toggleButton = screen.getByRole('button', { name: /toggle tool call details/i });
    
    // Initially expanded
    expect(screen.getByText('Arguments')).toBeInTheDocument();
    
    // Toggle to collapse
    fireEvent.click(toggleButton);
    expect(screen.queryByText('Arguments')).not.toBeInTheDocument();
    
    // Toggle to expand
    fireEvent.click(toggleButton);
    expect(screen.getByText('Arguments')).toBeInTheDocument();
  });

  it('toggles arguments visibility', () => {
    render(<MessageList messages={mockMessages} toolResults={mockToolResults} />);
    
    const toggleButton = screen.getByRole('button', { name: /toggle arguments/i });
    
    // Arguments should be hidden by default
    expect(screen.queryByText('"query": "test"')).not.toBeInTheDocument();
    
    // Toggle to show
    fireEvent.click(toggleButton);
    expect(screen.getByText(/query.*test/)).toBeInTheDocument();
  });

  it('toggles results visibility', () => {
    render(<MessageList messages={mockMessages} toolResults={mockToolResults} />);
    
    // Results should be expanded by default
    const toolRendererContent = screen.getByTestId('tool-renderer');
    expect(toolRendererContent).toBeInTheDocument();
    
    const toggleButton = screen.getByRole('button', { name: /toggle results/i });
    
    // Toggle to hide
    fireEvent.click(toggleButton);
    expect(screen.queryByTestId('tool-renderer')).not.toBeInTheDocument();
  });

  it('handles messages with array content blocks', () => {
    const multimodalMessage: Message = {
      role: 'user',
      content: [
        { type: 'text', text: 'Check this image:' },
        { type: 'image', source: { data: 'data:image/png;base64,test', media_type: 'image/png' } },
      ],
      toolCalls: [],
    };

    render(<MessageList messages={[multimodalMessage]} toolResults={{}} />);
    
    expect(screen.getByText('Check this image:')).toBeInTheDocument();
    expect(screen.getByRole('img')).toHaveAttribute('src', 'data:image/png;base64,test');
  });

  it('handles empty messages array', () => {
    render(<MessageList messages={[]} toolResults={{}} />);
    
    // Should render without errors
    expect(screen.queryByText('You')).not.toBeInTheDocument();
  });

  it('handles tool calls with missing function data', () => {
    const messageWithBadToolCall: Message = {
      role: 'assistant',
      content: 'Test',
      toolCalls: [
        {
          id: 'tool-2',
          function: undefined as unknown as { name: string; arguments: string },
        },
      ],
    };

    render(<MessageList messages={[messageWithBadToolCall]} toolResults={{}} />);
    
    expect(screen.getByText('Unknown')).toBeInTheDocument();
  });

  it('handles legacy tool_calls property', () => {
    const messageWithLegacyToolCalls: Message = {
      role: 'assistant',
      content: 'Test',
      tool_calls: [
        {
          id: 'tool-3',
          function: {
            name: 'legacy_tool',
            arguments: '{}',
          },
        },
      ] as Array<{ id: string; function: { name: string; arguments: string } }>,
    };

    render(<MessageList messages={[messageWithLegacyToolCalls]} toolResults={{}} />);
    
    expect(screen.getByText('legacy_tool')).toBeInTheDocument();
  });

  it('applies correct styling for user and assistant messages', () => {
    const { container } = render(<MessageList messages={mockMessages} toolResults={{}} />);
    
    const messageCards = container.querySelectorAll('.card');
    
    // User message should have message-user styling
    expect(messageCards[0]).toHaveClass('message-user');
    
    // Assistant message should have message-assistant styling
    expect(messageCards[1]).toHaveClass('message-assistant');
  });

  it('handles content blocks with image_url format', () => {
    const imageMessage: Message = {
      role: 'user',
      content: [
        { type: 'text', text: 'Image:' },
        { type: 'image', image_url: { url: 'https://example.com/image.png' } },
      ],
      toolCalls: [],
    };

    render(<MessageList messages={[imageMessage]} toolResults={{}} />);
    
    const img = screen.getByRole('img');
    expect(img).toHaveAttribute('src', 'https://example.com/image.png');
  });

  it('copies array content as JSON when copying message', () => {
    const arrayContentMessage: Message = {
      role: 'user',
      content: [
        { type: 'text', text: 'Multiple blocks' },
      ],
      toolCalls: [],
    };

    render(<MessageList messages={[arrayContentMessage]} toolResults={{}} />);
    
    const copyButton = screen.getByRole('button', { name: /copy message/i });
    fireEvent.click(copyButton);
    
    expect(utils.copyToClipboard).toHaveBeenCalledWith(
      JSON.stringify(arrayContentMessage.content, null, 2)
    );
  });

  it('preserves expanded state for multiple tool calls', () => {
    const messageWithMultipleTools: Message = {
      role: 'assistant',
      content: 'Multiple tools',
      toolCalls: [
        {
          id: 'tool-a',
          function: { name: 'tool_a', arguments: '{}' },
        },
        {
          id: 'tool-b',
          function: { name: 'tool_b', arguments: '{}' },
        },
      ],
    };

    render(<MessageList messages={[messageWithMultipleTools]} toolResults={{}} />);
    
    // Both tool calls should be expanded by default
    const badges = screen.getAllByText(/tool_[ab]/);
    expect(badges).toHaveLength(2);
  });
});