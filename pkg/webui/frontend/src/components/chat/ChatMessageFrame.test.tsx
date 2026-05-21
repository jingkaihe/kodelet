import { fireEvent, render, screen } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import ChatMessageFrame from './ChatMessageFrame';

const { copyToClipboardMock } = vi.hoisted(() => ({
  copyToClipboardMock: vi.fn(),
}));

vi.mock('../../utils', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../utils')>();
  return {
    ...actual,
    copyToClipboard: copyToClipboardMock,
  };
});

describe('ChatMessageFrame', () => {
  beforeEach(() => {
    copyToClipboardMock.mockReset();
  });

  it('renders user message chrome and copy action', () => {
    render(
      <ChatMessageFrame copyText="copy me" role="user">
        <p>User content</p>
      </ChatMessageFrame>
    );

    fireEvent.click(screen.getByRole('button', { name: 'Copy to clipboard' }));

    expect(screen.getByText('You')).toBeInTheDocument();
    expect(screen.getByText('User content')).toBeInTheDocument();
    expect(copyToClipboardMock).toHaveBeenCalledWith('copy me');
  });

  it('renders assistant message chrome without a panel-level copy action', () => {
    render(
      <ChatMessageFrame copyText="assistant copy" role="assistant">
        <p>Assistant content</p>
      </ChatMessageFrame>
    );

    expect(screen.getByText('Kodelet')).toBeInTheDocument();
    expect(screen.getByText('Assistant content')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Copy to clipboard' })).not.toBeInTheDocument();
  });
});
