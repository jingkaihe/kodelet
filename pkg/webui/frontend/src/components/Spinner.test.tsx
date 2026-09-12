import { act, render } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import Spinner from './Spinner';

afterEach(() => {
  vi.restoreAllMocks();
  vi.useRealTimers();
});

describe('Spinner', () => {
  it('uses the TUI dot frames at eight updates per second', () => {
    vi.useFakeTimers();
    const { container, unmount } = render(<Spinner />);
    const spinner = container.querySelector('.spinner-glyph');

    expect(spinner).toHaveTextContent('⣾');
    expect(spinner?.querySelectorAll('circle[opacity="1"]')).toHaveLength(7);
    expect(spinner?.querySelector('circle')).toHaveAttribute('opacity', '0.15');

    act(() => vi.advanceTimersByTime(124));
    expect(spinner).toHaveTextContent('⣾');

    act(() => vi.advanceTimersByTime(1));
    expect(spinner).toHaveTextContent('⣽');
    expect(spinner?.querySelector('circle')).toHaveAttribute('opacity', '1');
    expect(spinner?.querySelectorAll('circle')[1]).toHaveAttribute('opacity', '0.15');

    unmount();
    expect(vi.getTimerCount()).toBe(0);
  });

  it('restarts the animation when the reset key changes', () => {
    vi.useFakeTimers();
    const { container, rerender } = render(<Spinner resetKey="first-turn" />);
    const spinner = container.querySelector('.spinner-glyph');

    act(() => vi.advanceTimersByTime(125));
    expect(spinner).toHaveTextContent('⣽');

    rerender(<Spinner resetKey="second-turn" />);
    expect(spinner).toHaveTextContent('⣾');
    expect(vi.getTimerCount()).toBe(1);

    act(() => vi.advanceTimersByTime(125));
    expect(spinner).toHaveTextContent('⣽');
  });

  it('stays static when reduced motion is requested', () => {
    vi.useFakeTimers();
    vi.spyOn(window, 'matchMedia').mockReturnValue({
      matches: true,
    } as MediaQueryList);

    const { container } = render(<Spinner />);

    expect(container.querySelector('.spinner-glyph')).toHaveTextContent('⣾');
    expect(vi.getTimerCount()).toBe(0);
  });
});
