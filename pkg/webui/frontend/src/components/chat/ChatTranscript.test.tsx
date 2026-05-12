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

    expect(screen.getByText('Thought')).toBeInTheDocument();
    expect(container.querySelector('details')).not.toHaveAttribute('open');
    expect(container.querySelector('.lucide-brain')).toBeInTheDocument();
    expect(container.querySelector('.activity-dot-thinking')).not.toBeInTheDocument();
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

  it('renders markdown lists from asterisk markers', () => {
    const { container } = render(
      <ChatTranscript
        isStreaming={false}
        messages={[
          {
            role: 'assistant',
            blocks: [
              {
                type: 'message',
                content: '* first item\n* second item',
              },
            ],
          },
        ]}
      />
    );

    const list = container.querySelector('.chat-prose ul');

    expect(list).toBeInTheDocument();
    expect(list).toHaveClass('chat-markdown-list');
    expect(screen.getByText('first item')).toBeInTheDocument();
    expect(screen.getByText('second item')).toBeInTheDocument();
  });

  it('renders long markdown links inside chat prose', () => {
    const longURL = `https://example.com/${'very-long-path-segment'.repeat(12)}`;
    const { container } = render(
      <ChatTranscript
        isStreaming={false}
        messages={[
          {
            role: 'assistant',
            blocks: [
              {
                type: 'message',
                content: `[${longURL}](${longURL})`,
              },
            ],
          },
        ]}
      />
    );

    const link = container.querySelector('.chat-prose a');

    expect(link).toBeInTheDocument();
    expect(link).toHaveClass('chat-markdown-link');
    expect(link).toHaveAttribute('href', longURL);
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
    expect(screen.getByText('Thinking')).toBeInTheDocument();
    expect(container.querySelector('.chat-streaming-mark')).toBeInTheDocument();

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

    expect(screen.getByText('Thought')).toBeInTheDocument();
    expect(container.querySelector('details')).not.toHaveAttribute('open');
  });

  it('uses the dust spinner and stable label on in-progress thinking blocks', () => {
    const { container } = render(
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

    expect(screen.getByText('Thinking')).toBeInTheDocument();
    expect(container.querySelector('.chat-streaming-mark')).toBeInTheDocument();
  });

  it('groups consecutive completed thinking blocks into one collapsible thoughts card', () => {
    const { container } = render(
      <ChatTranscript
        isStreaming={false}
        messages={[
          {
            role: 'assistant',
            blocks: [
              {
                type: 'thinking',
                content: 'First thought',
                inProgress: false,
              },
              {
                type: 'thinking',
                content: 'Second thought',
                inProgress: false,
              },
              {
                type: 'thinking',
                content: 'Third thought',
                inProgress: false,
              },
            ],
          },
        ]}
      />
    );

    expect(screen.getByText('3 Thoughts')).toBeInTheDocument();
    expect(screen.queryByText('thought 1')).not.toBeInTheDocument();
    expect(screen.queryByText('thought 2')).not.toBeInTheDocument();
    expect(screen.queryByText('thought 3')).not.toBeInTheDocument();
    expect(screen.getByText('First thought')).toBeInTheDocument();
    expect(screen.getByText('Second thought')).toBeInTheDocument();
    expect(screen.getByText('Third thought')).toBeInTheDocument();
    expect(container.querySelectorAll('details')).toHaveLength(1);
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

  it('does not start the fallback label timer while an activity block is visible', () => {
    const setIntervalSpy = vi.spyOn(window, 'setInterval');

    try {
      render(
        <ChatTranscript
          isStreaming={true}
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
                      input: '{"command":"sleep 10"}',
                    },
                  ],
                },
              ],
            },
          ]}
        />
      );

      expect(screen.queryByLabelText('Kodelet is working')).not.toBeInTheDocument();
      expect(setIntervalSpy).not.toHaveBeenCalled();
    } finally {
      setIntervalSpy.mockRestore();
    }
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

  it('renders streamed steering as a regular user block', () => {
    render(
      <ChatTranscript
        isStreaming={false}
        messages={[
          {
            role: 'user',
            content: [
              { type: 'text', text: 'Use this image' },
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

    expect(screen.getByText('You')).toBeInTheDocument();
    expect(screen.getByText('Use this image')).toBeInTheDocument();
    expect(screen.queryByText(/User steering/)).not.toBeInTheDocument();
    expect(screen.getByAltText('Uploaded content')).toHaveAttribute(
      'src',
      'data:image/png;base64,aGVsbG8='
    );
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

    expect(screen.getAllByTitle('Bash: rg -n "ChatTranscript" pkg/webui/frontend/src')).not.toHaveLength(0)
    expect(
      screen.getByText(
        'Read file: /home/jingkaihe/workspace/kodelet/pkg/webui/frontend/src/components/chat/ChatTranscript.tsx'
      )
    ).toBeInTheDocument()
    expect(screen.queryByText('Tools (2)')).not.toBeInTheDocument()
    expect(container.querySelectorAll('details')).toHaveLength(2)
    expect(container.querySelector('.tool-summary-text')?.textContent).not.toContain('timeout')
  })

  it('uses icons instead of text labels for common developer tools', () => {
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
                    input: '{"command":"pwd"}',
                  },
                  {
                    callId: 'patch-1',
                    name: 'apply_patch',
                    input: '{"input":"*** Begin Patch\\n*** Update File: README.md\\n*** End Patch"}',
                  },
                  {
                    callId: 'skill-1',
                    name: 'skill',
                    input: '{"skill_name":"frontend-design"}',
                  },
                ],
              },
            ],
          },
        ]}
      />
    )

    expect(container.querySelector('.lucide-square-terminal')).toBeInTheDocument()
    expect(container.querySelector('.lucide-pencil')).toBeInTheDocument()
    expect(container.querySelector('.lucide-pocket-knife')).toBeInTheDocument()
  })

  it('uses the tool icon as the running status marker without adding a dot', () => {
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
                    input: '{"command":"sleep 10"}',
                  },
                ],
              },
            ],
          },
        ]}
      />
    )

    expect(screen.getByLabelText('Tool running')).toHaveTextContent('running')
    expect(container.querySelector('.activity-dot')).not.toBeInTheDocument()
    expect(container.querySelector('.tool-summary-icon-running')).toBeInTheDocument()
  })

  it('keeps long running tool input previews compact', () => {
    const longPrompt = 'x'.repeat(700)
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
                    callId: 'web-fetch-1',
                    name: 'web_fetch',
                    input: JSON.stringify({ url: 'https://example.com/news', prompt: longPrompt }),
                  },
                ],
              },
            ],
          },
        ]}
      />
    )

    expect(container.querySelector('.running-tool-input-preview')).toBeInTheDocument()
    expect(screen.getByText(/more characters/)).toBeInTheDocument()
    expect(screen.queryByText(longPrompt)).not.toBeInTheDocument()
  })

  it('uses bash duration as the completed activity status', () => {
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
                    input: '{"command":"pwd","description":"Print working directory"}',
                    result: {
                      toolName: 'bash',
                      success: true,
                      metadata: {
                        command: 'pwd',
                        exitCode: 0,
                        output: '/workspace/kodelet',
                        executionTime: 119000000,
                        workingDir: '/workspace/kodelet',
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

    expect(screen.getByLabelText('Tool 119ms')).toHaveTextContent('119ms')
    expect(screen.queryByLabelText('Tool done')).not.toBeInTheDocument()
    expect(container.querySelector('.activity-dot')).not.toBeInTheDocument()
  })

  it('uses failed status for unsuccessful bash results with duration metadata', () => {
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
                    input: '{"command":"false","description":"Run failing command"}',
                    result: {
                      toolName: 'bash',
                      success: false,
                      metadata: {
                        command: 'false',
                        exitCode: 1,
                        output: '',
                        executionTime: 119000000,
                        workingDir: '/workspace/kodelet',
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

    expect(screen.getByLabelText('Tool failed')).toHaveTextContent('failed')
    expect(container.querySelector('.activity-dot')).not.toBeInTheDocument()
    expect(container.querySelector('.tool-summary-icon-error')).toBeInTheDocument()
    expect(screen.queryByLabelText('Tool 119ms')).not.toBeInTheDocument()
  })
});
