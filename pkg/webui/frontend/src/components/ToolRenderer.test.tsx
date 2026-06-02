import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import ToolRenderer from './ToolRenderer';
import { ToolResult } from '../types';

describe('ToolRenderer', () => {
  it('uses the bash renderer for failed bash commands so output is still visible', () => {
    const toolResult: ToolResult = {
      toolName: 'bash',
      success: false,
      error: 'Command exited with status 1',
      timestamp: '2023-01-01T00:00:00Z',
      metadata: {
        command: 'cat missing-file',
        exitCode: 1,
        output: 'cat: missing-file: No such file or directory',
      },
    };

    const { container } = render(<ToolRenderer toolResult={toolResult} />);

    expect(screen.queryByText('cat missing-file')).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Copy to clipboard' })).toBeInTheDocument();
    expect(screen.getByText('exit 1')).toBeInTheDocument();
    expect(screen.getByText('cat: missing-file: No such file or directory')).toBeInTheDocument();
    expect(container.querySelector('.tool-terminal')).toBeInTheDocument();
    expect(screen.queryByText('Error (bash):')).not.toBeInTheDocument();
  });

  it('keeps the generic error renderer for other failed tools', () => {
    const toolResult: ToolResult = {
      toolName: 'file_read',
      success: false,
      error: 'permission denied',
      timestamp: '2023-01-01T00:00:00Z',
      metadata: {
        filePath: '/tmp/secret.txt',
      },
    };

    render(<ToolRenderer toolResult={toolResult} />);

    expect(screen.getByText('Error (file_read):')).toBeInTheDocument();
    expect(screen.getByText('permission denied')).toBeInTheDocument();
  });

  it('uses the native search renderer for failed OpenAI web search results', () => {
    const toolResult: ToolResult = {
      toolName: 'openai_web_search',
      success: false,
      error: 'OpenAI web search failed',
      timestamp: '2026-04-03T00:00:00Z',
      metadata: {
        status: 'failed',
        action: 'search',
        queries: ['kodelet web ui search'],
        sources: ['https://example.com/source'],
      },
    };

    render(<ToolRenderer toolResult={toolResult} />);

    expect(screen.getByText('failed')).toBeInTheDocument();
    expect(screen.getAllByText('kodelet web ui search')).toHaveLength(2);
    expect(screen.getByText('https://example.com/source')).toBeInTheDocument();
    expect(screen.queryByText('Error (openai_web_search):')).not.toBeInTheDocument();
  });

  it('routes extension tools by metadata type instead of registered tool name', () => {
    const toolResult: ToolResult = {
      toolName: 'workspace_summary',
      metadataType: 'extension_tool',
      success: true,
      timestamp: '2026-05-12T00:00:00Z',
      metadata: {
        extensionId: 'workspace',
        toolName: 'workspace_summary',
        output: '{"files":3}',
      },
    };

    const { container } = render(<ToolRenderer toolResult={toolResult} />);

    expect(screen.getByText('workspace_summary')).toBeInTheDocument();
    expect(screen.queryByText('Show raw data')).not.toBeInTheDocument();
    expect(container.querySelector('.tool-code-block code')?.textContent).toBe(
      '{\n  "files": 3\n}'
    );
  });
});
