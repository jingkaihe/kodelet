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

  it('keeps the same busy cadence when reduced motion is requested', () => {
    vi.useFakeTimers();
    const motionPreference = Object.assign(new EventTarget(), { matches: true });
    vi.spyOn(window, 'matchMedia').mockReturnValue(motionPreference as MediaQueryList);

    const { container, unmount } = render(<Spinner />);
    const spinner = container.querySelector('.spinner-glyph');

    act(() => vi.advanceTimersByTime(124));
    expect(spinner).toHaveTextContent('⣾');
    expect(spinner?.querySelector('circle')).toHaveAttribute('opacity', '0.15');

    act(() => vi.advanceTimersByTime(1));
    expect(spinner).toHaveTextContent('⣽');
    expect(spinner?.querySelector('circle')).toHaveAttribute('opacity', '1');

    unmount();
    expect(vi.getTimerCount()).toBe(0);
  });

  it('adapts to motion preference changes and cleans up its listener and timer', () => {
    vi.useFakeTimers();
    const motionPreference = Object.assign(new EventTarget(), { matches: true });
    vi.spyOn(window, 'matchMedia').mockReturnValue(motionPreference as MediaQueryList);
    const removeListener = vi.spyOn(motionPreference, 'removeEventListener');
    const { container, unmount } = render(<Spinner />);
    const spinner = container.querySelector('.spinner-glyph');

    act(() => {
      motionPreference.matches = false;
      motionPreference.dispatchEvent(new Event('change'));
    });
    expect(vi.getTimerCount()).toBe(1);
    act(() => vi.advanceTimersByTime(125));
    expect(spinner).toHaveTextContent('⣽');

    act(() => {
      motionPreference.matches = true;
      motionPreference.dispatchEvent(new Event('change'));
    });
    expect(vi.getTimerCount()).toBe(1);
    act(() => vi.advanceTimersByTime(124));
    expect(spinner).toHaveTextContent('⣽');
    act(() => vi.advanceTimersByTime(1));
    expect(spinner).toHaveTextContent('⣻');

    unmount();
    expect(removeListener).toHaveBeenCalledWith('change', expect.any(Function));
    expect(vi.getTimerCount()).toBe(0);
    motionPreference.dispatchEvent(new Event('change'));
    expect(vi.getTimerCount()).toBe(0);
  });

  it('keeps presets without a reduced-motion cadence static until motion is enabled', () => {
    vi.useFakeTimers();
    const motionPreference = Object.assign(new EventTarget(), { matches: true });
    vi.spyOn(window, 'matchMedia').mockReturnValue(motionPreference as MediaQueryList);
    const { container } = render(<Spinner preset={{ frames: ['a', 'b'], intervalMs: 100 }} />);
    const spinner = container.querySelector('.spinner-glyph');

    expect(spinner).toHaveTextContent('a');
    expect(vi.getTimerCount()).toBe(0);

    act(() => {
      motionPreference.matches = false;
      motionPreference.dispatchEvent(new Event('change'));
    });
    act(() => vi.advanceTimersByTime(100));
    expect(spinner).toHaveTextContent('b');

    act(() => {
      motionPreference.matches = true;
      motionPreference.dispatchEvent(new Event('change'));
      vi.advanceTimersByTime(1000);
    });
    expect(spinner).toHaveTextContent('b');
    expect(vi.getTimerCount()).toBe(0);
  });
});
