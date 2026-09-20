import { useCallback, useRef, useState } from 'react';
import type { ChatStreamEvent, UIWidgetEvent } from '../../types';

interface UIWidgetVersion {
  generation: string;
  sequence: number;
}

const numericVersionParts = (version: string | undefined): [bigint, bigint] => {
  const [major = '0', minor = '0'] = (version || '0:0').split(':', 2);
  try {
    return [BigInt(major), BigInt(minor)];
  } catch {
    return [0n, 0n];
  }
};

const compareNumericVersions = (left: string | undefined, right: string | undefined): number => {
  const [leftMajor, leftMinor] = numericVersionParts(left);
  const [rightMajor, rightMinor] = numericVersionParts(right);
  if (leftMajor !== rightMajor) {
    return leftMajor > rightMajor ? 1 : -1;
  }
  if (leftMinor === rightMinor) {
    return 0;
  }
  return leftMinor > rightMinor ? 1 : -1;
};

export const useExtensionWidgets = () => {
  const [widgets, setWidgets] = useState<Record<string, UIWidgetEvent>>({});
  const versionsRef = useRef<Record<string, UIWidgetVersion>>({});
  const revisionRef = useRef<string | null>(null);
  const snapshotRevisionRef = useRef<string | null>(null);

  const reset = useCallback(() => {
    revisionRef.current = null;
    snapshotRevisionRef.current = null;
    versionsRef.current = {};
    setWidgets({});
  }, []);

  const handleEvent = useCallback((event: ChatStreamEvent): boolean => {
    if (event.kind === 'ui-widgets') {
      const incomingRevision = event.ui_widget_revision;
      const currentRevision = revisionRef.current;
      if (
        incomingRevision &&
        currentRevision &&
        compareNumericVersions(incomingRevision, currentRevision) < 0
      ) {
        return true;
      }
      const snapshot = Object.fromEntries(
        (event.ui_widgets || [])
          .filter((widget) => !widget.removed)
          .map((widget) => [widget.key, widget])
      );
      if (incomingRevision) {
        revisionRef.current = incomingRevision;
        snapshotRevisionRef.current = incomingRevision;
      }
      versionsRef.current = Object.fromEntries(
        Object.values(snapshot).map((widget) => [
          widget.key,
          { generation: widget.generation || '0:0', sequence: widget.frame.sequence },
        ])
      );
      setWidgets(snapshot);
      return true;
    }
    if (event.kind !== 'ui-widget' || !event.ui_widget) {
      return false;
    }

    const incomingRevision = event.ui_widget_revision;
    if (
      incomingRevision &&
      snapshotRevisionRef.current &&
      compareNumericVersions(incomingRevision, snapshotRevisionRef.current) <= 0
    ) {
      return true;
    }
    if (
      incomingRevision &&
      (!revisionRef.current || compareNumericVersions(incomingRevision, revisionRef.current) > 0)
    ) {
      revisionRef.current = incomingRevision;
    }

    const widget = event.ui_widget;
    if (!widget.frame || typeof widget.frame.sequence !== 'number') {
      return true;
    }
    const incomingVersion: UIWidgetVersion = {
      generation: widget.generation || '0:0',
      sequence: widget.frame.sequence,
    };
    const currentVersion = versionsRef.current[widget.key];
    if (currentVersion) {
      const generationOrder = compareNumericVersions(
        incomingVersion.generation,
        currentVersion.generation
      );
      if (
        generationOrder < 0 ||
        (generationOrder === 0 && incomingVersion.sequence <= currentVersion.sequence)
      ) {
        return true;
      }
    }
    versionsRef.current = { ...versionsRef.current, [widget.key]: incomingVersion };
    setWidgets((currentWidgets) => {
      if (widget.removed) {
        if (!currentWidgets[widget.key]) {
          return currentWidgets;
        }
        const nextWidgets = { ...currentWidgets };
        delete nextWidgets[widget.key];
        return nextWidgets;
      }
      return { ...currentWidgets, [widget.key]: widget };
    });
    return true;
  }, []);

  return { widgets: Object.values(widgets), handleEvent, reset };
};
