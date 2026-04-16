import { describe, expect, it } from 'vitest';
import { render, screen } from '@testing-library/react';
import BashRenderer from './BashRenderer';
import { BashMetadata, ToolResult } from '../../types';

describe('BashRenderer', () => {
  const createToolResult = (metadata: Partial<BashMetadata>): ToolResult => ({
    toolName: 'bash',
    success: true,
    timestamp: '2023-01-01T00:00:00Z',
    metadata: metadata as BashMetadata,
  });

  it('returns null when metadata is missing', () => {
    const toolResult = createToolResult({});
    const { container } = render(
      <BashRenderer toolResult={{ ...toolResult, metadata: undefined }} />
    );

    expect(container.firstChild).toBeNull();
  });

  it('renders command metadata and success badge', () => {
    const toolResult = createToolResult({
      command: 'ls -la',
      exitCode: 0,
      executionTime: 250000000,
      workingDir: '/tmp/work',
      output: 'file1.txt\nfile2.txt',
    });

    const { container } = render(<BashRenderer toolResult={toolResult} />);

    expect(screen.getByText('ls -la')).toBeInTheDocument();
    expect(screen.getByText('exit 0')).toBeInTheDocument();
    expect(screen.getByText('250ms')).toBeInTheDocument();
    expect(screen.getByText('/tmp/work')).toBeInTheDocument();
    expect(container.querySelector('.tool-badge-success')).toBeInTheDocument();
    expect(container.querySelector('.tool-terminal')).toBeInTheDocument();
  });

  it('renders an error badge for non-zero exits', () => {
    const toolResult = createToolResult({
      command: 'invalid-command',
      exitCode: 127,
      output: 'command not found',
    });

    const { container } = render(<BashRenderer toolResult={toolResult} />);

    expect(screen.getByText('exit 127')).toBeInTheDocument();
    expect(container.querySelector('.tool-badge-error')).toBeInTheDocument();
  });

  it('renders failure details and output for unsuccessful commands', () => {
    const toolResult: ToolResult = {
      toolName: 'bash',
      success: false,
      error: 'Command exited with status 127',
      timestamp: '2023-01-01T00:00:00Z',
      metadata: {
        command: 'invalid-command',
        exitCode: 127,
        output: 'command not found',
      } as BashMetadata,
    };

    render(<BashRenderer toolResult={toolResult} />);

    expect(screen.getByText('Command exited with status 127')).toBeInTheDocument();
    expect(screen.getByText('command not found')).toBeInTheDocument();
    expect(screen.getByText('exit 127')).toBeInTheDocument();
  });

  it('shows a failed badge instead of exit 0 when execution failed without an exit code', () => {
    const toolResult: ToolResult = {
      toolName: 'bash',
      success: false,
      error: 'Command timed out after 10 seconds',
      timestamp: '2023-01-01T00:00:00Z',
      metadata: {
        command: 'sleep 20',
        exitCode: 0,
        output: '',
      } as BashMetadata,
    };

    render(<BashRenderer toolResult={toolResult} />);

    expect(screen.getByText('failed')).toBeInTheDocument();
    expect(screen.queryByText('exit 0')).not.toBeInTheDocument();
    expect(screen.getByText('Command failed without output.')).toBeInTheDocument();
  });

  it('shows a note when the command produces no output', () => {
    const toolResult = createToolResult({
      command: 'touch newfile.txt',
      exitCode: 0,
      output: '   \n\t  ',
    });

    render(<BashRenderer toolResult={toolResult} />);

    expect(screen.getByText('Command completed without output.')).toBeInTheDocument();
  });

  it('escapes HTML in terminal output', () => {
    const toolResult = createToolResult({
      command: 'echo',
      exitCode: 0,
      output: '<script>alert("xss")</script>',
    });

    const { container } = render(<BashRenderer toolResult={toolResult} />);

    expect(screen.queryByText('alert("xss")')).not.toBeInTheDocument();
    expect(container.querySelector('.tool-terminal-body pre')?.innerHTML).toContain(
      '&lt;script&gt;'
    );
  });
});
