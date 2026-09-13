import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  getTheme,
  initializeTheme,
  setTheme,
  subscribeTheme,
  THEME_STORAGE_KEY,
  THEMES,
} from './theme';

describe('Web UI themes', () => {
  let cleanup: (() => void) | undefined;
  let meta: HTMLMetaElement;
  let systemAppearance: MediaQueryList;

  const changeSystemAppearance = (dark: boolean) => {
    Object.defineProperty(systemAppearance, 'matches', { value: dark, configurable: true });
    systemAppearance.dispatchEvent(new Event('change'));
  };

  beforeEach(() => {
    localStorage.removeItem(THEME_STORAGE_KEY);
    document.documentElement.dataset.theme = 'kodelet';
    delete document.documentElement.dataset.themePreference;
    systemAppearance = Object.assign(new EventTarget(), {
      matches: false,
      media: '(prefers-color-scheme: dark)',
      onchange: null,
      addListener: vi.fn(),
      removeListener: vi.fn(),
    });
    vi.spyOn(window, 'matchMedia').mockReturnValue(systemAppearance);
    meta = document.createElement('meta');
    meta.name = 'theme-color';
    document.head.append(meta);
  });

  afterEach(() => {
    cleanup?.();
    vi.restoreAllMocks();
    meta.remove();
    localStorage.removeItem(THEME_STORAGE_KEY);
    document.documentElement.dataset.theme = 'kodelet';
    delete document.documentElement.dataset.themePreference;
  });

  it.each(THEMES)('restores $label and its browser chrome color', ({ id, background }) => {
    localStorage.setItem(THEME_STORAGE_KEY, id);
    cleanup = initializeTheme();
    expect(getTheme()).toBe(id);
    expect(document.documentElement.dataset.theme).toBe(id);
    expect(meta.content).toBe(background);
  });

  it.each([null, 'unknown', 'dark', 'system'])('follows the system for saved value %s', (saved) => {
    if (saved !== null) localStorage.setItem(THEME_STORAGE_KEY, saved);
    cleanup = initializeTheme();
    expect(getTheme()).toBe('system');
    expect(document.documentElement.dataset.theme).toBe('kodelet');
    expect(meta.content).toBe('#faf8ef');
    changeSystemAppearance(true);
    expect(getTheme()).toBe('system');
    expect(document.documentElement.dataset.theme).toBe('gruvbox-dark');
    expect(meta.content).toBe('#282828');
    changeSystemAppearance(false);
    expect(document.documentElement.dataset.theme).toBe('kodelet');
    expect(localStorage.getItem(THEME_STORAGE_KEY)).toBe(saved);
  });

  it('starts dark when the system is already dark and removes its media listener on cleanup', () => {
    changeSystemAppearance(true);
    cleanup = initializeTheme();
    expect(window.matchMedia).toHaveBeenCalledWith('(prefers-color-scheme: dark)');
    expect(getTheme()).toBe('system');
    expect(document.documentElement.dataset.theme).toBe('gruvbox-dark');
    cleanup();
    changeSystemAppearance(false);
    expect(document.documentElement.dataset.theme).toBe('gruvbox-dark');
  });

  it.each(THEMES)('keeps an explicit $label choice fixed until System is selected', ({ id }) => {
    cleanup = initializeTheme();
    setTheme(id);
    changeSystemAppearance(true);
    expect(getTheme()).toBe(id);
    expect(document.documentElement.dataset.theme).toBe(id);
    changeSystemAppearance(false);
    expect(document.documentElement.dataset.theme).toBe(id);
    changeSystemAppearance(true);
    setTheme('system');
    expect(getTheme()).toBe('system');
    expect(document.documentElement.dataset.theme).toBe('gruvbox-dark');
    expect(localStorage.getItem(THEME_STORAGE_KEY)).toBe('system');
    changeSystemAppearance(false);
    expect(document.documentElement.dataset.theme).toBe('kodelet');
  });

  it('persists choices and notifies subscribers without changing other document attributes', () => {
    cleanup = initializeTheme();
    const listener = vi.fn();
    const unsubscribe = subscribeTheme(listener);
    const originalStyle = document.documentElement.getAttribute('style');
    setTheme('gruvbox-dark');
    expect(localStorage.getItem(THEME_STORAGE_KEY)).toBe('gruvbox-dark');
    expect(getTheme()).toBe('gruvbox-dark');
    expect(listener).toHaveBeenCalledOnce();
    expect(document.documentElement.getAttribute('style')).toBe(originalStyle);
    unsubscribe();
    setTheme('kodelet-classic');
    expect(listener).toHaveBeenCalledOnce();
  });

  it('still switches when browser storage is blocked', () => {
    changeSystemAppearance(true);
    vi.spyOn(window, 'localStorage', 'get').mockImplementation(() => {
      throw new DOMException('Storage blocked', 'SecurityError');
    });
    cleanup = initializeTheme();
    expect(getTheme()).toBe('system');
    expect(document.documentElement.dataset.theme).toBe('gruvbox-dark');
    changeSystemAppearance(false);
    expect(document.documentElement.dataset.theme).toBe('kodelet');
    expect(() => setTheme('gruvbox-dark')).not.toThrow();
    expect(getTheme()).toBe('gruvbox-dark');
    expect(meta.content).toBe('#282828');
  });

  it('syncs changes and resets from other tabs, ignoring unrelated storage events', () => {
    cleanup = initializeTheme();
    const dispatch = (key: string | null, value: string | null, area = localStorage) => {
      window.dispatchEvent(
        new StorageEvent('storage', { key, newValue: value, storageArea: area })
      );
    };
    dispatch(THEME_STORAGE_KEY, 'gruvbox-dark', sessionStorage);
    dispatch('other-preference', 'gruvbox-dark');
    expect(getTheme()).toBe('system');
    dispatch(THEME_STORAGE_KEY, 'gruvbox-dark');
    expect(getTheme()).toBe('gruvbox-dark');
    dispatch(THEME_STORAGE_KEY, null);
    expect(getTheme()).toBe('system');
    expect(document.documentElement.dataset.theme).toBe('kodelet');
    dispatch(THEME_STORAGE_KEY, 'kodelet-classic');
    expect(getTheme()).toBe('kodelet-classic');
    changeSystemAppearance(true);
    expect(document.documentElement.dataset.theme).toBe('kodelet-classic');
    dispatch(THEME_STORAGE_KEY, 'system');
    expect(getTheme()).toBe('system');
    expect(document.documentElement.dataset.theme).toBe('gruvbox-dark');
    dispatch(THEME_STORAGE_KEY, 'kodelet-classic');
    dispatch(null, null);
    expect(getTheme()).toBe('system');
    expect(document.documentElement.dataset.theme).toBe('gruvbox-dark');
    cleanup();
    dispatch(THEME_STORAGE_KEY, 'gruvbox-dark');
    expect(getTheme()).toBe('system');
  });
});
