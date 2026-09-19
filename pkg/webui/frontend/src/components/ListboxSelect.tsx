import { Check, ChevronDown } from 'lucide-react';
import React from 'react';
import { cn } from '../utils';

export interface ListboxOption {
  value: string;
  label: string;
  disabled?: boolean;
}

export interface ListboxSelectClassNames {
  shell?: string;
  trigger?: string;
  chevron?: string;
  chevronIcon?: string;
  menu?: string;
  option?: string;
}

export interface ListboxSelectProps {
  /** Accessible name for the trigger and listbox. */
  label: string;
  id?: string;
  testId?: string;
  value: string;
  options: ListboxOption[];
  placeholder?: string;
  disabled?: boolean;
  busy?: boolean;
  /** Selector for the scrolling container that bounds the menu; defaults to the viewport. */
  boundarySelector?: string;
  classNames?: ListboxSelectClassNames;
  /** Custom trigger content; defaults to the selected option label. */
  renderValue?: (selected: ListboxOption | undefined) => React.ReactNode;
  onChange: (value: string) => void;
  onOpen?: () => void;
}

const defaultClassNames: Required<ListboxSelectClassNames> = {
  shell: 'new-chat-select-shell',
  trigger: 'new-chat-field-control new-chat-field-control-select new-chat-select-trigger',
  chevron: 'new-chat-select-chevron',
  chevronIcon: 'h-4 w-4',
  menu: 'new-chat-select-menu',
  option: 'new-chat-select-option',
};

