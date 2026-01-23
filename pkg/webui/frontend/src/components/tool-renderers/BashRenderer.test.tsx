import React from 'react';
import { describe, it, expect, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import BashRenderer from './BashRenderer';
import { ToolResult, BashMetadata } from '../../types';

// Mock shared components
interface MockToolCardProps {
  title: string;
  badge?: { text: string; className: string };
  actions?: React.ReactNode;
  children: React.ReactNode;
}

interface MockCopyButtonProps {
  content: string;
}

interface MockMetadataRowProps {
  label: string;
  value: string | number;
}

interface MockCollapsibleProps {
  title: string;
  children: React.ReactNode;
}

vi.mock('./shared', () => ({
  ToolCard: ({ title, badge, actions, children }: MockToolCardProps) => (
    <div data-testid="tool-card">
      <h3>{title}</h3>
      {badge && <span className={badge.className}>{badge.text}</span>}
      {actions && <div data-testid="actions">{actions}</div>}
      {children}
    </div>
  ),
  CopyButton: ({ content }: MockCopyButtonProps) => (
    <button data-testid="copy-button" data-content={content}>Copy</button>
  ),
  MetadataRow: ({ label, value }: MockMetadataRowProps) => (
    <div data-testid="metadata-row">
      {label}: {value}
    </div>
  ),
  Collapsible: ({ title, children }: MockCollapsibleProps) => (
    <div data-testid="collapsible">
      <h4>{title}</h4>
      {children}
    </div>
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
        startTime: '2023-01-01T00:00:00Z',
      });

      render(<BashRenderer toolResult={toolResult} />);

      expect(screen.getByText('Background Process')).toBeInTheDocument();
      expect(screen.getByText('PID: 12345')).toBeInTheDocument();
      const badge = screen.getByText('PID: 12345');
      expect(badge.className).toContain('font-heading');
    });

    it('renders background process metadata', () => {
      const toolResult = createToolResult({
        pid: 12345,
        command: 'npm run dev',
        logPath: '/var/log/app.log',
        startTime: '2023-01-01T00:00:00Z',
      });

      render(<BashRenderer toolResult={toolResult} />);

      expect(screen.getByText(/Command:.*npm run dev/)).toBeInTheDocument();
      expect(screen.getByText(/Log File:.*\/var\/log\/app.log/)).toBeInTheDocument();
      expect(screen.getByText(/Started:/)).toBeInTheDocument();
    });

    it('handles missing log path', () => {
      const toolResult = createToolResult({
        pid: 12345,
        command: 'npm run dev',
      });

      render(<BashRenderer toolResult={toolResult} />);

      expect(screen.getByText(/Log File:.*N\/A/)).toBeInTheDocument();
    });

    it('uses logFile as fallback for log path', () => {
      const toolResult = createToolResult({
        pid: 12345,
        command: 'npm run dev',
        logFile: '/tmp/output.log',
      });

      render(<BashRenderer toolResult={toolResult} />);

      expect(screen.getByText(/Log File:.*\/tmp\/output.log/)).toBeInTheDocument();
    });
  });

  describe('Command Execution', () => {
    it('renders successful command execution', () => {
      const toolResult = createToolResult({
        command: 'ls -la',
        exitCode: 0,
        output: 'file1.txt\nfile2.txt',
        workingDir: '/home/user',
        executionTime: 150,
      });

      render(<BashRenderer toolResult={toolResult} />);

      expect(screen.getByText('Command Execution')).toBeInTheDocument();
      expect(screen.getByText('Exit 0')).toBeInTheDocument();
      const badge = screen.getByText('Exit 0');
      expect(badge.className).toContain('font-heading');
    });

    it('renders failed command execution', () => {
      const toolResult = createToolResult({
        command: 'invalid-command',
        exitCode: 127,
        output: 'command not found',
      });

      render(<BashRenderer toolResult={toolResult} />);

      expect(screen.getByText('Exit 127')).toBeInTheDocument();
      const badge = screen.getByText('Exit 127');
      expect(badge.className).toContain('font-heading');
    });

    it('renders command metadata', () => {
      const toolResult = createToolResult({
        command: 'echo "test"',
        workingDir: '/home/user/project',
        executionTime: 250,
        output: 'test',
      });

      render(<BashRenderer toolResult={toolResult} />);

      expect(screen.getByText(/Command:.*echo "test"/)).toBeInTheDocument();
      expect(screen.getByText(/Directory:.*\/home\/user\/project/)).toBeInTheDocument();
      expect(screen.getByText(/Duration:.*250ms/)).toBeInTheDocument();
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

    it('renders command output in collapsible', () => {
      const toolResult = createToolResult({
        command: 'ls',
        output: 'file1.txt\nfile2.txt\nfile3.txt',
        exitCode: 0,
      });

      render(<BashRenderer toolResult={toolResult} />);

      const collapsible = screen.getByTestId('collapsible');
      expect(collapsible).toBeInTheDocument();
      expect(screen.getByText('Output')).toBeInTheDocument();
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

    it('renders terminal output with proper formatting', () => {
      const toolResult = createToolResult({
        command: 'ls',
        output: 'test output with\nmultiple lines',
        exitCode: 0,
      });

      const { container } = render(<BashRenderer toolResult={toolResult} />);

      const terminalOutput = container.querySelector('.bg-kodelet-dark');
      expect(terminalOutput).toBeInTheDocument();
      expect(terminalOutput).toHaveClass('bg-kodelet-dark', 'text-kodelet-green');
    });

    it('escapes HTML in output', () => {
      const toolResult = createToolResult({
        command: 'echo',
        output: '<script>alert("xss")</script>',
        exitCode: 0,
      });

      render(<BashRenderer toolResult={toolResult} />);

      // The HTML should be escaped
      expect(screen.queryByText('alert("xss")')).not.toBeInTheDocument();
      const pre = screen.getByTestId('collapsible').querySelector('pre');
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
      const badge = screen.getByText('Exit 0');
      expect(badge.className).toContain('font-heading');
    });
  });
});