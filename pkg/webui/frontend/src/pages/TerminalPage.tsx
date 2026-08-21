import { useEffect, useMemo, useState } from 'react';
import TerminalModal from '../components/workspace/TerminalModal';
import {
  clearTerminalPopOutRecord,
  createTerminalPopOutChannel,
  getTerminalPopOutSessionId,
  isTerminalPopOutMessage,
  TERMINAL_POP_OUT_HEARTBEAT_INTERVAL,
  type TerminalPopOutMessage,
  type TerminalPopOutRecord,
  writeTerminalPopOutRecord,
} from '../components/workspace/terminalPopOut';
import apiService from '../services/api';
import type { WorkspaceTarget } from '../types';

const TerminalPage = () => {
  const params = new URLSearchParams(window.location.search);
  const cwdLabel = params.get('cwd') ?? '';
  const runnerId = params.get('runnerId')?.trim() || undefined;
  const conversationId = params.get('conversationId')?.trim() || undefined;
  const [resolvedRunnerId, setResolvedRunnerId] = useState(
    conversationId ? undefined : runnerId
  );
  const [targetError, setTargetError] = useState<string | null>(null);

  useEffect(() => {
    if (!conversationId) {
      setResolvedRunnerId(runnerId);
      setTargetError(null);
      return undefined;
    }

    let cancelled = false;
    setResolvedRunnerId(undefined);
    setTargetError(null);
    void apiService
      .getConversation(conversationId)
      .then((conversation) => {
        if (cancelled) {
          return;
        }
        const affinityRunnerId = conversation.runnerId;
        if (!affinityRunnerId) {
          setTargetError('This conversation has no remote runner terminal.');
          return;
        }
        if (runnerId && runnerId !== affinityRunnerId) {
          setTargetError('The terminal runner does not match this conversation.');
          return;
        }
        setResolvedRunnerId(affinityRunnerId);
        setTargetError(null);
      })
      .catch(() => {
        if (!cancelled) {
          setTargetError('Unable to resolve the remote terminal.');
        }
      });

    return () => {
      cancelled = true;
    };
  }, [conversationId, runnerId]);

  const target = useMemo<WorkspaceTarget | null>(
    () =>
      resolvedRunnerId
        ? { kind: 'runner', runnerId: resolvedRunnerId, conversationId }
        : conversationId
          ? null
          : { kind: 'local', cwd: cwdLabel || undefined },
    [conversationId, cwdLabel, resolvedRunnerId]
  );

  useEffect(() => {
    if (!target) {
      return undefined;
    }
    const documentClassName = 'terminal-popout-active';
    let record: TerminalPopOutRecord = {
      id: getTerminalPopOutSessionId(),
      target,
      state: 'active',
      updatedAt: Date.now(),
    };
    const channel = createTerminalPopOutChannel();
    let active = false;
    let unloading = false;
    let heartbeat: number | null = null;

    const announce = () => {
      if (!active) {
        return;
      }
      record = { ...record, state: 'active', updatedAt: Date.now() };
      writeTerminalPopOutRecord(record);
      channel?.postMessage({ type: 'active', record } satisfies TerminalPopOutMessage);
    };

    const stopHeartbeat = () => {
      if (heartbeat !== null) {
        window.clearInterval(heartbeat);
        heartbeat = null;
      }
    };

    const activate = () => {
      if (active) {
        return;
      }
      active = true;
      unloading = false;
      announce();
      heartbeat = window.setInterval(
        announce,
        TERMINAL_POP_OUT_HEARTBEAT_INTERVAL
      );
    };

    const deactivate = (clearRecord: boolean) => {
      if (!active) {
        return;
      }
      active = false;
      stopHeartbeat();
      if (clearRecord) {
        clearTerminalPopOutRecord(record.id);
      } else {
        record = { ...record, state: 'closing', updatedAt: Date.now() };
        writeTerminalPopOutRecord(record);
      }
      channel?.postMessage({
        type: 'closing',
        id: record.id,
        target: record.target,
      } satisfies TerminalPopOutMessage);
    };

    const handleChannelMessage = (event: MessageEvent<unknown>) => {
      if (isTerminalPopOutMessage(event.data) && event.data.type === 'probe') {
        announce();
      }
    };
    const handleBeforeUnload = () => {
      unloading = true;
      deactivate(false);
    };
    const handlePageHide = (event: PageTransitionEvent) => {
      unloading = !event.persisted;
      deactivate(false);
    };
    const handlePageShow = (event: PageTransitionEvent) => {
      if (event.persisted) {
        activate();
      }
    };

    document.documentElement.classList.add(documentClassName);
    document.body.classList.add(documentClassName);
    channel?.addEventListener('message', handleChannelMessage);
    window.addEventListener('beforeunload', handleBeforeUnload);
    window.addEventListener('pagehide', handlePageHide);
    window.addEventListener('pageshow', handlePageShow);
    activate();

    return () => {
      channel?.removeEventListener('message', handleChannelMessage);
      window.removeEventListener('beforeunload', handleBeforeUnload);
      window.removeEventListener('pagehide', handlePageHide);
      window.removeEventListener('pageshow', handlePageShow);
      deactivate(!unloading);
      stopHeartbeat();
      channel?.close();
      document.documentElement.classList.remove(documentClassName);
      document.body.classList.remove(documentClassName);
    };
  }, [target]);

  if (!target) {
    return (
      <main className="terminal-popout-page" data-testid="terminal-popout-page">
        <div className="workspace-modal-placeholder" role={targetError ? 'alert' : 'status'}>
          {targetError || 'Resolving remote terminal…'}
        </div>
      </main>
    );
  }

  return (
    <main className="terminal-popout-page" data-testid="terminal-popout-page">
      <TerminalModal
        cwdLabel={cwdLabel}
        open
        onClose={() => window.close()}
        target={target}
        allowPopOut={false}
      />
    </main>
  );
};

export default TerminalPage;
