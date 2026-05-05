import { fireEvent, render, screen } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import ChatTranscript from './ChatTranscript';

const { copyToClipboardMock } = vi.hoisted(() => ({
  copyToClipboardMock: vi.fn(),
}));

vi.mock('../../utils', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../utils')>();
  return {
    ...actual,
    copyToClipboard: copyToClipboardMock,
  };
});

describe('ChatTranscript', () => {
  beforeEach(() => {
    copyToClipboardMock.mockReset();
  });

  it('renders the supplied empty-state greeting', () => {
    render(
      <ChatTranscript
        emptyStateTitle="Good afternoon"
        isStreaming={false}
        messages={[]}
      />
    );

    expect(screen.getByText('Good afternoon')).toBeInTheDocument();
  });

  it('renders completed thinking blocks collapsed by default', () => {
    const { container } = render(
      <ChatTranscript
        isStreaming={false}
        messages={[
          {
            role: 'assistant',
            blocks: [
              {
                type: 'thinking',
                content: '**Plan**\n\n- inspect repo',
                inProgress: false,
              },
            ],
          },
        ]}
      />
    );

    expect(screen.getByText('Thinking')).toBeInTheDocument();
    expect(container.querySelector('details')).not.toHaveAttribute('open');
    expect(screen.getByText('Plan')).toBeInTheDocument();
    expect(container.querySelector('strong')?.textContent).toBe('Plan');
    expect(screen.getByText('inspect repo')).toBeInTheDocument();
  });

  it('normalizes persisted thinking markdown so summary headings are split onto separate paragraphs', () => {
    const { container } = render(
      <ChatTranscript
        isStreaming={false}
        messages={[
          {
            role: 'assistant',
            blocks: [
              {
                type: 'thinking',
                content:
                  'I can use sentence case and prettify the tool names.**Improving tool text clarity**\n\nI should focus on the essence.',
                inProgress: false,
              },
            ],
          },
        ]}
      />
    );

    const paragraphs = container.querySelectorAll('details .chat-prose p');

    expect(paragraphs).toHaveLength(3);
    expect(paragraphs[0]?.textContent).toBe(
      'I can use sentence case and prettify the tool names.'
    );
    expect(paragraphs[1]?.textContent).toBe('Improving tool text clarity');
    expect(paragraphs[2]?.textContent).toBe('I should focus on the essence.');
  });

  it('renders inline backtick markdown as inline code within prose', () => {
    const { container } = render(
      <ChatTranscript
        isStreaming={false}
        messages={[
          {
            role: 'assistant',
            blocks: [
              {
                type: 'message',
                content:
                  'Run `go test ./pkg/webui -run TestServer_convertToWebMessages -count=1` and inspect `pkg/webui/server.go`.',
              },
            ],
          },
        ]}
      />
    );

    const paragraph = container.querySelector('.chat-prose p');
    const codeSpans = container.querySelectorAll('.chat-prose p code');

    expect(paragraph).toBeInTheDocument();
    expect(codeSpans).toHaveLength(2);
    expect(codeSpans[0]?.textContent).toBe(
      'go test ./pkg/webui -run TestServer_convertToWebMessages -count=1'
    );
    expect(codeSpans[1]?.textContent).toBe('pkg/webui/server.go');
    expect(container.querySelector('.chat-prose pre')).toBeNull();
  });

  it('auto-collapses a thinking block when streaming finishes', () => {
    const { container, rerender } = render(
      <ChatTranscript
        isStreaming={true}
        messages={[
          {
            role: 'assistant',
            blocks: [
              {
                type: 'thinking',
                content: 'Inspecting the repo',
                inProgress: true,
              },
            ],
          },
        ]}
      />
    );

    expect(container.querySelector('details')).toBeNull();
    expect(screen.getByText('Following the thread…')).toBeInTheDocument();

    rerender(
      <ChatTranscript
        isStreaming={false}
        messages={[
          {
            role: 'assistant',
            blocks: [
              {
                type: 'thinking',
                content: 'Inspecting the repo',
                inProgress: false,
              },
            ],
          },
        ]}
      />
    );

    expect(screen.getByText('Thinking')).toBeInTheDocument();
    expect(container.querySelector('details')).not.toHaveAttribute('open');
  });

  it('uses the rotating streaming label on in-progress thinking blocks', () => {
    render(
      <ChatTranscript
        isStreaming={true}
        messages={[
          {
            role: 'assistant',
            blocks: [
              {
                type: 'thinking',
                content: 'Inspecting the repo',
                inProgress: true,
              },
            ],
          },
        ]}
      />
    );

    expect(screen.getByText('Following the thread…')).toBeInTheDocument();
  });

  it('shows a streaming thinking indicator between assistant blocks', () => {
    render(
      <ChatTranscript
        isStreaming={true}
        messages={[
          {
            role: 'assistant',
            blocks: [
              {
                type: 'tools',
                tools: [],
              },
            ],
          },
        ]}
      />
    );

    expect(screen.getByLabelText('Kodelet is working')).toBeInTheDocument();
    expect(screen.getByText('Following the thread…')).toBeInTheDocument();
  });

  it('renders embedded base64 images in user content', () => {
    const { container } = render(
      <ChatTranscript
        isStreaming={false}
        messages={[
          {
            role: 'user',
            content: [
              { type: 'text', text: 'what is in the image?' },
              {
                type: 'image',
                source: {
                  data: 'aGVsbG8=',
                  media_type: 'image/png',
                },
              },
            ],
          },
        ]}
      />
    );

    expect(screen.getByAltText('Uploaded content')).toHaveAttribute(
      'src',
      'data:image/png;base64,aGVsbG8='
    );
    expect(container.querySelector('.chat-uploaded-image-media')).toBeInTheDocument();
  });

  it('uses an icon-only copy button with an accessible label', () => {
    render(
      <ChatTranscript
        isStreaming={false}
        messages={[
          {
            role: 'assistant',
            blocks: [
              {
                type: 'message',
                content: 'Ready to copy',
              },
            ],
          },
        ]}
      />
    );

    const button = screen.getByRole('button', { name: 'Copy to clipboard' });

    expect(button).toBeInTheDocument();
    expect(button).toHaveClass(
      'opacity-0',
      'transition-opacity',
      'group-hover/message:opacity-100',
      'group-focus-within/message:opacity-100'
    );
    expect(screen.queryByRole('button', { name: 'Copy' })).not.toBeInTheDocument();
  });

  it('copies each assistant message block independently', () => {
    render(
      <ChatTranscript
        isStreaming={false}
        messages={[
          {
            role: 'assistant',
            blocks: [
              {
                type: 'message',
                content: 'First assistant message',
              },
              {
                type: 'tools',
                tools: [
                  {
                    callId: 'bash-1',
                    name: 'bash',
                    input: '{"command":"pwd"}',
                  },
                ],
              },
              {
                type: 'message',
                content: 'Second assistant message',
              },
            ],
          },
        ]}
      />
    );

    const copyButtons = screen.getAllByRole('button', { name: 'Copy to clipboard' });

    expect(copyButtons).toHaveLength(2);

    fireEvent.click(copyButtons[1]);

    expect(copyToClipboardMock).toHaveBeenCalledWith('Second assistant message');
    expect(copyToClipboardMock).not.toHaveBeenCalledWith(
      expect.stringContaining('First assistant message')
    );
    expect(copyToClipboardMock).not.toHaveBeenCalledWith(expect.stringContaining('Bash'));
  });

  it('uses a friendly transcript label for native OpenAI search tool calls', () => {
    const { container } = render(
      <ChatTranscript
        isStreaming={false}
        messages={[
          {
            role: 'assistant',
            blocks: [
              {
                type: 'tools',
                tools: [
                  {
                    callId: 'search-1',
                    name: 'openai_web_search',
                    input: '{"type":"search","queries":["kodelet web ui"]}',
                    result: {
                      toolName: 'openai_web_search',
                      success: true,
                      metadata: {
                        status: 'completed',
                        action: 'search',
                        queries: ['kodelet web ui'],
                      },
                    },
                  },
                ],
              },
            ],
          },
        ]}
      />
    )

    expect(screen.getByText('Web search: kodelet web ui')).toBeInTheDocument()
    expect(screen.queryByText('Tools (1)')).not.toBeInTheDocument()
    expect(container.querySelectorAll('details')).toHaveLength(1)
  })

  it('renders each tool as its own collapsible row with concise summaries', () => {
    const { container } = render(
      <ChatTranscript
        isStreaming={false}
        messages={[
          {
            role: 'assistant',
            blocks: [
              {
                type: 'tools',
                tools: [
                  {
                    callId: 'bash-1',
                    name: 'bash',
                    input: '{"command":"rg -n \\\"ChatTranscript\\\" pkg/webui/frontend/src","timeout":30,"description":"Search transcript component"}',
                  },
                  {
                    callId: 'read-1',
                    name: 'file_read',
                    input: '{"file_path":"/home/jingkaihe/workspace/kodelet/pkg/webui/frontend/src/components/chat/ChatTranscript.tsx","offset":1,"line_limit":200}',
                  },
                ],
              },
            ],
          },
        ]}
      />
    )

    expect(screen.getByText('Bash: rg -n "ChatTranscript" pkg/webui/frontend/src')).toBeInTheDocument()
    expect(
      screen.getByText(
        'Read file: /home/jingkaihe/workspace/kodelet/pkg/webui/frontend/src/components/chat/ChatTranscript.tsx'
      )
    ).toBeInTheDocument()
    expect(screen.queryByText('Tools (2)')).not.toBeInTheDocument()
    expect(container.querySelectorAll('details')).toHaveLength(2)
    expect(container.querySelector('.tool-summary-text')?.textContent).not.toContain('timeout')
  })
});
