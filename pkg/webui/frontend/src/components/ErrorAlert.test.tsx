import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import ErrorAlert from './ErrorAlert';

describe('ErrorAlert', () => {
  it('renders error message', () => {
    render(<ErrorAlert message="Something went wrong" />);
    
    expect(screen.getByText('Something went wrong')).toBeInTheDocument();
  });

  it('renders with alert role', () => {
    render(<ErrorAlert message="Test error" />);
    
    const alert = screen.getByRole('alert');
    expect(alert).toHaveClass('alert', 'alert-error');
  });

  it('renders retry button when onRetry is provided', () => {
    const mockOnRetry = vi.fn();
    render(<ErrorAlert message="Error" onRetry={mockOnRetry} />);
    
    const retryButton = screen.getByRole('button', { name: /retry operation/i });
    expect(retryButton).toBeInTheDocument();
    expect(retryButton).toHaveTextContent('Retry');
  });

  it('does not render retry button when onRetry is not provided', () => {
    render(<ErrorAlert message="Error" />);
    
    expect(screen.queryByRole('button', { name: /retry operation/i })).not.toBeInTheDocument();
  });

  it('calls onRetry when retry button is clicked', () => {
    const mockOnRetry = vi.fn();
    render(<ErrorAlert message="Error" onRetry={mockOnRetry} />);
    
    const retryButton = screen.getByRole('button', { name: /retry operation/i });
    fireEvent.click(retryButton);
    
    expect(mockOnRetry).toHaveBeenCalledTimes(1);
  });

  it('renders error icon', () => {
    const { container } = render(<ErrorAlert message="Error" />);
    
    const svg = container.querySelector('svg');
    expect(svg).toBeInTheDocument();
    expect(svg).toHaveClass('stroke-current', 'shrink-0', 'h-6', 'w-6');
  });

  it('handles long error messages', () => {
    const longMessage = 'This is a very long error message that might wrap to multiple lines in the UI';
    render(<ErrorAlert message={longMessage} />);
    
    expect(screen.getByText(longMessage)).toBeInTheDocument();
  });

  it('retry button has correct styling', () => {
    render(<ErrorAlert message="Error" onRetry={() => {}} />);
    
    const retryButton = screen.getByRole('button', { name: /retry operation/i });
    expect(retryButton).toHaveClass('btn', 'btn-sm', 'btn-outline');
  });
});