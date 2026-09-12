import { fireEvent, render, screen } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import BashRenderer from './BashRenderer';
import { ReferenceTerminal } from './reference';
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

describe('ReferenceTerminal ANSI output', () => {
  it('renders basic and bright colors with the terminal theme palette', () => {
    const { container } = render(
      <ReferenceTerminal output={'\x1b[32mPassed\x1b[31mFailed\x1b[91mBright failure\x1b[30;46m RUN \x1b[0m'} />
    );

    expect(screen.getByText('Passed')).toHaveStyle({ color: 'var(--ansi-green)' });
    expect(screen.getByText('Failed')).toHaveStyle({ color: 'var(--ansi-red)' });
    expect(screen.getByText('Bright failure')).toHaveStyle({ color: 'var(--ansi-bright-red)' });
    expect(screen.getByText('RUN')).toHaveStyle({
      color: 'var(--ansi-black)',
      backgroundColor: 'var(--ansi-cyan)',
    });
    expect(container.textContent).not.toContain('\x1b');
    expect(container.textContent).not.toContain('[32m');
  });

  it('renders indexed, grayscale, and truecolor foregrounds and backgrounds', () => {
    render(
      <ReferenceTerminal output={[
        '\x1b[38;5;196mIndexed red',
        '\x1b[0;48;5;244mGray background',
        '\x1b[0;38;2;12;34;56;48;2;210;220;230mTruecolor',
        '\x1b[0m',
      ].join('')} />
    );

    expect(screen.getByText('Indexed red')).toHaveStyle({ color: 'rgb(255, 0, 0)' });
    expect(screen.getByText('Gray background')).toHaveStyle({ backgroundColor: 'rgb(128, 128, 128)' });
    expect(screen.getByText('Truecolor')).toHaveStyle({
      color: 'rgb(12, 34, 56)',
      backgroundColor: 'rgb(210, 220, 230)',
    });
  });

  it('combines decorations and resets only the requested attributes', () => {
    render(
      <ReferenceTerminal output={[
        '\x1b[32;1;2;3;4;9mDecorated',
        '\x1b[22mNormal intensity',
        '\x1b[23;24mStrike only',
        '\x1b[29mColor only',
        '\x1b[0m',
      ].join('')} />
    );

    const decorated = screen.getByText('Decorated');
    expect(decorated).toHaveStyle({ color: 'var(--ansi-green)', fontWeight: 700, opacity: 0.7, fontStyle: 'italic' });
    expect(decorated.style.textDecorationLine || decorated.style.textDecoration).toContain('underline');
    expect(decorated.style.textDecorationLine || decorated.style.textDecoration).toContain('line-through');

    const normalIntensity = screen.getByText('Normal intensity');
    expect(normalIntensity.style.fontWeight).toBe('');
    expect(normalIntensity.style.opacity).toBe('');
    expect(normalIntensity).toHaveStyle({ color: 'var(--ansi-green)', fontStyle: 'italic' });
    expect(normalIntensity.style.textDecorationLine || normalIntensity.style.textDecoration).toContain('underline');
    expect(normalIntensity.style.textDecorationLine || normalIntensity.style.textDecoration).toContain('line-through');

    const strikeOnly = screen.getByText('Strike only');
    expect(strikeOnly.style.fontStyle).toBe('');
    expect(strikeOnly.style.textDecorationLine || strikeOnly.style.textDecoration).toBe('line-through');

    const colorOnly = screen.getByText('Color only');
    expect(colorOnly).toHaveStyle({ color: 'var(--ansi-green)' });
    expect(colorOnly.style.textDecorationLine || colorOnly.style.textDecoration).toBe('');
  });

  it('restores inherited defaults with foreground, background, and full resets', () => {
    render(
      <ReferenceTerminal output={[
        '\x1b[1;31;46mStyled',
        '\x1b[39mDefault foreground',
        '\x1b[49mDefault background',
        '\x1b[0mFully reset',
        '\x1b[1;38;2;12;34;56;48;2;210;220;230mRGB styled',
        '\x1b[mShort reset',
      ].join('')} />
    );

    const foregroundReset = screen.getByText('Default foreground');
    expect(foregroundReset.style.color).toBe('');
    expect(foregroundReset).toHaveStyle({ backgroundColor: 'var(--ansi-cyan)', fontWeight: 700 });

    const backgroundReset = screen.getByText('Default background');
    expect(backgroundReset.style.color).toBe('');
    expect(backgroundReset.style.backgroundColor).toBe('');
    expect(backgroundReset).toHaveStyle({ fontWeight: 700 });

    for (const text of ['Fully reset', 'Short reset']) {
      const reset = screen.getByText(text);
      expect(reset.style.color).toBe('');
      expect(reset.style.backgroundColor).toBe('');
      expect(reset.style.fontWeight).toBe('');
    }
  });

  it('preserves colors across newlines, whitespace, and blank terminal lines', () => {
    const { container } = render(
      <ReferenceTerminal output={'\x1b[32mFirst line\n\n  Second line\x1b[0m\nPlain line'} />
    );
    const lines = container.querySelectorAll('.tool-terminal-line');

    expect(lines).toHaveLength(4);
    expect(lines[0].textContent).toBe('First line');
    expect(lines[1].textContent).toBe('\u00a0');
    expect(lines[2].textContent).toBe('  Second line');
    expect(lines[3].textContent).toBe('Plain line');
    expect(screen.getByText('First line')).toHaveStyle({ color: 'var(--ansi-green)' });
    expect(screen.getByText('Second line')).toHaveStyle({ color: 'var(--ansi-green)' });
    expect(screen.getByText('Plain line').style.color).toBe('');
  });

  it('does not leak unterminated styles into another terminal component', () => {
    render(
      <>
        <ReferenceTerminal output={'\x1b[1;31mFirst terminal'} />
        <ReferenceTerminal output="Independent terminal" />
      </>
    );

    expect(screen.getByText('First terminal')).toHaveStyle({ color: 'var(--ansi-red)', fontWeight: 700 });
    const independent = screen.getByText('Independent terminal');
    expect(independent.style.color).toBe('');
    expect(independent.style.fontWeight).toBe('');
  });

  it('reparses each output snapshot without retaining the previous final style', () => {
    const { rerender } = render(<ReferenceTerminal output={'\x1b[1;31mOld failure'} />);

    rerender(<ReferenceTerminal output={'Plain prefix\x1b[32mNew success\x1b[0m'} />);

    expect(screen.queryByText('Old failure')).not.toBeInTheDocument();
    expect(screen.getByText('Plain prefix').style.color).toBe('');
    expect(screen.getByText('Plain prefix').style.fontWeight).toBe('');
    expect(screen.getByText('New success')).toHaveStyle({ color: 'var(--ansi-green)' });
    expect(screen.getByText('New success').style.fontWeight).toBe('');
  });

  it('keeps incomplete ANSI sequences invisible until an accumulated update completes them', () => {
    const prefix = '\x1b[32mWorking';
    const { container, rerender } = render(<ReferenceTerminal output={prefix} />);

    for (const suffix of ['\x1b', '\x1b[', '\x1b[3', '\x1b[38;2;10;']) {
      rerender(<ReferenceTerminal output={prefix + suffix} />);

      expect(container.querySelector('.tool-terminal-body')?.textContent).toBe('Working');
      expect(screen.getByText('Working')).toHaveStyle({ color: 'var(--ansi-green)' });
    }

    rerender(<ReferenceTerminal output={`${prefix}\x1b[31mFailed\x1b[0m`} />);

    expect(screen.getByText('Working')).toHaveStyle({ color: 'var(--ansi-green)' });
    expect(screen.getByText('Failed')).toHaveStyle({ color: 'var(--ansi-red)' });
    expect(container.querySelector('.tool-terminal-body')?.textContent).toBe('WorkingFailed');
  });

  it('ignores OSC hyperlinks, titles, clipboard controls, and incomplete OSC payloads', () => {
    const output = [
      '\x1b]0;private title\x07',
      '\x1b]52;c;c2VjcmV0\x1b\\',
      '\x1b[32m',
      '\x1b]8;;javascript:alert(1)\x07',
      'Safe link label',
      '\x1b]8;;\x1b\\',
      '\x1b[0m',
    ].join('');
    const { container, rerender } = render(<ReferenceTerminal output={output} />);

    expect(container.querySelector('.tool-terminal-body')?.textContent).toBe('Safe link label');
    expect(screen.getByText('Safe link label')).toHaveStyle({ color: 'var(--ansi-green)' });
    expect(container.querySelector('a, [href]')).not.toBeInTheDocument();

    rerender(<ReferenceTerminal output={`${output}\x1b]52;c;unfinished clipboard payload`} />);
    expect(container.querySelector('.tool-terminal-body')?.textContent).toBe('Safe link label');
  });

  it('ignores cursor and erase controls without stripping literal bracket text', () => {
    const { container } = render(
      <ReferenceTerminal output={'\x1b[?25l\x1b[2J\x1b[H\x1b[2K\x1b[32mProgress\x1b[1G\x1b[0m [32m is literal\x1b[?25h'} />
    );

    expect(container.querySelector('.tool-terminal-body')?.textContent).toBe('Progress [32m is literal');
    expect(screen.getByText('Progress')).toHaveStyle({ color: 'var(--ansi-green)' });
    expect(screen.getByText('[32m is literal').style.color).toBe('');
  });

  it('renders colored HTML and attribute payloads as safe literal text', () => {
    const html = '<script>alert("xss")</script><img src=x onerror="alert(1)">';
    const attributes = '" style="color:red" onmouseover="alert(2)"><svg onload="alert(3)"> & text';
    const { container } = render(
      <ReferenceTerminal output={`\x1b[31m${html}\x1b[0m${attributes}`} />
    );

    expect(screen.getByText(html)).toHaveStyle({ color: 'var(--ansi-red)' });
    expect(screen.getByText(attributes)).toBeInTheDocument();
    expect(container.querySelector('.tool-terminal-body')?.textContent).toBe(html + attributes);
    expect(container.querySelector('script, img, svg, [onerror], [onload], [onmouseover]')).not.toBeInTheDocument();
    expect(container.querySelector('.tool-terminal-body pre')?.innerHTML).toContain('&lt;script&gt;');
  });

  it('retains the 120-line limit and omitted-line indicator with ANSI output', () => {
    const output = Array.from({ length: 123 }, (_, index) => `\x1b[32mLine ${index + 1}\x1b[0m`).join('\n');
    const { container } = render(<ReferenceTerminal output={output} />);

    expect(container.querySelectorAll('.tool-terminal-line')).toHaveLength(121);
    expect(screen.getByText('Line 120')).toHaveStyle({ color: 'var(--ansi-green)' });
    expect(screen.queryByText('Line 121', { exact: true })).not.toBeInTheDocument();
    expect(screen.queryByText('Line 123', { exact: true })).not.toBeInTheDocument();
    expect(screen.getByText('... (3 more lines)')).toBeInTheDocument();
    expect(container.textContent).not.toContain('\x1b');
  });
});
