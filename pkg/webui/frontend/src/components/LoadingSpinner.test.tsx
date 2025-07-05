import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import LoadingSpinner from './LoadingSpinner';

describe('LoadingSpinner', () => {
  it('renders with default message', () => {
    render(<LoadingSpinner />);
    
    expect(screen.getByText('Loading...')).toBeInTheDocument();
  });

  it('renders with custom message', () => {
    render(<LoadingSpinner message="Please wait..." />);
    
    expect(screen.getByText('Please wait...')).toBeInTheDocument();
  });

  it('renders with default size (lg)', () => {
    const { container } = render(<LoadingSpinner />);
    
    const spinner = container.querySelector('.loading');
    expect(spinner).toHaveClass('loading-spinner', 'loading-lg');
  });

  it('renders with small size', () => {
    const { container } = render(<LoadingSpinner size="sm" />);
    
    const spinner = container.querySelector('.loading');
    expect(spinner).toHaveClass('loading-spinner', 'loading-sm');
  });

  it('renders with medium size', () => {
    const { container } = render(<LoadingSpinner size="md" />);
    
    const spinner = container.querySelector('.loading');
    expect(spinner).toHaveClass('loading-spinner', 'loading-md');
  });

  it('renders with large size', () => {
    const { container } = render(<LoadingSpinner size="lg" />);
    
    const spinner = container.querySelector('.loading');
    expect(spinner).toHaveClass('loading-spinner', 'loading-lg');
  });

  it('applies correct wrapper styling', () => {
    const { container } = render(<LoadingSpinner />);
    
    const wrapper = container.firstChild as HTMLElement;
    expect(wrapper).toHaveClass('text-center', 'py-8');
  });

  it('applies correct message styling', () => {
    render(<LoadingSpinner message="Test message" />);
    
    const message = screen.getByText('Test message');
    expect(message).toHaveClass('mt-4', 'text-base-content/70');
  });

  it('renders empty message when provided', () => {
    render(<LoadingSpinner message="" />);
    
    // The paragraph should still exist but be empty
    const { container } = render(<LoadingSpinner message="" />);
    const paragraph = container.querySelector('p');
    expect(paragraph).toBeInTheDocument();
    expect(paragraph).toHaveTextContent('');
  });
});