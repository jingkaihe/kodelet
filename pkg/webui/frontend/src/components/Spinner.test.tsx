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

    act(() => vi.advanceTimersByTime(124));
    expect(spinner).toHaveTextContent('⣾');

    act(() => vi.advanceTimersByTime(1));
    expect(spinner).toHaveTextContent('⣽');

    unmount();
    expect(vi.getTimerCount()).toBe(0);
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
