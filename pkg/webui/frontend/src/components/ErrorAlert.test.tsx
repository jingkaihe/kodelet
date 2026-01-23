import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import ErrorAlert from './ErrorAlert';

describe('ErrorAlert', () => {
  it('displays error message to user', () => {
    render(<ErrorAlert message="Network connection failed" />);

    expect(screen.getByRole('alert')).toBeInTheDocument();
    expect(screen.getByText('Network connection failed')).toBeInTheDocument();
  });

  it('provides retry functionality when callback is provided', () => {
    const mockOnRetry = vi.fn();
    render(<ErrorAlert message="Failed to load data" onRetry={mockOnRetry} />);

    const retryButton = screen.getByRole('button', { name: /retry/i });
    fireEvent.click(retryButton);

    expect(mockOnRetry).toHaveBeenCalledTimes(1);
  });

  it('renders without retry option when no callback provided', () => {
    render(<ErrorAlert message="Permanent error" />);

    expect(screen.queryByRole('button')).not.toBeInTheDocument();
  });
});