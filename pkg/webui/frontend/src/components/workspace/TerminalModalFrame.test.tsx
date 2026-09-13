import { createEvent, fireEvent, render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import type React from 'react';
import { describe, expect, it, vi } from 'vitest';
import TerminalModalFrame from './TerminalModalFrame';

const renderFrame = (overrides: Partial<React.ComponentProps<typeof TerminalModalFrame>> = {}) => {
  const props: React.ComponentProps<typeof TerminalModalFrame> = {
    currentStatus: '',
    cwdLabel: '/tmp/project',
    statusVariant: 'live',
    onClose: vi.fn(),
    onTerminalInput: vi.fn(),
    onTerminalArrow: vi.fn(),
    ...overrides,
  };

  const renderResult = render(<TerminalModalFrame {...props}>terminal preview</TerminalModalFrame>);

  return { ...renderResult, props };
};

describe('TerminalModalFrame', () => {
  it('renders terminal chrome without requiring xterm', () => {
    renderFrame();

    expect(screen.queryByRole('heading', { name: 'Terminal' })).not.toBeInTheDocument();
    expect(screen.queryByText('/tmp/project')).not.toBeInTheDocument();
    expect(screen.queryByRole('status')).not.toBeInTheDocument();
    expect(screen.getByText('terminal preview')).toBeInTheDocument();
    expect(screen.getByRole('complementary', { name: 'Terminal' })).toBe(
      screen.getByTestId('terminal-panel')
    );
    expect(screen.queryByTestId('terminal-modal-backdrop')).not.toBeInTheDocument();
  });

  it('renders error status styling from props', () => {
    const { container } = renderFrame({
      currentStatus: 'Terminal connection failed',
      statusVariant: 'error',
    });

    expect(screen.getByText('Terminal connection failed')).toBeInTheDocument();
    expect(container.querySelector('.workspace-terminal-status-dot')).toHaveClass('is-error');
  });

  it('centers a quiet connection message without replacing the terminal host', () => {
    const { container, rerender, props } = renderFrame({
      currentStatus: 'Restoring session…',
      statusVariant: 'connecting',
    });
    const host = screen.getByTestId('terminal-host');
    expect(screen.getByRole('status')).toHaveTextContent(/^Connecting$/);
    expect(screen.getByRole('status')).toHaveClass('workspace-terminal-connecting');
    expect(host).toHaveAttribute('aria-busy', 'true');
    expect(host).toHaveClass('is-connecting');
    expect(container.querySelector('.workspace-terminal-status-bar')).not.toBeInTheDocument();
    rerender(<TerminalModalFrame {...props} currentStatus="" statusVariant="live" />);
    expect(screen.getByTestId('terminal-host')).toBe(host);
    expect(host).not.toHaveAttribute('aria-busy');
    expect(host).not.toHaveClass('is-connecting');
    expect(screen.queryByRole('status')).not.toBeInTheDocument();
  });

  it('renders an optional pop-out action', () => {
    const onPopOut = vi.fn();

    renderFrame({ onPopOut });

    fireEvent.click(screen.getByRole('button', { name: 'Open terminal in new window' }));

    expect(onPopOut).toHaveBeenCalledTimes(1);
  });

  it.each([
    'onTerminalInput',
    'onTerminalArrow',
  ] as const)('omits controls when %s is missing', (callback) => {
    renderFrame({ [callback]: undefined });

    expect(screen.queryByRole('group', { name: 'Terminal keys' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'More keys' })).not.toBeInTheDocument();
  });

  it('sends the exact input bytes and arrow directions through separate callbacks', async () => {
    const user = userEvent.setup();
    const { props } = renderFrame();

    for (const [name, data] of [
      ['Ctrl+C', '\x03'],
      ['Ctrl+D', '\x04'],
      ['Esc', '\x1b'],
      ['Tab', '\t'],
    ]) {
      await user.click(screen.getByRole('button', { name }));
      expect(props.onTerminalInput).toHaveBeenLastCalledWith(data);
    }
    expect(props.onTerminalInput).toHaveBeenCalledTimes(4);
    expect(props.onTerminalArrow).not.toHaveBeenCalled();

    for (const [name, direction] of [
      ['Arrow left', 'D'],
      ['Arrow down', 'B'],
      ['Arrow up', 'A'],
      ['Arrow right', 'C'],
    ]) {
      await user.click(screen.getByRole('button', { name }));
      expect(props.onTerminalArrow).toHaveBeenLastCalledWith(direction);
    }
    expect(props.onTerminalArrow).toHaveBeenCalledTimes(4);
    expect(props.onTerminalInput).toHaveBeenCalledTimes(4);

    await user.click(screen.getByRole('button', { name: 'More keys' }));
    for (const [name, data] of [
      ['Ctrl+Z', '\x1a'],
      ['Ctrl+L', '\x0c'],
      ['Ctrl+A', '\x01'],
      ['Ctrl+E', '\x05'],
      ['Ctrl+U', '\x15'],
      ['Ctrl+K', '\x0b'],
      ['Ctrl+W', '\x17'],
      ['Ctrl+R', '\x12'],
    ]) {
      await user.click(screen.getByRole('button', { name }));
      expect(props.onTerminalInput).toHaveBeenLastCalledWith(data);
    }
    expect(props.onTerminalInput).toHaveBeenCalledTimes(12);
    expect(props.onTerminalArrow).toHaveBeenCalledTimes(4);
  });

  it('reveals more keys without replacing the main keys or terminal host', async () => {
    const user = userEvent.setup();
    const { props } = renderFrame();
    const host = screen.getByTestId('terminal-host');
    const mainKeys = screen.getByRole('group', { name: 'Terminal keys' });
    const toggle = screen.getByRole('button', { name: 'More keys' });
    const moreKeys = document.getElementById(toggle.getAttribute('aria-controls') ?? '');

    expect(toggle).toHaveAttribute('aria-expanded', 'false');
    expect(toggle).toHaveAccessibleName('More keys');
    expect(toggle).toHaveAttribute('title', 'More keys — show additional control keys');
    expect(moreKeys).not.toBeVisible();
    expect(screen.queryByRole('button', { name: 'Ctrl+Z' })).not.toBeInTheDocument();
    await user.click(toggle);
    expect(toggle).toHaveAttribute('aria-expanded', 'true');
    expect(screen.getByRole('button', { name: 'Less keys' })).toBe(toggle);
    expect(toggle).toHaveAttribute('title', 'Less keys — hide additional control keys');
    expect(screen.getByRole('group', { name: 'More terminal keys' })).toBe(moreKeys);
    expect(moreKeys).toBeVisible();
    expect(screen.getByRole('group', { name: 'Terminal keys' })).toBe(mainKeys);
    expect(screen.getByTestId('terminal-host')).toBe(host);
    expect(screen.getByRole('button', { name: 'Ctrl+C' })).toBeVisible();
    await user.click(toggle);
    expect(toggle).toHaveAttribute('aria-expanded', 'false');
    expect(screen.getByRole('button', { name: 'More keys' })).toBe(toggle);
    expect(toggle).toHaveAttribute('title', 'More keys — show additional control keys');
    expect(moreKeys).not.toBeVisible();
    expect(props.onTerminalInput).not.toHaveBeenCalled();
    expect(props.onTerminalArrow).not.toHaveBeenCalled();
  });

  it('uses a distinct disclosure target for each terminal frame', () => {
    renderFrame();
    renderFrame();
    const ids = screen
      .getAllByRole('button', { name: 'More keys' })
      .map((button) => button.getAttribute('aria-controls'));

    expect(new Set(ids).size).toBe(2);
    for (const id of ids) {
      expect(document.getElementById(id ?? '')).toBeInTheDocument();
    }
  });

  it.each([
    'connecting',
    'idle',
    'error',
  ] as const)('disables all input keys but leaves disclosure usable while %s', async (statusVariant) => {
    const user = userEvent.setup();
    const { props, rerender } = renderFrame({ statusVariant });
    const toggle = screen.getByRole('button', { name: 'More keys' });

    expect(toggle).toBeEnabled();
    await user.click(toggle);
    for (const name of ['Terminal keys', 'More terminal keys']) {
      for (const button of within(screen.getByRole('group', { name })).getAllByRole('button')) {
        expect(button).toBeDisabled();
        await user.click(button);
      }
    }
    expect(props.onTerminalInput).not.toHaveBeenCalled();
    expect(props.onTerminalArrow).not.toHaveBeenCalled();

    rerender(<TerminalModalFrame {...props} statusVariant="live" />);
    expect(screen.getByRole('button', { name: 'Ctrl+C' })).toBeEnabled();
    expect(screen.getByRole('button', { name: 'Ctrl+R' })).toBeEnabled();
    await user.click(screen.getByRole('button', { name: 'Ctrl+C' }));
    expect(props.onTerminalInput).toHaveBeenCalledExactlyOnceWith('\x03');
  });

  it.each([
    'Ctrl+C',
    'Arrow left',
    'More keys',
  ])('retains terminal focus and does not activate %s on pointer down or a canceled gesture', (name) => {
    const { props } = renderFrame();
    const host = screen.getByTestId('terminal-host');
    host.tabIndex = 0;
    host.focus();
    const button = screen.getByRole('button', { name });
    const pointerDown = createEvent.pointerDown(button, { bubbles: true, cancelable: true });

    fireEvent(button, pointerDown);
    expect(pointerDown.defaultPrevented).toBe(true);
    expect(host).toHaveFocus();
    fireEvent.pointerCancel(button);
    expect(props.onTerminalInput).not.toHaveBeenCalled();
    expect(props.onTerminalArrow).not.toHaveBeenCalled();
    expect(screen.getByRole('button', { name: 'More keys' })).toHaveAttribute(
      'aria-expanded',
      'false'
    );
  });

  it('sends a touch key only on completed click and keeps terminal focus', async () => {
    const user = userEvent.setup();
    const { props } = renderFrame();
    const host = screen.getByTestId('terminal-host');
    host.tabIndex = 0;
    host.focus();

    await user.pointer({
      keys: '[TouchA>]',
      target: screen.getByRole('button', { name: 'Ctrl+C' }),
    });
    expect(host).toHaveFocus();
    expect(props.onTerminalInput).not.toHaveBeenCalled();
    await user.pointer({ keys: '[/TouchA]' });
    expect(props.onTerminalInput).toHaveBeenCalledExactlyOnceWith('\x03');
    expect(host).toHaveFocus();
  });

  it('does not cancel pointer gestures on either scroll strip', async () => {
    const user = userEvent.setup();
    renderFrame();
    await user.click(screen.getByRole('button', { name: 'More keys' }));

    for (const name of ['Terminal keys', 'More terminal keys']) {
      const strip = screen.getByRole('group', { name });
      const pointerDown = createEvent.pointerDown(strip, { bubbles: true, cancelable: true });
      fireEvent(strip, pointerDown);
      expect(pointerDown.defaultPrevented).toBe(false);
    }
  });

  it.each([
    '{Enter}',
    ' ',
  ])('supports native %s activation without stealing button focus', async (key) => {
    const user = userEvent.setup();
    const { props } = renderFrame();
    const interrupt = screen.getByRole('button', { name: 'Ctrl+C' });
    await user.tab();
    expect(interrupt).toHaveFocus();
    await user.keyboard(key);
    expect(props.onTerminalInput).toHaveBeenCalledExactlyOnceWith('\x03');
    expect(interrupt).toHaveFocus();

    const arrow = screen.getByRole('button', { name: 'Arrow left' });
    arrow.focus();
    await user.keyboard(key);
    expect(props.onTerminalArrow).toHaveBeenCalledExactlyOnceWith('D');
    expect(arrow).toHaveFocus();

    const toggle = screen.getByRole('button', { name: 'More keys' });
    toggle.focus();
    await user.keyboard(key);
    expect(toggle).toHaveAttribute('aria-expanded', 'true');
    expect(toggle).toHaveFocus();
    await user.tab();
    expect(screen.getByRole('button', { name: 'Ctrl+Z' })).toHaveFocus();
    await user.keyboard(key);
    expect(props.onTerminalInput).toHaveBeenLastCalledWith('\x1a');
    expect(screen.getByRole('button', { name: 'Ctrl+Z' })).toHaveFocus();
  });

  it('disables and shields the embedded terminal while the pop-out is active', () => {
    const onPopOut = vi.fn();

    renderFrame({
      currentStatus: 'Open in pop-out window',
      onPopOut,
      popOutActive: true,
      statusVariant: 'connecting',
    });

    expect(screen.getByTestId('terminal-host')).toHaveAttribute('aria-disabled', 'true');
    expect(screen.getByTestId('terminal-host')).toHaveAttribute('inert');
    expect(screen.getByTestId('terminal-host')).not.toHaveAttribute('aria-busy');
    expect(screen.queryByText('Connecting')).not.toBeInTheDocument();
    expect(screen.queryByText('Open in pop-out window')).not.toBeInTheDocument();
    expect(screen.queryByRole('group', { name: 'Terminal keys' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'More keys' })).not.toBeInTheDocument();
    expect(screen.getByText('Terminal is open in the pop-out')).toBeInTheDocument();
    expect(screen.queryByText('Close that window to resume here.')).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Focus pop-out' }));
    expect(onPopOut).toHaveBeenCalledTimes(1);
  });
});
