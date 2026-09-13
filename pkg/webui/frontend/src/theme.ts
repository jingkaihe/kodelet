export const THEMES = [
  {
    id: 'kodelet',
    label: 'Gruvbox light',
    background: '#faf8ef',
    surface: '#f8f5e9',
    foreground: '#3c3836',
  },
  {
    id: 'gruvbox-dark',
    label: 'Gruvbox dark',
    background: '#282828',
    surface: '#252525',
    foreground: '#ebdbb2',
  },
  {
    id: 'kodelet-classic',
    label: 'Classic light',
    background: '#faf9f5',
    surface: '#f4efe5',
    foreground: '#141413',
  },
] as const;

export type ThemeId = (typeof THEMES)[number]['id'];
export type ThemePreference = ThemeId | 'system';
export const THEME_OPTIONS = [{ id: 'system', label: 'System' }, ...THEMES] as const;
export const THEME_STORAGE_KEY = 'kodelet.theme';
const THEME_CHANGE_EVENT = 'kodelet:theme-change';
const SYSTEM_DARK_QUERY = '(prefers-color-scheme: dark)';

const normalizeTheme = (value: string | null | undefined): ThemePreference =>
  THEME_OPTIONS.find((theme) => theme.id === value)?.id ?? 'system';

export const getTheme = (): ThemePreference =>
  normalizeTheme(document.documentElement.dataset.themePreference);

const applyTheme = (preference: ThemePreference) => {
  const theme =
    preference === 'system'
      ? window.matchMedia(SYSTEM_DARK_QUERY).matches
        ? 'gruvbox-dark'
        : 'kodelet'
      : preference;
  document.documentElement.dataset.themePreference = preference;
  document.documentElement.dataset.theme = theme;
  document
    .querySelector('meta[name="theme-color"]')
    ?.setAttribute(
      'content',
      (THEMES.find((option) => option.id === theme) ?? THEMES[0]).background
    );
  window.dispatchEvent(new Event(THEME_CHANGE_EVENT));
};

export const setTheme = (theme: ThemePreference) => {
  applyTheme(theme);
  try {
    localStorage.setItem(THEME_STORAGE_KEY, theme);
  } catch {
    // A blocked/full storage area must not prevent changing the current tab.
  }
};

export const subscribeTheme = (listener: () => void) => {
  window.addEventListener(THEME_CHANGE_EVENT, listener);
  return () => window.removeEventListener(THEME_CHANGE_EVENT, listener);
};

export const initializeTheme = () => {
  let saved: string | null = null;
  let storage: Storage | undefined;
  try {
    storage = window.localStorage;
    saved = storage.getItem(THEME_STORAGE_KEY);
  } catch {
    // Use the default when browser storage is unavailable.
  }
  applyTheme(normalizeTheme(saved));

  const onStorage = (event: StorageEvent) => {
    if (event.storageArea === storage && (event.key === THEME_STORAGE_KEY || event.key === null)) {
      applyTheme(normalizeTheme(event.newValue));
    }
  };
  const systemAppearance = window.matchMedia(SYSTEM_DARK_QUERY);
  const onSystemAppearanceChange = () => {
    if (getTheme() === 'system') applyTheme('system');
  };
  window.addEventListener('storage', onStorage);
  systemAppearance.addEventListener('change', onSystemAppearanceChange);
  return () => {
    window.removeEventListener('storage', onStorage);
    systemAppearance.removeEventListener('change', onSystemAppearanceChange);
  };
};
