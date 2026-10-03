import { useEffect, useMemo, useState } from 'react';
import TerminalModal from '../components/workspace/TerminalModal';
import {
  clearTerminalPopOutRecord,
  createTerminalPopOutChannel,
  getTerminalPopOutSessionId,
  getTerminalPopOutWindowName,
  isTerminalPopOutMessage,
  TERMINAL_POP_OUT_HEARTBEAT_INTERVAL,
  type TerminalPopOutMessage,
  type TerminalPopOutRecord,
  writeTerminalPopOutRecord,
} from '../components/workspace/terminalPopOut';
import apiService from '../services/api';
import type { WorkspaceTarget } from '../types';

const TerminalPage = () => {
  // Canonicalizing the URL must not change the attachment request or reload its conversation.
  const [params] = useState(() => new URLSearchParams(window.location.search));
  const requestedCWD = params.get('cwd') || undefined;
  const runnerId = params.get('runnerId')?.trim() || undefined;
  const conversationId = params.get('conversationId')?.trim() || undefined;
  const [target, setTarget] = useState<WorkspaceTarget | null>(() =>
    conversationId
      ? null
      : runnerId
        ? { kind: 'runner', runnerId, cwd: requestedCWD }
        : { kind: 'local', cwd: requestedCWD }
  );
  const [canonicalCWD, setCanonicalCWD] = useState<string>();
  const [targetError, setTargetError] = useState<string | null>(null);

  useEffect(() => {
    if (!conversationId) {
      return undefined;
    }

    let cancelled = false;
    setTarget(null);
    setCanonicalCWD(undefined);
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
        setTarget({
          kind: 'runner',
          runnerId: affinityRunnerId,
          conversationId,
          cwd: conversation.cwd,
        });
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

  const ownershipTarget = useMemo<WorkspaceTarget | null>(() => {
    if (!target || target.kind === 'local') return target;
    const cwd = canonicalCWD || target.cwd;
    if (!cwd?.startsWith('/')) return null;
    return cwd === target.cwd ? target : { ...target, cwd };
  }, [canonicalCWD, target]);

  useEffect(() => {
    if (!target) {
      return undefined;
    }
    const documentClassName = 'terminal-popout-active';
    document.documentElement.classList.add(documentClassName);
    document.body.classList.add(documentClassName);
    return () => {
      document.documentElement.classList.remove(documentClassName);
      document.body.classList.remove(documentClassName);
    };
  }, [target]);

  useEffect(() => {
    if (!ownershipTarget) {
      return undefined;
    }
    window.name = getTerminalPopOutWindowName(ownershipTarget);
    if (ownershipTarget.kind === 'runner' && ownershipTarget.cwd) {
      const url = new URL(window.location.href);
      url.searchParams.set('runnerId', ownershipTarget.runnerId);
      url.searchParams.set('cwd', ownershipTarget.cwd);
      window.history.replaceState(window.history.state, '', url);
    }
    let record: TerminalPopOutRecord = {
      id: getTerminalPopOutSessionId(),
      target: ownershipTarget,
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
      heartbeat = window.setInterval(announce, TERMINAL_POP_OUT_HEARTBEAT_INTERVAL);
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
    };
  }, [ownershipTarget]);

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
        cwdLabel={ownershipTarget?.cwd || target.cwd || ''}
        open
        onClose={() => window.close()}
        onReady={(event) => {
          if (target.kind === 'runner' && event.cwd.startsWith('/')) {
            setCanonicalCWD(event.cwd);
          }
        }}
        target={target}
        allowPopOut={false}
      />
    </main>
  );
};

export default TerminalPage;
