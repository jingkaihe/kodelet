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

  it('renders confirmation prompts without a free-form input', () => {
    const onCancel = vi.fn();
    const onSubmit = vi.fn();
    render(
      <UIInputDialog
        mode="confirm"
        request={{
          id: 'confirm-1',
          title: 'Allow bash?',
          message: 'A tool call incoming',
          confirmButtonText: 'Allow',
        }}
        onCancel={onCancel}
        onSubmit={onSubmit}
      />
    );

    expect(screen.getByText('A tool call incoming')).toBeInTheDocument();
    expect(screen.queryByTestId('ui-input-response')).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Allow' }));
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));

    expect(onSubmit).toHaveBeenCalledWith('');
    expect(onCancel).toHaveBeenCalledTimes(1);
  });

  it('renders select prompts and submits the selected option', async () => {
    const onSubmit = vi.fn();
    render(
      <UIInputDialog
        mode="select"
        request={{
          id: 'select-1',
          title: 'What is your favourite food?',
          message: 'Choose what you like.',
          options: ['Pasta', 'Pizza', 'Focaccia'],
        }}
        onCancel={vi.fn()}
        onSubmit={onSubmit}
      />
    );

    await waitFor(() => {
      expect(screen.getByTestId('ui-select-response')).toHaveFocus();
    });
    fireEvent.change(screen.getByTestId('ui-select-response'), {
      target: { value: 'Pizza' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Select' }));

    expect(onSubmit).toHaveBeenCalledWith('Pizza');
  });

  it('disables select submission when no options are available', () => {
    const onSubmit = vi.fn();
    render(
      <UIInputDialog
        mode="select"
        request={{
          id: 'select-empty',
          title: 'Pick one',
          options: [],
        }}
        onCancel={vi.fn()}
        onSubmit={onSubmit}
      />
    );

    const submitButton = screen.getByRole('button', { name: 'Select' });
    expect(submitButton).toBeDisabled();

    fireEvent.click(submitButton);
    expect(onSubmit).not.toHaveBeenCalled();
  });
});
