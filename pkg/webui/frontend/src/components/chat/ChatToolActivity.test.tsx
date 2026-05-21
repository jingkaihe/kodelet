import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import type { ChatRenderToolCall } from '../../types';
import ChatToolActivity, {
  formatToolInputPreview,
  getToolActivityStatus,
  getToolSummary,
} from './ChatToolActivity';

describe('ChatToolActivity', () => {
  it('renders a running tool with a compact input preview', () => {
    const longPrompt = 'x'.repeat(700);
    const { container } = render(
      <ChatToolActivity
        tools={[
          {
            callId: 'web-fetch-1',
            name: 'web_fetch',
            input: JSON.stringify({ url: 'https://example.com/news', prompt: longPrompt }),
          },
        ]}
      />
    );

    expect(screen.getByText('Fetch URL: https://example.com/news')).toBeInTheDocument();
    expect(screen.getByLabelText('Tool running')).toHaveTextContent('running');
    expect(screen.getByText('Awaiting tool result…')).toBeInTheDocument();
    expect(container.querySelector('.activity-card-live')).toBeInTheDocument();
    expect(container.querySelector('.running-tool-input-preview')).toBeInTheDocument();
    expect(screen.getByText(/more characters/)).toBeInTheDocument();
    expect(screen.queryByText(longPrompt)).not.toBeInTheDocument();
  });

  it('renders a successful bash result with duration status and tool details', () => {
    const { container } = render(
      <ChatToolActivity
        tools={[
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
        ]}
      />
    );

    expect(screen.getByText('Bash: pwd')).toBeInTheDocument();
    expect(screen.getByLabelText('Tool 119ms')).toHaveTextContent('119ms');
    expect(screen.getByText('Print working directory')).toBeInTheDocument();
    expect(screen.getByText('/workspace/kodelet')).toBeInTheDocument();
    expect(container.querySelector('.activity-card-live')).not.toBeInTheDocument();
  });

  it('renders failed tool results with error styling and failed status', () => {
    const { container } = render(
      <ChatToolActivity
        tools={[
          {
            callId: 'bash-1',
            name: 'bash',
            input: '{"command":"false","description":"Run failing command"}',
            result: {
              toolName: 'bash',
              success: false,
              error: 'Command exited with status 1.',
              metadata: {
                command: 'false',
                exitCode: 1,
                output: '',
                executionTime: 119000000,
                workingDir: '/workspace/kodelet',
              },
            },
          },
        ]}
      />
    );

    expect(screen.getByText('Bash: false')).toBeInTheDocument();
    expect(screen.getByLabelText('Tool failed')).toHaveTextContent('failed');
    expect(screen.getByText('Command exited with status 1.')).toBeInTheDocument();
    expect(container.querySelector('.activity-card-error')).toBeInTheDocument();
    expect(container.querySelector('.tool-summary-icon-error')).toBeInTheDocument();
  });

  it('returns null for an empty tool collection', () => {
    const { container } = render(<ChatToolActivity tools={[]} />);

    expect(container.firstChild).toBeNull();
  });

  it('summarizes patch and OpenAI search tool calls for compact headers', () => {
    const patchCall: ChatRenderToolCall = {
      callId: 'patch-1',
      name: 'apply_patch',
      input: '{"input":"*** Begin Patch\\n*** Update File: README.md\\n*** End Patch"}',
      result: {
        toolName: 'apply_patch',
        success: true,
        metadata: {
          changes: [
            {
              path: 'README.md',
              operation: 'update',
            },
            {
              path: 'docs/DEVELOPMENT.md',
              operation: 'update',
            },
          ],
        },
      },
    };

    const openPageCall: ChatRenderToolCall = {
      callId: 'search-1',
      name: 'openai_web_search',
      input: '{"type":"open_page","status":"completed"}',
      result: {
        toolName: 'openai_web_search',
        success: true,
        metadata: {
          status: 'completed',
          action: 'open_page',
        },
      },
    };

    expect(getToolSummary(patchCall)).toBe('Apply patch: update README.md (+1 more)');
    expect(getToolSummary(openPageCall)).toBe('Open page: URL unavailable');
  });

  it('exposes tool status and preview helpers for focused formatting tests', () => {
    expect(
      getToolActivityStatus({
        callId: 'bash-1',
        name: 'bash',
        input: '{"command":"pwd"}',
        result: {
          toolName: 'bash',
          success: true,
          metadata: {
            command: 'pwd',
            executionTime: 119000000,
          },
        },
      })
    ).toBe('119ms');

    expect(formatToolInputPreview('{"command":"pwd"}')).toBe('{\n  "command": "pwd"\n}');
  });
});
