import type React from 'react';
import { createRef, useEffect, useState } from 'react';

const AUTO_SCROLL_BOTTOM_THRESHOLD = 80;
// Native scrolling follows its input within a frame or two. Input that has not moved
// the transcript by then went elsewhere, so stop holding the transcript for it.
const SCROLL_INTENT_GRACE_MS = 200;
const KEY_SCROLL_EXCLUDED_TARGETS =
  'input, textarea, select, button, summary, [contenteditable]:not([contenteditable="false"])';
const UPWARD_SCROLL_KEYS = new Set(['ArrowUp', 'PageUp', 'Home']);
const DOWNWARD_SCROLL_KEYS = new Set(['ArrowDown', 'PageDown', 'End']);
const SCROLLABLE_OVERFLOW = new Set(['auto', 'scroll', 'overlay']);

type ScrollPosition = { top: number; height: number; viewport: number };
type ScrollIntent = ScrollPosition & { direction: number };

const getScrollPosition = (element: HTMLElement): ScrollPosition => ({
  top: element.scrollTop,
  height: element.scrollHeight,
  viewport: element.clientHeight,
});

const isNearBottom = ({ top, height, viewport }: ScrollPosition): boolean =>
  height - top - viewport <= AUTO_SCROLL_BOTTOM_THRESHOLD;

const getKeyScrollDirection = (event: KeyboardEvent): number => {
  if (
    event.defaultPrevented ||
    (event.target instanceof Element && event.target.closest(KEY_SCROLL_EXCLUDED_TARGETS))
  ) {
    return 0;
  }
  if (event.key === ' ') return event.shiftKey ? -1 : 1;
  if (UPWARD_SCROLL_KEYS.has(event.key)) return -1;
  return DOWNWARD_SCROLL_KEYS.has(event.key) ? 1 : 0;
};

// A scroller inside the transcript, such as live thinking or tool output, takes
// vertical input while it can still move that way.
const isConsumedByNestedScroller = (
  transcript: HTMLElement,
  target: EventTarget | null,
  direction: number
): boolean => {
  if (!(target instanceof Element) || !transcript.contains(target)) return false;
  for (let node: Element | null = target; node && node !== transcript; node = node.parentElement) {
    if (
      node.scrollHeight > node.clientHeight &&
      SCROLLABLE_OVERFLOW.has(getComputedStyle(node).overflowY) &&
      (direction < 0
        ? node.scrollTop > 0
        : Math.ceil(node.scrollTop) + node.clientHeight < node.scrollHeight)
    ) {
      return true;
    }
  }
  return false;
};

