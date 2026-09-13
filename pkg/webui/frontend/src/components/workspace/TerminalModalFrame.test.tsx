import { fireEvent, render, screen } from '@testing-library/react';
import type React from 'react';
import { describe, expect, it, vi } from 'vitest';
import TerminalModalFrame from './TerminalModalFrame';

const renderFrame = (overrides: Partial<React.ComponentProps<typeof TerminalModalFrame>> = {}) => {
  const props: React.ComponentProps<typeof TerminalModalFrame> = {
    currentStatus: '',
    cwdLabel: '/tmp/project',
    statusVariant: 'live',
    onClose: vi.fn(),
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
    expect(screen.getByText('Terminal is open in the pop-out')).toBeInTheDocument();
    expect(screen.queryByText('Close that window to resume here.')).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Focus pop-out' }));
    expect(onPopOut).toHaveBeenCalledTimes(1);
  });
});