const ListboxSelect = ({
  label,
  id: explicitId,
  testId,
  value,
  options,
  placeholder,
  disabled,
  busy,
  boundarySelector,
  classNames,
  renderValue,
  onChange,
  onOpen,
}: ListboxSelectProps) => {
  const generatedId = React.useId();
  const id = explicitId ?? generatedId;
  const classes = { ...defaultClassNames, ...classNames };
  const rootRef = React.useRef<HTMLDivElement>(null);
  const buttonRef = React.useRef<HTMLButtonElement>(null);
  const searchRef = React.useRef({ text: '', time: 0 });
  const [expanded, setExpanded] = React.useState(false);
  const [activeValue, setActiveValue] = React.useState(value);
  const [placement, setPlacement] = React.useState({
    above: false,
    alignEnd: false,
    maxHeight: 240,
  });
  const open = expanded && !disabled;
  const enabledOptions = options.filter((option) => !option.disabled);
  const selectedOption = options.find((option) => option.value === value);
  const activeIndex = options.findIndex(
    (option) => option.value === activeValue && !option.disabled
  );

  const openMenu = () => {
    // Button clicks do not focus the trigger in every browser.
    buttonRef.current?.focus();
    searchRef.current = { text: '', time: 0 };
    setActiveValue(
      enabledOptions.find((option) => option.value === value)?.value ??
        enabledOptions[0]?.value ??
        ''
    );
    setExpanded(true);
    onOpen?.();
  };

  const chooseOption = (nextValue: string) => {
    if (enabledOptions.some((option) => option.value === nextValue) && nextValue !== value) {
      onChange(nextValue);
    }
    setExpanded(false);
  };

  React.useEffect(() => {
    if (disabled) setExpanded(false);
  }, [disabled]);

  React.useEffect(() => {
    // Options may arrive after the menu opens (for example while loading).
    if (!open || activeIndex >= 0) return;
    const fallback =
      enabledOptions.find((option) => option.value === value)?.value ?? enabledOptions[0]?.value;
    if (fallback !== undefined) setActiveValue(fallback);
  }, [open, activeIndex, enabledOptions, value]);

  React.useLayoutEffect(() => {
    if (!open) return;
    // Keep the menu inside the bounding scroll container, including on small screens.
    const positionMenu = () => {
      const button = buttonRef.current?.getBoundingClientRect();
      if (!button) return;
      const panel = boundarySelector
        ? rootRef.current?.closest(boundarySelector)?.getBoundingClientRect()
        : undefined;
      const panelBottom = panel ? Math.min(panel.bottom, window.innerHeight) : window.innerHeight;
      const panelTop = panel ? Math.max(panel.top, 0) : 0;
      const below = panelBottom - button.bottom - 8;
      const above = button.top - panelTop - 8;
      const placeAbove = below < 160 && above > below;
      const panelLeft = panel ? Math.max(panel.left, 0) : 0;
      const panelRight = panel ? Math.min(panel.right, window.innerWidth) : window.innerWidth;
      setPlacement({
        above: placeAbove,
        // Grow the menu toward the side with more room when it is wider than the trigger.
        alignEnd: button.right - panelLeft > panelRight - button.left,
        maxHeight: Math.max(0, Math.min(240, placeAbove ? above : below)),
      });
    };
    const onPointerDown = (event: PointerEvent) => {
      if (!rootRef.current?.contains(event.target as Node)) setExpanded(false);
    };
    positionMenu();
    window.addEventListener('resize', positionMenu);
    window.addEventListener('scroll', positionMenu, true);
    document.addEventListener('pointerdown', onPointerDown);
    return () => {
      window.removeEventListener('resize', positionMenu);
      window.removeEventListener('scroll', positionMenu, true);
      document.removeEventListener('pointerdown', onPointerDown);
    };
  }, [open, boundarySelector]);

  React.useEffect(() => {
    if (open) {
      document
        .getElementById(`${id}-option-${activeIndex}`)
        ?.scrollIntoView?.({ block: 'nearest' });
    }
  }, [open, id, activeIndex]);

  const onKeyDown = (event: React.KeyboardEvent<HTMLButtonElement>) => {
    if (event.key === 'Escape' && open) {
      event.preventDefault();
      event.stopPropagation();
      setExpanded(false);
      return;
    }
    if (event.key === 'Tab') {
      if (open) chooseOption(activeValue);
      return;
    }
    if (event.key === 'Enter' || event.key === ' ') {
      event.preventDefault();
      if (open) chooseOption(activeValue);
      else openMenu();
      return;
    }
    const index = enabledOptions.findIndex((option) => option.value === activeValue);
    if (['ArrowDown', 'ArrowUp', 'Home', 'End', 'PageDown', 'PageUp'].includes(event.key)) {
      event.preventDefault();
      if (open && event.altKey && event.key === 'ArrowUp') {
        chooseOption(activeValue);
        return;
      }
      if (!open) openMenu();
      let nextIndex = index;
      if (event.key === 'Home') nextIndex = 0;
      else if (event.key === 'End') nextIndex = enabledOptions.length - 1;
      else if (open) {
        const step = event.key.startsWith('Page') ? 10 : 1;
        nextIndex += event.key.endsWith('Down') ? step : -step;
      } else return;
      const next = enabledOptions[Math.max(0, Math.min(enabledOptions.length - 1, nextIndex))];
      if (next) setActiveValue(next.value);
      return;
    }
    if (event.key.length !== 1 || event.ctrlKey || event.metaKey || event.altKey) return;
    event.preventDefault();
    if (!open) openMenu();
    const now = Date.now();
    const text =
      (now - searchRef.current.time < 500 ? searchRef.current.text : '') + event.key.toLowerCase();
    searchRef.current = { text, time: now };
    const repeated = [...text].every((character) => character === text[0]);
    const prefix = repeated ? text[0] : text;
    const start = open ? Math.max(0, index + (repeated ? 1 : 0)) : 0;
    const ordered = [...enabledOptions.slice(start), ...enabledOptions.slice(0, start)];
    const match = ordered.find((option) => option.label.toLowerCase().startsWith(prefix));
    if (match) setActiveValue(match.value);
  };

  return (
    <div className={cn(classes.shell, open && 'is-open')} ref={rootRef}>
      <button
        aria-activedescendant={open && activeIndex >= 0 ? `${id}-option-${activeIndex}` : undefined}
        aria-busy={busy}
        aria-controls={open ? `${id}-listbox` : undefined}
        aria-expanded={open}
        aria-haspopup="listbox"
        aria-label={label}
        className={classes.trigger}
        data-testid={testId}
        disabled={disabled}
        id={id}
        onBlur={(event) => {
          if (!rootRef.current?.contains(event.relatedTarget as Node | null)) setExpanded(false);
        }}
        onClick={() => {
          if (open) setExpanded(false);
          else openMenu();
        }}
        onKeyDown={onKeyDown}
        ref={buttonRef}
        role="combobox"
        type="button"
      >
        {renderValue
          ? renderValue(selectedOption)
          : (selectedOption?.label ?? placeholder ?? options[0]?.label)}
        <span className={classes.chevron} aria-hidden="true">
          <ChevronDown className={classes.chevronIcon} strokeWidth={1.8} />
        </span>
      </button>
      {open && (
        <div
          aria-label={label}
          className={cn(
            classes.menu,
            placement.above && 'is-above',
            placement.alignEnd && 'is-align-end'
          )}
          id={`${id}-listbox`}
          role="listbox"
          style={{ maxHeight: placement.maxHeight }}
        >
          {options.map((option, index) => (
            <button
              aria-selected={option.value === value}
              className={cn(classes.option, option.value === activeValue && 'is-active')}
              data-value={option.value}
              disabled={option.disabled}
              id={`${id}-option-${index}`}
              key={option.value}
              onClick={() => {
                chooseOption(option.value);
                buttonRef.current?.focus();
              }}
              onMouseDown={(event) => event.preventDefault()}
              role="option"
              tabIndex={-1}
              type="button"
            >
              <span>{option.label}</span>
              {option.value === value && <Check aria-hidden="true" className="h-4 w-4 shrink-0" />}
            </button>
          ))}
        </div>
      )}
    </div>
  );
};

export default ListboxSelect;
