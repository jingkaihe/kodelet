import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import UIInputDialog from './UIInputDialog';

const baseRequest = {
  id: 'input-1',
  title: 'What is your birthday?',
  placeholder: 'e.g., January 1, 1990',
};

describe('UIInputDialog', () => {
  it('renders the question-focused prompt without extra labels', async () => {
    render(
      <UIInputDialog
        request={baseRequest}
        onCancel={vi.fn()}
        onSubmit={vi.fn()}
      />
    );

    expect(screen.getByRole('heading', { name: 'What is your birthday?' })).toBeInTheDocument();
    expect(screen.queryByText('Extension prompt')).not.toBeInTheDocument();
    expect(screen.queryByText('Response')).not.toBeInTheDocument();
    await waitFor(() => {
      expect(screen.getByPlaceholderText('e.g., January 1, 1990')).toHaveFocus();
    });
  });

  it('submits entered text and allows dismissal', () => {
    const onCancel = vi.fn();
    const onSubmit = vi.fn();
    render(
      <UIInputDialog
        request={baseRequest}
        onCancel={onCancel}
        onSubmit={onSubmit}
      />
    );

    fireEvent.change(screen.getByTestId('ui-input-response'), {
      target: { value: 'January 1, 1990' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Submit' }));
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));

    expect(onSubmit).toHaveBeenCalledWith('January 1, 1990');
    expect(onCancel).toHaveBeenCalledTimes(1);
  });

  it('disables submit for required empty answers', () => {
    const onSubmit = vi.fn();
    render(
      <UIInputDialog
        request={{ ...baseRequest, required: true }}
        onCancel={vi.fn()}
        onSubmit={onSubmit}
      />
    );

    const submitButton = screen.getByRole('button', { name: 'Submit' });
    expect(submitButton).toBeDisabled();

    fireEvent.click(submitButton);
    expect(onSubmit).not.toHaveBeenCalled();
  });
});
