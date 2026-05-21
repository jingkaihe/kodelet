import type React from 'react';
import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import TerminalModalFrame from './TerminalModalFrame';

const renderFrame = (
  overrides: Partial<React.ComponentProps<typeof TerminalModalFrame>> = {}
) => {
  const props: React.ComponentProps<typeof TerminalModalFrame> = {
    currentStatus: 'Connected',
    cwdLabel: '/tmp/project',
    statusVariant: 'live',
    terminalSize: { width: 980, height: 620 },
    onClose: vi.fn(),
    onResizeStart: vi.fn(),
    ...overrides,
  };

  const renderResult = render(
    <TerminalModalFrame {...props}>terminal preview</TerminalModalFrame>
  );

  return { ...renderResult, props };
};

describe('TerminalModalFrame', () => {
  it('renders terminal chrome without requiring xterm', () => {
    renderFrame();

    expect(screen.getByRole('heading', { name: 'Terminal' })).toBeInTheDocument();
    expect(screen.getByText('/tmp/project')).toBeInTheDocument();
    expect(screen.getByText('Connected')).toBeInTheDocument();
    expect(screen.getByText('terminal preview')).toBeInTheDocument();
  });

  it('keeps close and resize behavior external', () => {
    const { props } = renderFrame();

    fireEvent.click(screen.getByRole('button', { name: 'Close' }));
    fireEvent.pointerDown(screen.getByTestId('terminal-resize-handle'));

    expect(props.onClose).toHaveBeenCalledTimes(1);
    expect(props.onResizeStart).toHaveBeenCalledTimes(1);
  });

  it('renders error status styling from props', () => {
    const { container } = renderFrame({
      currentStatus: 'Terminal connection failed',
      statusVariant: 'error',
    });

    expect(screen.getByText('Terminal connection failed')).toBeInTheDocument();
    expect(container.querySelector('.workspace-terminal-status-dot')).toHaveClass(
      'is-error'
    );
  });
});
