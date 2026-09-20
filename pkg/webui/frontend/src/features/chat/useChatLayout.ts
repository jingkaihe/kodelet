import type React from 'react';
import { useCallback, useEffect, useRef, useState } from 'react';

const DEFAULT_SIDEBAR_WIDTH = 320;
export const MIN_SIDEBAR_WIDTH = 260;
export const MAX_SIDEBAR_WIDTH = 520;
const SIDEBAR_WIDTH_STORAGE_KEY = 'kodelet.chat.sidebar.width';
const SIDEBAR_VISIBLE_STORAGE_KEY = 'kodelet.chat.sidebar.visible';
const MOBILE_LAYOUT_MEDIA_QUERY = '(max-width: 1023px)';
const WORKSPACE_OVERLAY_MEDIA_QUERY = '(max-width: 1180px)';
const OVERLAY_FOCUSABLE_SELECTOR = [
  'button:not([disabled])',
  '[href]',
  'input:not([disabled])',
  'select:not([disabled])',
  'textarea:not([disabled])',
  "[contenteditable='true']",
  "[tabindex]:not([tabindex='-1'])",
].join(',');
type WorkspacePanelView = 'diff' | 'terminal' | 'browser';

const clampSidebarWidth = (width: number): number =>
  Math.min(MAX_SIDEBAR_WIDTH, Math.max(MIN_SIDEBAR_WIDTH, width));

const readStoredSidebarVisible = (): boolean => {
  if (typeof window === 'undefined') {
    return true;
  }

  return window.localStorage.getItem(SIDEBAR_VISIBLE_STORAGE_KEY) !== 'false';
};

const isMobileLayoutViewport = (): boolean =>
  typeof window !== 'undefined' &&
  typeof window.matchMedia === 'function' &&
  window.matchMedia(MOBILE_LAYOUT_MEDIA_QUERY).matches;

const useMediaQuery = (query: string): boolean => {
  const [matches, setMatches] = useState(
    () =>
      typeof window !== 'undefined' &&
      typeof window.matchMedia === 'function' &&
      window.matchMedia(query).matches
  );

  useEffect(() => {
    if (typeof window.matchMedia !== 'function') {
      return undefined;
    }

    const mediaQuery = window.matchMedia(query);
    const handleChange = (event: MediaQueryListEvent) => {
      setMatches(event.matches);
    };

    setMatches(mediaQuery.matches);
    mediaQuery.addEventListener('change', handleChange);
    return () => {
      mediaQuery.removeEventListener('change', handleChange);
    };
  }, [query]);

  return matches;
};

const readInitialSidebarVisible = (): boolean =>
  isMobileLayoutViewport() ? false : readStoredSidebarVisible();

const readStoredSidebarWidth = (): number => {
  if (typeof window === 'undefined') {
    return DEFAULT_SIDEBAR_WIDTH;
  }

  const storedWidth = window.localStorage.getItem(SIDEBAR_WIDTH_STORAGE_KEY);
  if (storedWidth === null) {
    return DEFAULT_SIDEBAR_WIDTH;
  }

  const parsedWidth = Number(storedWidth);
  return Number.isFinite(parsedWidth) ? clampSidebarWidth(parsedWidth) : DEFAULT_SIDEBAR_WIDTH;
};

