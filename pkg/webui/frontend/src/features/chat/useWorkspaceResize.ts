import type React from 'react';
import { useCallback, useEffect, useLayoutEffect, useRef, useState } from 'react';
import type { useChatLayout } from './useChatLayout';

const WORKSPACE_WIDTH_STORAGE_KEY = 'kodelet.chat.workspace.width';
const MIN_WORKSPACE_WIDTH = 360;
const MIN_CHAT_WIDTH = 400;

const readStoredWorkspaceWidth = (): number | null => {
  if (typeof window === 'undefined') return null;
  const width = Number(window.localStorage.getItem(WORKSPACE_WIDTH_STORAGE_KEY));
  return Number.isFinite(width) && width > 0 ? width : null;
};

type WorkspaceLayout = Pick<
  ReturnType<typeof useChatLayout>,
  | 'workspaceToolsRef'
  | 'sidebarShellRef'
  | 'sidebarVisible'
  | 'workspacePanelOpen'
  | 'workspaceOverlayLayout'
>;

export const useWorkspaceResize = (
  {
    workspaceToolsRef,
    sidebarShellRef,
    sidebarVisible,
    workspacePanelOpen,
    workspaceOverlayLayout,
  }: WorkspaceLayout,
  workspaceToolsAvailable: boolean,
  higherPriorityDialogOpen: boolean
) => {
  const [workspaceWidth, setWorkspaceWidth] = useState(readStoredWorkspaceWidth);
  const [workspaceSize, setWorkspaceSize] = useState({ width: 0, min: 0, max: 0 });
  const [isResizingWorkspace, setIsResizingWorkspace] = useState(false);
  const workspaceResizerRef = useRef<HTMLHRElement | null>(null);
  const workspaceSizeRef = useRef(workspaceSize);
  const workspaceResizeStartRef = useRef<{
    pointerId: number;
    startX: number;
    startWidth: number;
  } | null>(null);

  useEffect(() => {
    if (workspaceWidth !== null) {
      window.localStorage.setItem(WORKSPACE_WIDTH_STORAGE_KEY, String(workspaceWidth));
    }
  }, [workspaceWidth]);

  const applyWorkspaceWidth = useCallback(
    (width: number) => {
      const size = workspaceSizeRef.current;
      const nextWidth = Math.round(Math.min(size.max, Math.max(size.min, width)));
      workspaceSizeRef.current = { ...size, width: nextWidth };
      workspaceToolsRef.current?.style.setProperty('--workspace-width', `${nextWidth}px`);
      workspaceResizerRef.current?.setAttribute('aria-valuenow', String(nextWidth));
      workspaceResizerRef.current?.setAttribute('aria-valuetext', `${nextWidth} pixels`);
      return nextWidth;
    },
    [workspaceToolsRef]
  );

  useEffect(() => {
    if (!isResizingWorkspace) return undefined;
    const start = workspaceResizeStartRef.current;
    const separator = workspaceResizerRef.current;
    if (
      !start ||
      !separator ||
      !workspacePanelOpen ||
      workspaceOverlayLayout ||
      higherPriorityDialogOpen
    ) {
      workspaceResizeStartRef.current = null;
      if (start && separator?.hasPointerCapture?.(start.pointerId)) {
        separator.releasePointerCapture(start.pointerId);
      }
      setIsResizingWorkspace(false);
      return undefined;
    }

    const previousUserSelect = document.body.style.userSelect;
    const previousCursor = document.body.style.cursor;
    document.body.style.userSelect = 'none';
    document.body.style.cursor = 'col-resize';

    const finish = (commit: boolean) => {
      if (workspaceResizeStartRef.current !== start) return;
      workspaceResizeStartRef.current = null;
      if (commit) setWorkspaceWidth(workspaceSizeRef.current.width);
      else applyWorkspaceWidth(start.startWidth);
      setWorkspaceSize(workspaceSizeRef.current);
      setIsResizingWorkspace(false);
    };
    const move = (event: PointerEvent) => {
      if (event.pointerId !== start.pointerId) return;
      applyWorkspaceWidth(start.startWidth + start.startX - event.clientX);
    };
    const end = (event: PointerEvent) => {
      if (event.pointerId === start.pointerId) finish(event.type === 'pointerup');
    };
    const cancel = () => finish(false);
    const keyDown = (event: KeyboardEvent) => {
      if (event.key !== 'Escape') return;
      event.preventDefault();
      cancel();
    };
    window.addEventListener('pointermove', move);
    window.addEventListener('pointerup', end);
    window.addEventListener('pointercancel', end);
    window.addEventListener('blur', cancel);
    window.addEventListener('keydown', keyDown);
    separator.addEventListener('lostpointercapture', cancel);
    return () => {
      window.removeEventListener('pointermove', move);
      window.removeEventListener('pointerup', end);
      window.removeEventListener('pointercancel', end);
      window.removeEventListener('blur', cancel);
      window.removeEventListener('keydown', keyDown);
      separator.removeEventListener('lostpointercapture', cancel);
      if (separator.hasPointerCapture?.(start.pointerId)) {
        separator.releasePointerCapture(start.pointerId);
      }
      document.body.style.userSelect = previousUserSelect;
      document.body.style.cursor = previousCursor;
      if (workspaceResizeStartRef.current === start) {
        workspaceResizeStartRef.current = null;
        applyWorkspaceWidth(start.startWidth);
      }
    };
  }, [
    applyWorkspaceWidth,
    higherPriorityDialogOpen,
    isResizingWorkspace,
    workspaceOverlayLayout,
    workspacePanelOpen,
  ]);

  useLayoutEffect(() => {
    const shell = workspaceToolsRef.current;
    const layout = shell?.parentElement;
    if (
      !shell ||
      !layout ||
      !workspaceToolsAvailable ||
      !workspacePanelOpen ||
      workspaceOverlayLayout
    ) {
      return undefined;
    }
    const sidebar = sidebarVisible
      ? sidebarShellRef.current
      : layout.querySelector('.sidebar-collapsed-rail');
    const measure = () => {
      const layoutWidth = layout.getBoundingClientRect().width;
      if (layoutWidth <= 0) return;
      const max = Math.max(
        1,
        Math.floor(layoutWidth - (sidebar?.getBoundingClientRect().width || 0) - MIN_CHAT_WIDTH)
      );
      const min = Math.min(MIN_WORKSPACE_WIDTH, max);
      const rem = Number.parseFloat(getComputedStyle(document.documentElement).fontSize) || 16;
      // Match the previous desktop default, unless chat needs more room.
      const defaultWidth =
        Math.min(Math.max(30 * rem, window.innerWidth * 0.38), 46 * rem, window.innerWidth * 0.48) +
        2.75 * rem;
      const width = Math.round(
        Math.min(
          max,
          Math.max(
            min,
            workspaceResizeStartRef.current
              ? workspaceSizeRef.current.width
              : (workspaceWidth ?? defaultWidth)
          )
        )
      );
      const size = { width, min, max };
      workspaceSizeRef.current = size;
      setWorkspaceSize((current) =>
        current.width === width && current.min === min && current.max === max ? current : size
      );
    };
    measure();
    const observer = new ResizeObserver(measure);
    observer.observe(layout);
    if (sidebar) observer.observe(sidebar);
    window.addEventListener('resize', measure);
    return () => {
      observer.disconnect();
      window.removeEventListener('resize', measure);
    };
  }, [
    sidebarShellRef,
    sidebarVisible,
    workspaceOverlayLayout,
    workspacePanelOpen,
    workspaceToolsAvailable,
    workspaceToolsRef,
    workspaceWidth,
  ]);

  const handleWorkspaceResizeStart = (event: React.PointerEvent<HTMLHRElement>) => {
    if (event.button !== 0 || workspaceResizeStartRef.current) return;
    event.preventDefault();
    event.currentTarget.focus();
    event.currentTarget.setPointerCapture?.(event.pointerId);
    workspaceResizeStartRef.current = {
      pointerId: event.pointerId,
      startX: event.clientX,
      startWidth: workspaceSizeRef.current.width,
    };
    setIsResizingWorkspace(true);
  };

  const handleWorkspaceResizeKeyDown = (event: React.KeyboardEvent<HTMLHRElement>) => {
    if (workspaceResizeStartRef.current) return;
    const size = workspaceSizeRef.current;
    let width: number;
    switch (event.key) {
      case 'ArrowLeft':
        width = size.width + 10;
        break;
      case 'ArrowRight':
        width = size.width - 10;
        break;
      case 'Home':
        width = size.min;
        break;
      case 'End':
        width = size.max;
        break;
      default:
        return;
    }
    event.preventDefault();
    setWorkspaceWidth(applyWorkspaceWidth(width));
    setWorkspaceSize(workspaceSizeRef.current);
  };

  return {
    workspaceSize,
    isResizingWorkspace,
    workspaceResizerRef,
    handleWorkspaceResizeStart,
    handleWorkspaceResizeKeyDown,
  };
};
