import { fireEvent, render, screen } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import BashRenderer from './BashRenderer';
import { BashMetadata, ToolResult } from '../../types';
import * as utils from '../../utils';

vi.mock('../../utils', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../utils')>();
  return {
    ...actual,
    copyToClipboard: vi.fn(),
  };
});

describe('BashRenderer', () => {
  beforeEach(() => {
    vi.mocked(utils.copyToClipboard).mockReset();
  });

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

  it('renders a compact result header and output', () => {
    const toolResult = createToolResult({
      command: 'ls -la',
      exitCode: 0,
      executionTime: 250000000,
      workingDir: '/tmp/work',
      output: 'file1.txt\nfile2.txt',
    });

    const { container } = render(<BashRenderer toolResult={toolResult} />);

    expect(screen.getByText('exit 0')).toBeInTheDocument();
    expect(screen.getByText('command output')).toBeInTheDocument();
    expect(screen.queryByText('ls -la')).not.toBeInTheDocument();
    expect(screen.queryByText('250ms')).not.toBeInTheDocument();
    expect(screen.queryByText('/tmp/work')).not.toBeInTheDocument();
    expect(screen.queryByText('shell command')).not.toBeInTheDocument();
    expect(screen.queryByText(/B$/)).not.toBeInTheDocument();
    expect(container.querySelector('.bash-tool-badge.is-success')).toBeInTheDocument();
    expect(container.querySelector('.tool-terminal')).toBeInTheDocument();
  });

  it('copies the command from the compact action', () => {
    const toolResult = createToolResult({
      command: 'ls -la',
      exitCode: 0,
      output: 'file1.txt',
    });

    render(<BashRenderer toolResult={toolResult} />);

    fireEvent.click(screen.getByRole('button', { name: 'Copy to clipboard' }));

    expect(utils.copyToClipboard).toHaveBeenCalledWith('ls -la');
  });

  it('renders the tool-call description when provided', () => {
    const toolResult = createToolResult({
      command: 'pwd',
      exitCode: 0,
      output: '/tmp/work',
    });

    render(
      <BashRenderer
        toolInput='{"command":"pwd","description":"Print the working directory"}'
        toolResult={toolResult}
      />
    );

    expect(screen.getByText('Print the working directory')).toBeInTheDocument();
  });

  it('renders an error badge for non-zero exits', () => {
    const toolResult = createToolResult({
      command: 'invalid-command',
      exitCode: 127,
      output: 'command not found',
    });

    const { container } = render(<BashRenderer toolResult={toolResult} />);

    expect(screen.getByText('exit 127')).toBeInTheDocument();
    expect(container.querySelector('.bash-tool-badge.is-error')).toBeInTheDocument();
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

  it('renders a running state for partial snapshots', () => {
    const toolResult = createToolResult({
      command: 'long-task',
      exitCode: 0,
      output: '',
    });

    const { container } = render(<BashRenderer isPartial toolResult={toolResult} />);

    expect(screen.getByText('running')).toBeInTheDocument();
    expect(screen.getByText('Waiting for command output…')).toBeInTheDocument();
    expect(screen.queryByText('exit 0')).not.toBeInTheDocument();
    expect(container.querySelector('.bash-tool-badge.is-success')).not.toBeInTheDocument();
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
