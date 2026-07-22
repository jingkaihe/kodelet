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
            input: JSON.stringify({
              url: 'https://example.com/news',
              prompt: longPrompt,
            }),
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

  it('renders accumulated bash output while the tool is still running', () => {
    const { container } = render(
      <ChatToolActivity
        tools={[
          {
            callId: 'bash-1',
            name: 'bash',
            input: '{"command":"long-task","description":"Run long task"}',
            inProgress: true,
            result: {
              toolName: 'bash',
              success: true,
              metadata: {
                command: 'long-task',
                output: 'partial output',
                exitCode: 0,
              },
            },
          },
        ]}
      />
    );

    expect(screen.getByLabelText('Tool running')).toHaveTextContent('running');
    expect(screen.getByText('partial output')).toBeInTheDocument();
    expect(screen.queryByText('Awaiting tool result…')).not.toBeInTheDocument();
    expect(container.querySelector('.activity-card-live')).toBeInTheDocument();
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

  it('summarizes and opens a live code-search agent run', () => {
    const { container } = render(
      <ChatToolActivity
        tools={[
          {
            callId: 'search-1',
            name: 'code_search',
            input: '{"query":"Trace tool updates"}',
            inProgress: true,
            result: {
              toolName: 'code_search',
              success: true,
              metadataType: 'extension_tool',
              metadata: {
                extensionId: 'code-search',
                toolName: 'code_search',
                output: 'Searching code',
                data: {
                  taskRun: {
                    version: 1,
                    revision: 1,
                    kind: 'code_search',
                    status: 'running',
                    phase: 'starting',
                    title: 'Searching code',
                    detail: 'starting task',
                    elapsedMs: 1000,
                    counts: { succeeded: 0, failed: 0, running: 0 },
                    activities: [],
                  },
                },
              },
            },
          },
        ]}
      />
    );

    expect(screen.getByText('Code search: Trace tool updates')).toBeInTheDocument();
    expect(screen.queryByText('Searching code')).not.toBeInTheDocument();
    expect(container.querySelector('.activity-card[open]')).toBeInTheDocument();
  });

  it('shows a live subagent instruction only in the outer activity header', () => {
    const { container } = render(
      <ChatToolActivity
        tools={[
          {
            callId: 'subagent-1',
            name: 'subagent',
            input: '{"task":"Review the task progress renderer"}',
            inProgress: true,
            result: {
              toolName: 'subagent',
              success: true,
              metadataType: 'extension_tool',
              metadata: {
                extensionId: 'subagent',
                toolName: 'subagent',
                output: 'Delegated task - Review the task progress renderer',
                data: {
                  taskRun: {
                    version: 1,
                    revision: 1,
                    kind: 'subagent',
                    status: 'running',
                    phase: 'starting',
                    title: 'Delegated task',
                    detail: 'Review the task progress renderer',
                    elapsedMs: 1000,
                    counts: { succeeded: 0, failed: 0, running: 0 },
                    activities: [],
                  },
                },
              },
            },
          },
        ]}
      />
    );

    expect(
      screen.getByText('Delegated task: Review the task progress renderer')
    ).toBeInTheDocument();
    expect(container.querySelector('.activity-detail-content')).not.toHaveTextContent(
      'Review the task progress renderer'
    );
    expect(container.querySelector('.task-run-headline')).not.toBeInTheDocument();
  });
});
