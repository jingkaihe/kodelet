import { act, render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { initializeTheme, setTheme, THEME_STORAGE_KEY } from '../theme';
import ThemePicker from './ThemePicker';

describe('ThemePicker', () => {
  let cleanup: () => void;
  beforeEach(() => {
    localStorage.removeItem(THEME_STORAGE_KEY);
    cleanup = initializeTheme();
  });
  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
    localStorage.removeItem(THEME_STORAGE_KEY);
    document.documentElement.dataset.theme = 'kodelet';
    delete document.documentElement.dataset.themePreference;
  });

  it('exposes all palettes, applies the selection, and returns focus', async () => {
    const user = userEvent.setup();
    render(<ThemePicker />);
    const trigger = screen.getByRole('button', { name: 'Choose theme' });
    await user.click(trigger);
    expect(trigger).toHaveAttribute('aria-expanded', 'true');
    expect(screen.getAllByRole('menuitemradio').map((option) => option.textContent)).toEqual([
      'System',
      'Gruvbox light',
      'Gruvbox dark',
      'Classic light',
    ]);
    expect(screen.queryByRole('combobox')).not.toBeInTheDocument();
    expect(screen.getByRole('menuitemradio', { name: 'Gruvbox light' })).toBeInTheDocument();
    expect(screen.getByRole('menuitemradio', { name: 'System' })).toHaveAttribute(
      'aria-checked',
      'true'
    );
    await user.click(screen.getByRole('menuitemradio', { name: 'Gruvbox dark' }));
    expect(localStorage.getItem(THEME_STORAGE_KEY)).toBe('gruvbox-dark');
    expect(document.documentElement).toHaveAttribute('data-theme', 'gruvbox-dark');
    expect(trigger).toHaveAttribute('title', 'Theme: Gruvbox dark');
    expect(trigger).toHaveFocus();
    expect(screen.queryByRole('menu')).not.toBeInTheDocument();
  });

  it('supports arrow keys, Home, End, Enter and Escape without changing on navigation', async () => {
    const user = userEvent.setup();
    render(<ThemePicker />);
    const trigger = screen.getByRole('button', { name: 'Choose theme' });
    trigger.focus();
    await user.keyboard('{Enter}');
    expect(screen.getByRole('menuitemradio', { name: 'System' })).toHaveFocus();
    await user.keyboard('{ArrowUp}');
    expect(screen.getByRole('menuitemradio', { name: 'Classic light' })).toHaveFocus();
    await user.keyboard('{Home}{ArrowDown}{ArrowDown}');
    expect(screen.getByRole('menuitemradio', { name: 'Gruvbox dark' })).toHaveFocus();
    expect(document.documentElement).toHaveAttribute('data-theme', 'kodelet');
    await user.keyboard('{End}{Enter}');
    expect(document.documentElement).toHaveAttribute('data-theme', 'kodelet-classic');
    await user.keyboard('{Enter}{Escape}');
    expect(trigger).toHaveFocus();
    expect(screen.queryByRole('menu')).not.toBeInTheDocument();
  });

  it('dismisses on outside clicks and when tabbing away', async () => {
    const user = userEvent.setup();
    render(
      <>
        <ThemePicker />
        <button type="button">Outside</button>
      </>
    );
    const trigger = screen.getByRole('button', { name: 'Choose theme' });
    await user.click(trigger);
    await user.click(screen.getByRole('button', { name: 'Outside' }));
    expect(screen.queryByRole('menu')).not.toBeInTheDocument();
    await user.click(trigger);
    await user.tab();
    expect(screen.getByRole('button', { name: 'Outside' })).toHaveFocus();
    expect(screen.queryByRole('menu')).not.toBeInTheDocument();
  });

  it('keeps multiple pickers in sync with external theme changes', async () => {
    const user = userEvent.setup();
    render(
      <>
        <ThemePicker />
        <ThemePicker />
      </>
    );
    act(() => setTheme('kodelet-classic'));
    const triggers = screen.getAllByRole('button', { name: 'Choose theme' });
    expect(triggers.every((trigger) => trigger.title === 'Theme: Classic light')).toBe(true);
    await user.click(triggers[1]);
    expect(
      within(screen.getByRole('menu')).getByRole('menuitemradio', { name: 'Classic light' })
    ).toHaveAttribute('aria-checked', 'true');
  });

  it('keeps System checked when the OS appearance changes and can return to it', async () => {
    cleanup();
    const systemAppearance = Object.assign(new EventTarget(), { matches: false });
    vi.spyOn(window, 'matchMedia').mockReturnValue(systemAppearance as MediaQueryList);
    cleanup = initializeTheme();
    const user = userEvent.setup();
    render(<ThemePicker />);
    const trigger = screen.getByRole('button', { name: 'Choose theme' });
    await user.click(trigger);
    act(() => {
      systemAppearance.matches = true;
      systemAppearance.dispatchEvent(new Event('change'));
    });
    expect(document.documentElement).toHaveAttribute('data-theme', 'gruvbox-dark');
    expect(screen.getByRole('menuitemradio', { name: 'System' })).toHaveAttribute(
      'aria-checked',
      'true'
    );
    expect(screen.getByRole('menuitemradio', { name: 'Gruvbox dark' })).toHaveAttribute(
      'aria-checked',
      'false'
    );
    expect(trigger).toHaveAttribute('title', 'Theme: System');
    await user.click(screen.getByRole('menuitemradio', { name: 'Gruvbox light' }));
    expect(document.documentElement).toHaveAttribute('data-theme', 'kodelet');
    expect(localStorage.getItem(THEME_STORAGE_KEY)).toBe('kodelet');
    await user.click(trigger);
    await user.click(screen.getByRole('menuitemradio', { name: 'System' }));
    expect(document.documentElement).toHaveAttribute('data-theme', 'gruvbox-dark');
    expect(localStorage.getItem(THEME_STORAGE_KEY)).toBe('system');
    expect(trigger).toHaveAttribute('title', 'Theme: System');
  });
});