const createTranscriptAutoScroll = () => {
  const scrollRef = createRef<HTMLDivElement>();
  let following = true;
  let position: ScrollPosition | null = null;
  // Reader input that has not moved the transcript yet, with the position it started from.
  let intent: ScrollIntent | null = null;
  let intentTimer: ReturnType<typeof setTimeout> | undefined;
  let touch: { x: number; y: number } | null = null;
  let releasePointer: ((resume?: boolean) => void) | null = null;

  const clearIntent = () => {
    clearTimeout(intentTimer);
    intentTimer = undefined;
    intent = null;
  };

  // Attributes transcript movement to pending input. Returns true while the input
  // explains the current position, so callers must not reinterpret it.
  const resolveIntent = (element: HTMLElement): boolean => {
    if (!intent) return false;
    const movement = (element.scrollTop - intent.top) * intent.direction;
    if (movement < 0) {
      // Something else, such as reflow clamping, moved the transcript the other way.
      clearIntent();
      return false;
    }
    if (movement > 0) {
      // Input can move the viewport before its scroll event runs. Compare with the
      // pre-input bottom, not content appended by an intervening stream commit.
      following = intent.direction > 0 && isNearBottom({ ...intent, top: element.scrollTop });
      clearIntent();
    }
    return true;
  };

  const followTranscript = () => {
    const element = scrollRef.current;
    if (!element) return;
    resolveIntent(element);
    // Upward input or a scrollbar drag may not have moved the transcript yet; do not undo it.
    if (following && !releasePointer && !(intent && intent.direction < 0)) {
      element.scrollTop = Math.max(0, element.scrollHeight - element.clientHeight);
      // Remaining downward input did not move the transcript; do not attribute our scroll to it.
      clearIntent();
    }
    position = getScrollPosition(element);
  };

  const recordIntent = (direction: number, target: EventTarget | null) => {
    const element = scrollRef.current;
    if (!element || !direction || isConsumedByNestedScroller(element, target, direction)) return;
    const start = position ?? getScrollPosition(element);
    // Upward input cannot move a transcript that is already at the top, e.g. a short one.
    if (direction < 0 && start.top <= 0 && element.scrollTop <= 0) return;
    // Keep the first position across input events coalesced before the scroll event.
    if (!intent || intent.direction * direction < 0) intent = { ...start, direction };
    clearTimeout(intentTimer);
    intentTimer = setTimeout(() => {
      const current = scrollRef.current;
      if (current) resolveIntent(current);
      // Input that never moved the transcript went elsewhere; catch up on held content.
      clearIntent();
      followTranscript();
    }, SCROLL_INTENT_GRACE_MS);
  };

  // Hold following while the reader drags the transcript's own scrollbar, so a
  // stream commit cannot snap back before the drag's scroll event arrives.
  const holdForPointer = () => {
    releasePointer?.(false);
    const listeners = new AbortController();
    const release = (resume = true) => {
      listeners.abort();
      releasePointer = null;
      if (resume) followTranscript();
    };
    const options = { capture: true, signal: listeners.signal };
    window.addEventListener('pointerup', () => release(), options);
    window.addEventListener('pointercancel', () => release(), options);
    window.addEventListener('blur', () => release(), options);
    // Browsers may not report pointerup after a native scrollbar drag; the next hover does.
    window.addEventListener(
      'pointermove',
      (event) => {
        if (!event.buttons) release();
      },
      options
    );
    releasePointer = release;
  };

  const connect = () => {
    const handleKeyDown = (event: KeyboardEvent) => {
      const element = scrollRef.current;
      const { target } = event;
      // Scroll keys move the transcript when focus is inside it or on nothing in particular.
      if (
        element &&
        target instanceof Node &&
        (element.contains(target) ||
          target === document.body ||
          target === document.documentElement)
      ) {
        recordIntent(getKeyScrollDirection(event), target);
      }
    };
    window.addEventListener('keydown', handleKeyDown);
    return () => {
      window.removeEventListener('keydown', handleKeyDown);
      releasePointer?.(false);
      clearIntent();
    };
  };

  const resetTranscriptScroll = () => {
    releasePointer?.(false);
    clearIntent();
    following = true;
    position = null;
    touch = null;
  };

  const transcriptScrollProps = {
    ref: scrollRef,
    onScroll: (event: React.UIEvent<HTMLDivElement>) => {
      const element = event.currentTarget;
      const next = getScrollPosition(element);
      const previous = position;
      // Resizing/reflow and our own bottom adjustment are not the reader scrolling away.
      if (
        !resolveIntent(element) &&
        (!previous ||
          (next.height === previous.height &&
            next.viewport === previous.viewport &&
            next.top !== previous.top))
      ) {
        following = isNearBottom(next);
      }
      position = next;
    },
    onWheel: (event: React.WheelEvent<HTMLDivElement>) => {
      // Ignore pinch zoom, and the vertical drift of horizontal swipes over wide content.
      if (
        !event.ctrlKey &&
        !event.defaultPrevented &&
        Math.abs(event.deltaY) >= Math.abs(event.deltaX)
      ) {
        recordIntent(event.deltaY, event.target);
      }
    },
    onTouchStart: (event: React.TouchEvent<HTMLDivElement>) => {
      const point = event.touches.length === 1 ? event.touches[0] : null;
      touch = point && { x: point.clientX, y: point.clientY };
    },
    onTouchMove: (event: React.TouchEvent<HTMLDivElement>) => {
      const point = event.touches.length === 1 ? event.touches[0] : null;
      if (point && touch && !event.defaultPrevented) {
        const deltaY = touch.y - point.clientY;
        if (Math.abs(deltaY) >= Math.abs(touch.x - point.clientX)) {
          recordIntent(deltaY, event.target);
        }
      }
      touch = point && { x: point.clientX, y: point.clientY };
    },
    onPointerDown: (event: React.PointerEvent<HTMLDivElement>) => {
      // Presses on transcript content are not scrollbar drags.
      if (event.target === event.currentTarget) holdForPointer();
    },
  };

  return { connect, followTranscript, resetTranscriptScroll, transcriptScrollProps };
};

/**
 * Keeps the chat transcript pinned to its bottom while the reader follows it, and
 * stops when the reader scrolls away. The returned functions are stable; call
 * `followTranscript` from a layout effect after content changes.
 */
export const useTranscriptAutoScroll = () => {
  const [autoScroll] = useState(createTranscriptAutoScroll);
  useEffect(() => autoScroll.connect(), [autoScroll]);
  return autoScroll;
};
