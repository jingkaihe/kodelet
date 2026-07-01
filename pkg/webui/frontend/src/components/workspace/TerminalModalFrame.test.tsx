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
    onClose: vi.fn(),
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

    expect(screen.queryByRole('heading', { name: 'Terminal' })).not.toBeInTheDocument();
    expect(screen.queryByText('/tmp/project')).not.toBeInTheDocument();
    expect(screen.getByText('Connected')).toBeInTheDocument();
    expect(screen.getByText('terminal preview')).toBeInTheDocument();
    expect(screen.getByTestId('terminal-panel')).toHaveAttribute('role', 'complementary');
    expect(screen.queryByTestId('terminal-modal-backdrop')).not.toBeInTheDocument();
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

  it('renders an optional pop-out action', () => {
    const onPopOut = vi.fn();

    renderFrame({ onPopOut });

    fireEvent.click(screen.getByRole('button', { name: 'Open terminal in new window' }));

    expect(onPopOut).toHaveBeenCalledTimes(1);
  });
});
