import { Check, Monitor, Palette } from 'lucide-react';
import { useEffect, useId, useRef, useState, useSyncExternalStore } from 'react';
import { getTheme, setTheme, subscribeTheme, THEME_OPTIONS } from '../theme';

export default function ThemePicker() {
  const theme = useSyncExternalStore(subscribeTheme, getTheme);
  const [open, setOpen] = useState(false);
  const rootRef = useRef<HTMLDivElement>(null);
  const buttonRef = useRef<HTMLButtonElement>(null);
  const menuRef = useRef<HTMLDivElement>(null);
  const menuId = useId();

  useEffect(() => {
    if (!open) return;
    menuRef.current?.querySelector<HTMLButtonElement>('[aria-checked="true"]')?.focus();

    const onPointerDown = (event: PointerEvent) => {
      if (!rootRef.current?.contains(event.target as Node)) setOpen(false);
    };
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        event.preventDefault();
        setOpen(false);
        buttonRef.current?.focus();
      }
    };
    document.addEventListener('pointerdown', onPointerDown);
    document.addEventListener('keydown', onKeyDown);
    return () => {
      document.removeEventListener('pointerdown', onPointerDown);
      document.removeEventListener('keydown', onKeyDown);
    };
  }, [open]);

  return (
    <div className="theme-picker" ref={rootRef}>
      <button
        aria-controls={open ? menuId : undefined}
        aria-expanded={open}
        aria-haspopup="menu"
        aria-label="Choose theme"
        className="sidebar-toggle-button"
        onClick={() => setOpen(!open)}
        ref={buttonRef}
        title={`Theme: ${THEME_OPTIONS.find((option) => option.id === theme)?.label}`}
        type="button"
      >
        <Palette aria-hidden="true" className="h-4 w-4" strokeWidth={1.9} />
      </button>
      {open && (
        <div
          aria-label="Color theme"
          className="theme-picker-menu"
          id={menuId}
          ref={menuRef}
          role="menu"
          onBlur={(event) => {
            if (!rootRef.current?.contains(event.relatedTarget as Node | null)) setOpen(false);
          }}
          onKeyDown={(event) => {
            const items = Array.from(
              menuRef.current?.querySelectorAll<HTMLButtonElement>('[role="menuitemradio"]') ?? []
            );
            const index = items.indexOf(document.activeElement as HTMLButtonElement);
            let next: number;
            switch (event.key) {
              case 'ArrowDown':
                next = (index + 1) % items.length;
                break;
              case 'ArrowUp':
                next = (index - 1 + items.length) % items.length;
                break;
              case 'Home':
                next = 0;
                break;
              case 'End':
                next = items.length - 1;
                break;
              default:
                return;
            }
            event.preventDefault();
            items[next]?.focus();
          }}
        >
          <div className="theme-picker-heading" role="presentation">
            Appearance
          </div>
          {THEME_OPTIONS.map((option) => (
            <button
              aria-checked={theme === option.id}
              className="theme-picker-option"
              key={option.id}
              onClick={() => {
                setTheme(option.id);
                setOpen(false);
                buttonRef.current?.focus();
              }}
              role="menuitemradio"
              tabIndex={-1}
              type="button"
            >
              {option.id === 'system' ? (
                <span aria-hidden="true" className="flex w-6 shrink-0 justify-center">
                  <Monitor className="h-4 w-4" strokeWidth={1.9} />
                </span>
              ) : (
                <span aria-hidden="true" className="theme-picker-swatch">
                  <span style={{ background: option.background }} />
                  <span style={{ background: option.surface }} />
                  <span style={{ background: option.foreground }} />
                </span>
              )}
              <span>{option.label}</span>
              {theme === option.id && <Check aria-hidden="true" className="h-4 w-4" />}
            </button>
          ))}
        </div>
      )}
    </div>
  );
}
