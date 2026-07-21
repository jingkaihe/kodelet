import { describe, expect, it } from 'vitest';
import { render, screen } from '@testing-library/react';
import ExtensionToolRenderer from './ExtensionToolRenderer';
import { ToolResult } from '../../types';

describe('ExtensionToolRenderer', () => {
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

  it('renders accumulated task activity while the tool is running', () => {
    const toolResult: ToolResult = {
      toolName: 'code_search',
      success: true,
      metadata: {
        extensionId: 'code-search',
        toolName: 'code_search',
        output: 'Searching code — 2 actions running',
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

    const { container } = render(<ExtensionToolRenderer isPartial toolResult={toolResult} />);

    expect(screen.getByText('Searching code')).toBeInTheDocument();
    expect(screen.getByText('2 actions running')).toBeInTheDocument();
    expect(screen.getByText('10 done · 2 running · 1m 08s')).toBeInTheDocument();
    expect(screen.getByText('Search "HandleToolUpdate" in pkg/')).toBeInTheDocument();
    expect(screen.getByText('+9 earlier completed')).toBeInTheDocument();
    expect(container.querySelector('.task-run-activity.is-running')).toBeInTheDocument();
  });

  it('renders the completed task response as markdown', () => {
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

    expect(screen.getByRole('heading', { name: 'Findings' })).toBeInTheDocument();
    expect(screen.getByText('pass')).toBeInTheDocument();
    expect(screen.getByText('Show activity')).toBeInTheDocument();
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