export const useChatLayout = (higherPriorityDialogOpen: boolean) => {
  const [workspacePanelView, setWorkspacePanelView] = useState<WorkspacePanelView | null>(null);
  const mobileLayout = useMediaQuery(MOBILE_LAYOUT_MEDIA_QUERY);
  const workspaceOverlayLayout = useMediaQuery(WORKSPACE_OVERLAY_MEDIA_QUERY);
  const [sidebarVisible, setSidebarVisible] = useState(readInitialSidebarVisible);
  const [sidebarWidth, setSidebarWidth] = useState(readStoredSidebarWidth);
  const [isResizingSidebar, setIsResizingSidebar] = useState(false);
  const sidebarResizeStartRef = useRef<{
    startX: number;
    startWidth: number;
  } | null>(null);
  const desktopSidebarVisibleRef = useRef(readStoredSidebarVisible());
  const restoringDesktopSidebarRef = useRef(false);
  const sidebarWidthRef = useRef(sidebarWidth);
  const sidebarShellRef = useRef<HTMLElement | null>(null);
  const sidebarReturnFocusRef = useRef<HTMLElement | null>(null);
  const workspaceToolsRef = useRef<HTMLElement | null>(null);
  const workspacePanelOpen = workspacePanelView !== null;
  const sidebarOverlayOpen = mobileLayout && sidebarVisible;
  const workspaceOverlayOpen = workspaceOverlayLayout && workspacePanelOpen;

  const closeMobileSidebar = useCallback(() => {
    if (mobileLayout) {
      setSidebarVisible(false);
    }
  }, [mobileLayout]);

  useEffect(() => {
    if (mobileLayout) {
      setSidebarVisible(false);
      return;
    }

    restoringDesktopSidebarRef.current = true;
    setSidebarVisible(desktopSidebarVisibleRef.current);
  }, [mobileLayout]);

  useEffect(() => {
    if (mobileLayout) {
      return;
    }

    if (restoringDesktopSidebarRef.current) {
      if (sidebarVisible === desktopSidebarVisibleRef.current) {
        restoringDesktopSidebarRef.current = false;
      }
      return;
    }

    desktopSidebarVisibleRef.current = sidebarVisible;
    window.localStorage.setItem(SIDEBAR_VISIBLE_STORAGE_KEY, String(sidebarVisible));
  }, [mobileLayout, sidebarVisible]);

  useEffect(() => {
    if (higherPriorityDialogOpen) {
      return undefined;
    }

    const overlay = workspaceOverlayOpen
      ? workspaceToolsRef.current
      : sidebarOverlayOpen
        ? sidebarShellRef.current
        : null;
    if (!overlay) {
      return undefined;
    }

    const previousFocus =
      document.activeElement instanceof HTMLElement ? document.activeElement : null;
    const returnFocus = sidebarOverlayOpen ? sidebarReturnFocusRef.current : previousFocus;
    const focusOverlay = window.setTimeout(() => {
      const initialFocus = overlay.querySelector<HTMLElement>(
        workspaceOverlayOpen
          ? '[data-testid="workspace-tools-toggle"]'
          : '[data-testid="sidebar-hide-button"]'
      );
      if (initialFocus && !initialFocus.hasAttribute('disabled')) {
        initialFocus.focus();
        return;
      }
      overlay.focus();
    }, 0);

    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape' && sidebarOverlayOpen) {
        // Let a nested menu handle Escape before dismissing the entire drawer.
        if (event.target instanceof Element && event.target.closest('[role="menu"]')) {
          return;
        }
        event.preventDefault();
        setSidebarVisible(false);
        return;
      }
      if (event.key !== 'Tab') {
        return;
      }

      const focusableElements = Array.from(
        overlay.querySelectorAll<HTMLElement>(OVERLAY_FOCUSABLE_SELECTOR)
      ).filter((element) => !element.hasAttribute('disabled') && !element.closest('[inert]'));
      if (focusableElements.length === 0) {
        event.preventDefault();
        overlay.focus();
        return;
      }

      const firstElement = focusableElements[0];
      const lastElement = focusableElements[focusableElements.length - 1];
      const activeElement = document.activeElement;
      if (!activeElement || !overlay.contains(activeElement)) {
        event.preventDefault();
        (event.shiftKey ? lastElement : firstElement).focus();
        return;
      }
      if (event.shiftKey && activeElement === firstElement) {
        event.preventDefault();
        lastElement.focus();
        return;
      }
      if (!event.shiftKey && activeElement === lastElement) {
        event.preventDefault();
        firstElement.focus();
      }
    };

    window.addEventListener('keydown', handleKeyDown, true);
    return () => {
      window.clearTimeout(focusOverlay);
      window.removeEventListener('keydown', handleKeyDown, true);
      window.setTimeout(() => {
        if (returnFocus?.isConnected) {
          returnFocus.focus();
          return;
        }
        if (sidebarOverlayOpen) {
          document
            .querySelector<HTMLElement>('[data-testid="sidebar-attached-toggle-mobile"]')
            ?.focus();
        }
      }, 0);
    };
  }, [higherPriorityDialogOpen, sidebarOverlayOpen, workspaceOverlayOpen]);

  useEffect(() => {
    if (workspacePanelView !== 'terminal' || higherPriorityDialogOpen || sidebarOverlayOpen) {
      return undefined;
    }

    const workspace = workspaceToolsRef.current;
    if (!workspace) {
      return undefined;
    }

    const handleTerminalExitKey = (event: KeyboardEvent) => {
      if (event.key !== 'F6') {
        return;
      }

      const terminalHost = workspace.querySelector<HTMLElement>('.workspace-terminal-host');
      const activeElement = document.activeElement;
      if (
        !terminalHost ||
        !(activeElement instanceof HTMLElement) ||
        !terminalHost.contains(activeElement)
      ) {
        return;
      }

      const target = event.shiftKey
        ? workspace.querySelector<HTMLElement>('[data-testid="workspace-tools-diff-tab"]') ||
          workspace.querySelector<HTMLElement>('[data-testid="workspace-tools-terminal-tab"]')
        : workspace.querySelector<HTMLElement>('[data-testid="workspace-tools-toggle"]');
      if (!target) {
        return;
      }

      event.preventDefault();
      event.stopPropagation();
      target.focus();
    };

    window.addEventListener('keydown', handleTerminalExitKey, true);
    return () => {
      window.removeEventListener('keydown', handleTerminalExitKey, true);
    };
  }, [higherPriorityDialogOpen, sidebarOverlayOpen, workspacePanelView]);

  useEffect(() => {
    window.localStorage.setItem(SIDEBAR_WIDTH_STORAGE_KEY, String(sidebarWidth));
  }, [sidebarWidth]);

  useEffect(() => {
    if (!isResizingSidebar) {
      return undefined;
    }

    const previousUserSelect = document.body.style.userSelect;
    const previousCursor = document.body.style.cursor;
    document.body.style.userSelect = 'none';
    document.body.style.cursor = 'col-resize';

    const handleMouseMove = (event: MouseEvent) => {
      const resizeStart = sidebarResizeStartRef.current;
      if (!resizeStart) {
        return;
      }

      const nextWidth = clampSidebarWidth(
        resizeStart.startWidth + (event.clientX - resizeStart.startX)
      );
      sidebarWidthRef.current = nextWidth;
      sidebarShellRef.current?.style.setProperty('--sidebar-width', `${nextWidth}px`);
    };

    const stopResizing = () => {
      sidebarResizeStartRef.current = null;
      setSidebarWidth(sidebarWidthRef.current);
      setIsResizingSidebar(false);
    };

    window.addEventListener('mousemove', handleMouseMove);
    window.addEventListener('mouseup', stopResizing);

    return () => {
      document.body.style.userSelect = previousUserSelect;
      document.body.style.cursor = previousCursor;
      window.removeEventListener('mousemove', handleMouseMove);
      window.removeEventListener('mouseup', stopResizing);
    };
  }, [isResizingSidebar]);

  const handleSidebarToggle = () => {
    if (workspaceOverlayLayout && !sidebarVisible) {
      setWorkspacePanelView(null);
    }
    if (!sidebarVisible) {
      const activeElement =
        document.activeElement instanceof HTMLElement && document.activeElement !== document.body
          ? document.activeElement
          : null;
      sidebarReturnFocusRef.current =
        activeElement ||
        document.querySelector<HTMLElement>('[data-testid="sidebar-attached-toggle-mobile"]') ||
        document.querySelector<HTMLElement>('[data-testid="sidebar-attached-toggle"]');
    }
    setSidebarVisible(!sidebarVisible);
  };

  const handleSidebarResizeStart = (event: React.MouseEvent<HTMLElement>) => {
    event.preventDefault();
    sidebarWidthRef.current = sidebarWidth;
    sidebarResizeStartRef.current = {
      startX: event.clientX,
      startWidth: sidebarWidth,
    };
    setIsResizingSidebar(true);
  };

  const handleSidebarResizeKeyDown = (event: React.KeyboardEvent<HTMLHRElement>) => {
    let nextWidth: number;
    switch (event.key) {
      case 'ArrowLeft':
        nextWidth = sidebarWidth - 10;
        break;
      case 'ArrowRight':
        nextWidth = sidebarWidth + 10;
        break;
      case 'Home':
        nextWidth = MIN_SIDEBAR_WIDTH;
        break;
      case 'End':
        nextWidth = MAX_SIDEBAR_WIDTH;
        break;
      default:
        return;
    }

    event.preventDefault();
    sidebarWidthRef.current = clampSidebarWidth(nextWidth);
    setSidebarWidth(sidebarWidthRef.current);
  };

  return {
    mobileLayout,
    workspaceOverlayLayout,
    sidebarVisible,
    setSidebarVisible,
    sidebarWidth,
    isResizingSidebar,
    sidebarShellRef,
    workspaceToolsRef,
    workspacePanelView,
    setWorkspacePanelView,
    workspacePanelOpen,
    sidebarOverlayOpen,
    workspaceOverlayOpen,
    closeMobileSidebar,
    handleSidebarToggle,
    handleSidebarResizeStart,
    handleSidebarResizeKeyDown,
  };
};
