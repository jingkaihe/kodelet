import { describe, it, expect, vi } from 'vitest';
import { render, waitFor } from '@testing-library/react';
import App from './App';

vi.mock('./pages/ChatPage', () => ({
  default: () => <div data-testid="chat-page">Chat page</div>,
}));

describe('App', () => {
  it('renders without crashing', async () => {
    const { container } = render(<App />);

    await waitFor(() => {
      expect(container.querySelector('.min-h-screen')).toBeInTheDocument();
    });
  });

  it('provides base application structure', async () => {
    const { container } = render(<App />);

    await waitFor(() => {
      const wrapper = container.firstElementChild;
      expect(wrapper).toHaveClass('min-h-screen');
    });
  });
});
