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

  it('renders thinking blocks as markdown', () => {
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
                inProgress: true,
              },
            ],
          },
        ]}
      />
    );

    expect(screen.getByText('Plan')).toBeInTheDocument();
    expect(container.querySelector('strong')?.textContent).toBe('Plan');
    expect(screen.getByText('inspect repo')).toBeInTheDocument();
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
});
