import { useEffect } from 'react';
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

const TerminalPage = () => {
  const params = new URLSearchParams(window.location.search);
  const cwdLabel = params.get('cwd') ?? '';
  const conversationId = params.get('conversationId')?.trim() || undefined;

  useEffect(() => {
    const documentClassName = 'terminal-popout-active';
    let record: TerminalPopOutRecord = {
      id: getTerminalPopOutSessionId(),
      cwd: cwdLabel,
      conversationId,
      state: 'active',
      updatedAt: Date.now(),
      version: 2,
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
        cwd: record.cwd,
        conversationId: record.conversationId,
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
  }, [conversationId, cwdLabel]);

  return (
    <main className="terminal-popout-page" data-testid="terminal-popout-page">
      <TerminalModal
        conversationId={conversationId}
        cwdLabel={cwdLabel}
        open
        onClose={() => window.close()}
        showPopOut={false}
      />
    </main>
  );
};

export default TerminalPage;
