import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import EmptyState from './EmptyState';

describe('EmptyState', () => {
  const defaultProps = {
    icon: '📭',
    title: 'No items found',
    description: 'There are no items to display',
  };

  it('renders icon, title, and description', () => {
    render(<EmptyState {...defaultProps} />);
    
    expect(screen.getByText('📭')).toBeInTheDocument();
    expect(screen.getByText('No items found')).toBeInTheDocument();
    expect(screen.getByText('There are no items to display')).toBeInTheDocument();
  });

  it('renders icon with correct accessibility attributes', () => {
    render(<EmptyState {...defaultProps} />);
    
    const iconElement = screen.getByRole('img', { name: 'No items found' });
    expect(iconElement).toBeInTheDocument();
    expect(iconElement).toHaveTextContent('📭');
  });

  it('renders optional action', () => {
    const action = <button>Add new item</button>;
    render(<EmptyState {...defaultProps} action={action} />);
    
    const button = screen.getByRole('button', { name: 'Add new item' });
    expect(button).toBeInTheDocument();
  });

  it('does not render action container when no action provided', () => {
    const { container } = render(<EmptyState {...defaultProps} />);
    
    const actionContainer = container.querySelector('.mt-6');
    expect(actionContainer).not.toBeInTheDocument();
  });

  it('applies correct styling classes', () => {
    const { container } = render(<EmptyState {...defaultProps} />);
    
    const wrapper = container.firstChild as HTMLElement;
    expect(wrapper).toHaveClass('text-center', 'py-12');
    
    const iconElement = screen.getByRole('img');
    expect(iconElement).toHaveClass('text-6xl', 'mb-4');
    
    const title = screen.getByText('No items found');
    expect(title).toHaveClass('mt-4', 'text-xl', 'font-semibold', 'text-base-content/70');
    
    const description = screen.getByText('There are no items to display');
    expect(description).toHaveClass('mt-2', 'text-base-content/50');
  });

  it('renders complex action elements', () => {
    const complexAction = (
      <div>
        <button>Primary Action</button>
        <button>Secondary Action</button>
      </div>
    );
    
    render(<EmptyState {...defaultProps} action={complexAction} />);
    
    expect(screen.getByRole('button', { name: 'Primary Action' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Secondary Action' })).toBeInTheDocument();
  });

  it('handles different emoji icons', () => {
    render(<EmptyState {...defaultProps} icon="🔍" />);
    
    expect(screen.getByText('🔍')).toBeInTheDocument();
  });

  it('handles multi-line descriptions', () => {
    const multiLineDescription = 'This is a longer description\nthat spans multiple lines';
    render(<EmptyState {...defaultProps} description={multiLineDescription} />);
    
    // Check that the text content includes both parts
    const description = screen.getByText((_content, element) => {
      return element?.textContent === 'This is a longer description\nthat spans multiple lines';
    });
    expect(description).toBeInTheDocument();
  });
});