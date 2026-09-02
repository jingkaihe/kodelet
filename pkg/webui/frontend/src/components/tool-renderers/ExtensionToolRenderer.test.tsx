import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { ToolResult } from '../../types';
import ExtensionToolRenderer from './ExtensionToolRenderer';

describe('ExtensionToolRenderer', () => {
  afterEach(() => {
    vi.useRealTimers();
  });

  it('pretty-prints JSON output from extension tools', () => {
    const toolResult: ToolResult = {
      toolName: 'git_info',
      success: true,
      timestamp: '2026-05-12T00:00:00Z',
      metadata: {
        type: 'extension_tool',
        extensionID: 'git',
        toolName: 'git_info',
        output: '{"branch":"main","changes":0}',
      },
    };

    const { container } = render(<ExtensionToolRenderer toolResult={toolResult} />);

    expect(screen.getByText('git_info')).toBeInTheDocument();
    expect(container.querySelector('.tool-code-block code')?.textContent).toBe(
      '{\n  "branch": "main",\n  "changes": 0\n}'
    );
  });

  it('renders generic markdown presentation without exposing unrelated metadata', () => {
    const toolResult: ToolResult = {
      toolName: 'send_instruction',
      success: true,
      metadata: {
        type: 'extension_tool',
        extensionID: 'worker-tools',
        toolName: 'send_instruction',
        executionTime: 125000000,
        output: 'Started run run_456.',
        data: {
          agent_id: 'agt_123',
          run_id: 'run_456',
          conversation_id: 'conv_789',
          presentation: {
            summary: 'Follow up parser-reviewer',
            body: 'Review **parser recovery** and the retry path.',
            format: 'markdown',
          },
        },
      },
    };

    const { container } = render(<ExtensionToolRenderer toolResult={toolResult} />);

    expect(screen.queryByText('Follow up parser-reviewer')).not.toBeInTheDocument();
    expect(screen.queryByText('agt_123')).not.toBeInTheDocument();
    expect(screen.queryByText('run_456')).not.toBeInTheDocument();
    expect(screen.queryByText('conv_789')).not.toBeInTheDocument();
    expect(screen.queryByText('Task')).not.toBeInTheDocument();
    expect(screen.getByText('parser recovery')).toBeInTheDocument();
    expect(container.querySelector('.extension-presentation-body strong')).toHaveTextContent(
      'parser recovery'
    );
    expect(container.querySelector('.tool-terminal')).not.toBeInTheDocument();
    expect(screen.queryByText('send_instruction')).not.toBeInTheDocument();
    expect(screen.queryByText('Started run run_456.')).not.toBeInTheDocument();
  });

  it('sanitizes generic markdown presentation bodies', () => {
    const toolResult: ToolResult = {
      toolName: 'redirect_worker',
      success: true,
      metadata: {
        type: 'extension_tool',
        extensionID: 'worker-tools',
        toolName: 'redirect_worker',
        output: 'Queued update.',
        data: {
          agent_id: 'agt_123',
          run_id: 'run_456',
          conversation_id: 'conv_789',
          presentation: {
            summary: 'Steer parser-reviewer',
            body: '<img src=x onerror="alert(1)">Focus on [unsafe](javascript:alert(1)) handling.',
            format: 'markdown',
          },
        },
      },
    };

    const { container } = render(<ExtensionToolRenderer toolResult={toolResult} />);

    expect(screen.queryByText('Steer parser-reviewer')).not.toBeInTheDocument();
    expect(screen.queryByText('agt_123')).not.toBeInTheDocument();
    expect(screen.queryByText('run_456')).not.toBeInTheDocument();
    expect(screen.queryByText('conv_789')).not.toBeInTheDocument();
    expect(screen.queryByText('Message')).not.toBeInTheDocument();
    const renderedMessage = container.querySelector('.extension-presentation-body');
    expect(renderedMessage).toHaveTextContent('Focus on unsafe handling.');
    expect(renderedMessage?.querySelector('a')).toBeNull();
    expect(container.querySelector('img')).not.toBeInTheDocument();
    expect(container.querySelector('.tool-terminal')).not.toBeInTheDocument();
  });

  it('uses extension output when presentation omits a body', () => {
    const toolResult: ToolResult = {
      toolName: 'observe_worker',
      success: true,
      metadata: {
        type: 'extension_tool',
        extensionID: 'worker-tools',
        toolName: 'observe_worker',
        output: '**Inspection complete.**',
        data: {
          presentation: {
            summary: 'Wait for parser-reviewer',
            format: 'markdown',
          },
        },
      },
    };

    const { container } = render(<ExtensionToolRenderer toolResult={toolResult} />);

    expect(screen.queryByText('Wait for parser-reviewer')).not.toBeInTheDocument();
    expect(container.querySelector('.tool-terminal')).toHaveTextContent('**Inspection complete.**');
    expect(container.querySelector('.extension-presentation-body strong')).toBeNull();
  });

  it('does not fall back to extension output when presentation supplies an empty body', () => {
    const toolResult: ToolResult = {
      toolName: 'observe_worker',
      success: true,
      metadata: {
        type: 'extension_tool',
        extensionID: 'worker-tools',
        toolName: 'observe_worker',
        output: 'Internal worker details.',
        data: {
          presentation: {
            summary: 'Wait for parser-reviewer',
            body: '',
          },
        },
      },
    };

    render(<ExtensionToolRenderer toolResult={toolResult} />);

    expect(
      screen.getByText('Extension tool completed without presentation details.')
    ).toBeInTheDocument();
    expect(screen.queryByText('Internal worker details.')).not.toBeInTheDocument();
  });

  it('keeps host errors visible alongside presentation content', () => {
    const toolResult: ToolResult = {
      toolName: 'redirect_worker',
      success: false,
      error: 'The worker is no longer running.',
      metadata: {
        type: 'extension_tool',
        extensionID: 'worker-tools',
        toolName: 'redirect_worker',
        output: 'Internal worker details.',
        data: {
          presentation: {
            summary: 'Steer parser-reviewer',
            body: 'Focus on parser recovery.',
          },
        },
      },
    };

    render(<ExtensionToolRenderer toolResult={toolResult} />);

    expect(screen.queryByText('Steer parser-reviewer')).not.toBeInTheDocument();
    expect(screen.getByText('The worker is no longer running.')).toBeInTheDocument();
    expect(screen.getByText('Focus on parser recovery.')).toBeInTheDocument();
    expect(screen.queryByText('Internal worker details.')).not.toBeInTheDocument();
  });

  it('does not repeat a host error through the presentation fallback body', () => {
    const toolResult: ToolResult = {
      toolName: 'cancel_worker',
      success: false,
      error: 'The worker could not be canceled.',
      metadata: {
        type: 'extension_tool',
        extensionID: 'worker-tools',
        toolName: 'cancel_worker',
        output: 'The worker could not be canceled.',
        data: {
          presentation: {
            summary: 'Cancel parser-reviewer',
          },
        },
      },
    };

    render(<ExtensionToolRenderer toolResult={toolResult} />);

    expect(screen.getAllByText('The worker could not be canceled.')).toHaveLength(1);
    expect(
      screen.queryByText('Extension tool completed without presentation details.')
    ).not.toBeInTheDocument();
  });

  it('falls back to normal rendering for unsafe presentation metadata', () => {
    const toolResult: ToolResult = {
      toolName: 'send_instruction',
      success: true,
      metadata: {
        type: 'extension_tool',
        extensionID: 'worker-tools',
        toolName: 'send_instruction',
        output: 'Normal extension output.',
        data: {
          presentation: {
            summary: 'Follow up \u202eparser-reviewer',
            body: 'This body must not replace normal output.',
          },
        },
      },
    };

    render(<ExtensionToolRenderer toolResult={toolResult} />);

    expect(screen.getByText('send_instruction')).toBeInTheDocument();
    expect(screen.getByText('Normal extension output.')).toBeInTheDocument();
    expect(screen.queryByText('This body must not replace normal output.')).not.toBeInTheDocument();
  });

  it('renders accumulated task activity while the tool is running', () => {
    vi.useFakeTimers();
    const toolResult: ToolResult = {
      toolName: 'code_search',
      success: true,
      metadata: {
        extensionId: 'code-search',
        toolName: 'code_search',
        output: 'Searching code - 2 actions running',
        data: {
          taskRun: {
            version: 1,
            revision: 7,
            kind: 'code_search',
            status: 'running',
            phase: 'working',
            title: 'Searching code',
            detail: '2 actions running',
            elapsedMs: 68000,
            counts: { succeeded: 10, failed: 0, running: 2 },
            activities: [
              {
                id: 'search-1',
                sequence: 1,
                kind: 'grep_tool',
                label: 'Search "HandleToolUpdate" in pkg/',
                status: 'succeeded',
              },
              {
                id: 'read-1',
                sequence: 2,
                kind: 'file_read',
                label: 'Read pkg/llm/base/tool_execution.go',
                status: 'running',
              },
            ],
            omittedSucceeded: 9,
          },
        },
      },
    };

    const { container, rerender } = render(
      <ExtensionToolRenderer isPartial toolResult={toolResult} />
    );

    expect(screen.queryByText('Searching code')).not.toBeInTheDocument();
    expect(container.querySelector('.task-run-headline')).not.toBeInTheDocument();
    expect(screen.getByText('10 done · 2 running · 1m 08s')).toBeInTheDocument();
    expect(screen.getByText('Search "HandleToolUpdate" in pkg/')).toBeInTheDocument();
    expect(screen.getByText('+9 earlier completed')).toBeInTheDocument();
    expect(container.querySelector('.task-run-activity.is-running')).toBeInTheDocument();

    act(() => vi.advanceTimersByTime(1000));
    expect(screen.getByText('10 done · 2 running · 1m 09s')).toBeInTheDocument();

    act(() => vi.advanceTimersByTime(1000));
    expect(screen.getByText('10 done · 2 running · 1m 10s')).toBeInTheDocument();

    rerender(<ExtensionToolRenderer toolResult={toolResult} />);
    expect(vi.getTimerCount()).toBe(0);
  });

  it('hides Markdown fence-only failed activity previews', () => {
    const toolResult: ToolResult = {
      toolName: 'subagent',
      success: false,
      error: 'tests failed',
      metadata: {
        extensionId: 'subagent',
        toolName: 'subagent',
        output: 'tests failed',
        data: {
          taskRun: {
            version: 1,
            revision: 2,
            kind: 'subagent',
            status: 'failed',
            phase: 'failed',
            title: 'Delegated task failed',
            elapsedMs: 1000,
            counts: { succeeded: 0, failed: 2, running: 0 },
            activities: [
              {
                id: 'test-1',
                sequence: 1,
                kind: 'bash',
                label: 'Bash: Run TypeScript SDK unit tests',
                status: 'failed',
                preview: '```',
              },
              {
                id: 'test-2',
                sequence: 2,
                kind: 'bash',
                label: 'Bash: Run Go tests',
                status: 'failed',
                preview: '```text\nGo tests failed\n```',
              },
            ],
          },
        },
      },
    };

    render(<ExtensionToolRenderer toolResult={toolResult} />);

    expect(screen.getByText('Bash: Run TypeScript SDK unit tests')).toBeInTheDocument();
    expect(screen.getByText('Go tests failed')).toBeInTheDocument();
    expect(screen.queryByText('```')).not.toBeInTheDocument();
  });

  it('renders the completed task response as markdown', async () => {
    const toolResult: ToolResult = {
      toolName: 'subagent',
      success: true,
      metadata: {
        extensionId: 'subagent',
        toolName: 'subagent',
        output: '## Findings\n\nTests now **pass**.',
        data: {
          taskRun: {
            version: 1,
            revision: 9,
            kind: 'subagent',
            status: 'completed',
            phase: 'completed',
            title: 'Delegated task',
            detail: '',
            elapsedMs: 74000,
            counts: { succeeded: 12, failed: 0, running: 0 },
            activities: [],
            omittedSucceeded: 12,
          },
        },
      },
    };

    const { container } = render(<ExtensionToolRenderer toolResult={toolResult} />);

    expect(screen.queryByText('Delegated task')).not.toBeInTheDocument();
    expect(screen.getByRole('heading', { name: 'Findings' })).toBeInTheDocument();
    expect(screen.getByText('pass')).toBeInTheDocument();
    const activityToggle = screen.getByText('Show activity');
    expect(activityToggle).toBeInTheDocument();

    fireEvent.click(activityToggle);
    await waitFor(() => expect(screen.getByText('Hide activity')).toBeInTheDocument());

    fireEvent.click(screen.getByText('Hide activity'));
    await waitFor(() => expect(screen.getByText('Show activity')).toBeInTheDocument());
    expect(container.querySelector('.task-run-response strong')).toHaveTextContent('pass');
  });

  it('sanitizes raw HTML and unsafe links in task markdown', () => {
    const toolResult: ToolResult = {
      toolName: 'subagent',
      success: true,
      metadata: {
        extensionId: 'subagent',
        toolName: 'subagent',
        output: [
          '<img src=x onerror="alert(1)">',
          '',
          '[unsafe](javascript:alert(1))',
          '',
          '[encoded](jav&#x61;script&colon;alert(1))',
        ].join('\n'),
        data: {
          taskRun: {
            version: 1,
            revision: 2,
            kind: 'subagent',
            status: 'completed',
            phase: 'completed',
            title: 'Delegated task',
            elapsedMs: 1000,
            counts: { succeeded: 1, failed: 0, running: 0 },
            activities: [],
          },
        },
      },
    };

    const { container } = render(<ExtensionToolRenderer toolResult={toolResult} />);

    expect(container.querySelector('img')).not.toBeInTheDocument();
    expect(screen.getByText('unsafe').closest('a')).toBeNull();
    expect(screen.getByText('encoded').closest('a')).toBeNull();
  });

  it('shows errors for generic failed extension tools', () => {
    const toolResult: ToolResult = {
      toolName: 'weather',
      success: false,
      error: 'extension timed out',
      metadata: {
        extensionId: 'weather',
        toolName: 'weather',
        output: '',
      },
    };

    render(<ExtensionToolRenderer toolResult={toolResult} />);

    expect(screen.getByText('extension timed out')).toBeInTheDocument();
    expect(screen.queryByText('Extension tool completed without output.')).not.toBeInTheDocument();
  });

  it('falls back to generic extension output for malformed task snapshots', () => {
    const toolResult: ToolResult = {
      toolName: 'custom_task',
      success: true,
      metadata: {
        extensionId: 'custom',
        toolName: 'custom_task',
        output: 'plain fallback',
        data: { taskRun: { version: 1 } },
      },
    };

    const { container } = render(<ExtensionToolRenderer toolResult={toolResult} />);

    expect(screen.getByText('plain fallback')).toBeInTheDocument();
    expect(container.querySelector('.task-run-progress')).not.toBeInTheDocument();
  });

  it('falls back instead of rendering oversized task activity', () => {
    const activities = Array.from({ length: 15 }, (_, index) => ({
      id: `activity-${index}`,
      sequence: index + 1,
      kind: 'file_read',
      label: `Read file-${index}.go`,
      status: 'succeeded',
    }));
    const toolResult: ToolResult = {
      toolName: 'custom_task',
      success: true,
      metadata: {
        extensionId: 'custom',
        toolName: 'custom_task',
        output: 'bounded fallback',
        data: {
          taskRun: {
            version: 1,
            revision: 1,
            kind: 'custom',
            status: 'completed',
            phase: 'completed',
            title: 'Custom task',
            elapsedMs: 1,
            counts: { succeeded: 15, failed: 0, running: 0 },
            activities,
          },
        },
      },
    };

    const { container } = render(<ExtensionToolRenderer toolResult={toolResult} />);

    expect(screen.getByText('bounded fallback')).toBeInTheDocument();
    expect(container.querySelector('.task-run-result')).not.toBeInTheDocument();
  });
});
