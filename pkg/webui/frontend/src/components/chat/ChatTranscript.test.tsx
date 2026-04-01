import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import ChatTranscript from './ChatTranscript';

describe('ChatTranscript', () => {
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
    render(
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
      'group-hover:opacity-100',
      'group-focus-within:opacity-100'
    );
    expect(screen.queryByRole('button', { name: 'Copy' })).not.toBeInTheDocument();
  });
});
