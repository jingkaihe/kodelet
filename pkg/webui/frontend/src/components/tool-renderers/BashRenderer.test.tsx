import { describe, it, expect, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import BashRenderer from './BashRenderer';
import { ToolResult, BashMetadata } from '../../types';

// Mock shared components
interface MockCopyButtonProps {
  content: string;
}

interface MockStatusBadgeProps {
  text: string;
  variant?: string;
}

vi.mock('./shared', () => ({
  CopyButton: ({ content }: MockCopyButtonProps) => (
    <button data-testid="copy-button" data-content={content}>Copy</button>
  ),
  StatusBadge: ({ text, variant }: MockStatusBadgeProps) => (
    <span data-testid="status-badge" data-variant={variant}>{text}</span>
  ),
}));

// Mock utils
vi.mock('./utils', () => ({
  formatDuration: (duration: number) => `${duration}ms`,
}));

describe('BashRenderer', () => {
  const createToolResult = (metadata: Partial<BashMetadata>): ToolResult => ({
    toolName: 'bash',
    success: true,
    error: undefined,
    timestamp: '2023-01-01T00:00:00Z',
    metadata: metadata as BashMetadata,
  });

  it('returns null when metadata is missing', () => {
    const toolResult = createToolResult({});
    const { container } = render(<BashRenderer toolResult={{ ...toolResult, metadata: undefined }} />);

    expect(container.firstChild).toBeNull();
  });

  describe('Background Process', () => {
    it('renders background process with PID', () => {
      const toolResult = createToolResult({
        pid: 12345,
        command: 'npm run dev',
        logPath: '/var/log/app.log',
      });

      render(<BashRenderer toolResult={toolResult} />);

      expect(screen.getByText('npm run dev')).toBeInTheDocument();
      expect(screen.getByText('PID: 12345')).toBeInTheDocument();
    });

    it('renders log file path', () => {
      const toolResult = createToolResult({
        pid: 12345,
        command: 'npm run dev',
        logPath: '/var/log/app.log',
      });

      render(<BashRenderer toolResult={toolResult} />);

      expect(screen.getByText(/Log:.*\/var\/log\/app.log/)).toBeInTheDocument();
    });

    it('handles missing log path', () => {
      const toolResult = createToolResult({
        pid: 12345,
        command: 'npm run dev',
      });

      render(<BashRenderer toolResult={toolResult} />);

      expect(screen.getByText(/Log:.*N\/A/)).toBeInTheDocument();
    });
  });

  describe('Command Execution', () => {
    it('renders command with exit code', () => {
      const toolResult = createToolResult({
        command: 'ls -la',
        exitCode: 0,
        output: 'file1.txt\nfile2.txt',
      });

      render(<BashRenderer toolResult={toolResult} />);

      expect(screen.getByText('ls -la')).toBeInTheDocument();
      expect(screen.getByText('Exit 0')).toBeInTheDocument();
    });

    it('shows success variant for exit code 0', () => {
      const toolResult = createToolResult({
        command: 'ls',
        exitCode: 0,
        output: 'file.txt',
      });

      render(<BashRenderer toolResult={toolResult} />);

      const badge = screen.getByTestId('status-badge');
      expect(badge).toHaveAttribute('data-variant', 'success');
    });

    it('shows error variant for non-zero exit code', () => {
      const toolResult = createToolResult({
        command: 'invalid-command',
        exitCode: 127,
        output: 'command not found',
      });

      render(<BashRenderer toolResult={toolResult} />);

      const badge = screen.getByTestId('status-badge');
      expect(badge).toHaveAttribute('data-variant', 'error');
    });

    it('shows duration when provided', () => {
      const toolResult = createToolResult({
        command: 'echo "test"',
        executionTime: 250,
        output: 'test',
      });

      render(<BashRenderer toolResult={toolResult} />);

      expect(screen.getByText('250ms')).toBeInTheDocument();
    });

    it('shows copy button for output', () => {
      const toolResult = createToolResult({
        command: 'echo "test"',
        output: 'test output',
        exitCode: 0,
      });

      render(<BashRenderer toolResult={toolResult} />);

      const copyButton = screen.getByTestId('copy-button');
      expect(copyButton).toHaveAttribute('data-content', 'test output');
    });

    it('does not show copy button when no output', () => {
      const toolResult = createToolResult({
        command: 'echo "test"',
        output: '',
        exitCode: 0,
      });

      render(<BashRenderer toolResult={toolResult} />);

      expect(screen.queryByTestId('copy-button')).not.toBeInTheDocument();
    });

    it('renders terminal output', () => {
      const toolResult = createToolResult({
        command: 'ls',
        output: 'file1.txt\nfile2.txt\nfile3.txt',
        exitCode: 0,
      });

      const { container } = render(<BashRenderer toolResult={toolResult} />);

      const terminalOutput = container.querySelector('.bg-kodelet-dark');
      expect(terminalOutput).toBeInTheDocument();
    });

    it('shows "No output" when output is empty', () => {
      const toolResult = createToolResult({
        command: 'touch newfile.txt',
        output: '',
        exitCode: 0,
      });

      render(<BashRenderer toolResult={toolResult} />);

      expect(screen.getByText('No output')).toBeInTheDocument();
    });

    it('handles whitespace-only output as empty', () => {
      const toolResult = createToolResult({
        command: 'echo -n',
        output: '   \n\t  ',
        exitCode: 0,
      });

      render(<BashRenderer toolResult={toolResult} />);

      expect(screen.getByText('No output')).toBeInTheDocument();
    });

    it('escapes HTML in output', () => {
      const toolResult = createToolResult({
        command: 'echo',
        output: '<script>alert("xss")</script>',
        exitCode: 0,
      });

      render(<BashRenderer toolResult={toolResult} />);

      expect(screen.queryByText('alert("xss")')).not.toBeInTheDocument();
      const pre = document.querySelector('pre');
      expect(pre?.innerHTML).toContain('&lt;script&gt;');
    });

    it('handles ANSI color codes in output', () => {
      const ESC = '\u001b';
      const toolResult = createToolResult({
        command: 'ls --color',
        output: `${ESC}[31mError${ESC}[0m: File not found`,
        exitCode: 1,
      });

      const { container } = render(<BashRenderer toolResult={toolResult} />);

      const coloredSpan = container.querySelector('.text-red-500');
      expect(coloredSpan).toBeInTheDocument();
    });

    it('defaults exit code to 0 when not provided', () => {
      const toolResult = createToolResult({
        command: 'echo "test"',
        output: 'test',
      });

      render(<BashRenderer toolResult={toolResult} />);

      expect(screen.getByText('Exit 0')).toBeInTheDocument();
    });
  });
});
