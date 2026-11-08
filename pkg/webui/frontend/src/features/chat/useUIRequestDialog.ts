import { useState } from 'react';
import apiService from '../../services/api';
import type {
  ChatStreamEvent,
  UIConfirmRequestEvent,
  UIInputRequestEvent,
  UISelectRequestEvent,
} from '../../types';
import { showToast } from '../../utils';
import { isBlockingUIRequestEvent } from './runStreams';

export type UIRequestDialogState =
  | { mode: 'input'; request: UIInputRequestEvent }
  | { mode: 'confirm'; request: UIConfirmRequestEvent }
  | { mode: 'select'; request: UISelectRequestEvent };

interface UIRequestDialogOptions {
  /** The conversation a prompt answers when it does not name one. */
  fallbackConversationId: string | null;
  /** Closes dialogs that a prompt replaces. */
  onBlockingRequest: () => void;
}

/**
 * Extension prompts and notifications from the page's event streams. Only the
 * page that submitted a run receives its prompts; answers are HTTP requests.
 */
export const useUIRequestDialog = ({
  fallbackConversationId,
  onBlockingRequest,
}: UIRequestDialogOptions) => {
  const [dialog, setDialog] = useState<UIRequestDialogState | null>(null);
  const [submitting, setSubmitting] = useState(false);

  /** Shows a prompt or notification, and reports whether the event was one. */
  const handleEvent = (event: ChatStreamEvent): boolean => {
    if (event.kind === 'ui-request-end') {
      setDialog((current) =>
        current &&
        current.request.id === event.ui_request_id &&
        current.request.conversationId === event.conversation_id
          ? null
          : current
      );
      return true;
    }
    if (event.kind === 'ui-notification' && event.ui_notify) {
      showToast(event.ui_notify.message, 'info', event.ui_notify.title);
      return true;
    }
    if (isBlockingUIRequestEvent(event)) {
      onBlockingRequest();
    }

    if (event.kind === 'ui-input-request' && event.ui_input) {
      setDialog({
        mode: 'input',
        request: {
          ...event.ui_input,
          conversationId: event.conversation_id,
        },
      });
      setSubmitting(false);
      return true;
    }

    if (event.kind === 'ui-confirm-request' && event.ui_confirm) {
      setDialog({
        mode: 'confirm',
        request: {
          ...event.ui_confirm,
          conversationId: event.conversation_id,
        },
      });
      setSubmitting(false);
      return true;
    }

    if (event.kind === 'ui-select-request' && event.ui_select) {
      setDialog({
        mode: 'select',
        request: {
          ...event.ui_select,
          conversationId: event.conversation_id,
        },
      });
      setSubmitting(false);
      return true;
    }

    return false;
  };

  const respond = async (
    current: UIRequestDialogState,
    response: { status: 'submitted' | 'dismissed'; value?: string }
  ) => {
    const request = current.request;
    let payload = response;
    if (current.mode === 'confirm' && response.status === 'submitted') {
      payload = { ...response, value: 'true' };
    }
    if (current.mode === 'confirm' && response.status === 'dismissed') {
      payload = { ...response, value: 'false' };
    }

    const targetConversationId = request.conversationId || fallbackConversationId;
    if (!targetConversationId) {
      showToast('Cannot answer extension prompt before conversation starts', 'error');
      return;
    }

    setSubmitting(true);
    try {
      await apiService.respondToUIInput(targetConversationId, request.id, payload);
      setDialog((currentDialog) =>
        currentDialog?.request.id === request.id ? null : currentDialog
      );
    } catch (error) {
      const message = error instanceof Error ? error.message : 'Failed to answer extension prompt';
      showToast(message, 'error');
    } finally {
      setSubmitting(false);
    }
  };

  return { dialog, submitting, handleEvent, respond, close: () => setDialog(null) };
};
