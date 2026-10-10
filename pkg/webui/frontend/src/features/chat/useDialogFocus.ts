import { useEffect, useEffectEvent, useRef } from 'react';

// Focus management for informational dialogs whose interactive elements are buttons.
export const useDialogFocus = (onClose: () => void, returnFocusSelector: string) => {
  const dialogRef = useRef<HTMLDivElement | null>(null);
  const closeButtonRef = useRef<HTMLButtonElement | null>(null);
  const close = useEffectEvent(onClose);

  useEffect(() => {
    const previousFocus =
      document.activeElement instanceof HTMLElement && document.activeElement !== document.body
        ? document.activeElement
        : null;
    const focusTimer = window.setTimeout(() => closeButtonRef.current?.focus(), 0);
    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        event.preventDefault();
        event.stopPropagation();
        close();
        return;
      }
      if (event.key !== 'Tab') return;

      const dialog = dialogRef.current;
      const buttons = dialog?.querySelectorAll<HTMLButtonElement>('button:not([disabled])');
      if (!dialog || !buttons?.length) return;
      const first = buttons[0];
      const last = buttons[buttons.length - 1];
      if (!dialog.contains(document.activeElement)) {
        event.preventDefault();
        (event.shiftKey ? last : first).focus();
      } else if (event.shiftKey && document.activeElement === first) {
        event.preventDefault();
        last.focus();
      } else if (!event.shiftKey && document.activeElement === last) {
        event.preventDefault();
        first.focus();
      }
    };
    document.addEventListener('keydown', handleKeyDown, true);
    return () => {
      window.clearTimeout(focusTimer);
      document.removeEventListener('keydown', handleKeyDown, true);
      // A menu trigger may unmount when the dialog opens. Restore focus after inert clears.
      window.setTimeout(() => {
        const target = [
          previousFocus,
          ...document.querySelectorAll<HTMLElement>(returnFocusSelector),
        ].find((element) => element?.isConnected && !element.closest('[inert]'));
        target?.focus();
      }, 0);
    };
  }, [returnFocusSelector]);

  return { dialogRef, closeButtonRef };
};
