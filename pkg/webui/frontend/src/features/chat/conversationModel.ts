import type { Conversation, PendingImageAttachment } from '../../types';

/** Conversation data helpers for the chat page. */

export const normalizeConversation = (conversation: Conversation): Conversation => ({
  ...conversation,
  cwd:
    typeof conversation.cwd === 'string' && conversation.cwd.trim()
      ? conversation.cwd.trim()
      : undefined,
  profile:
    typeof conversation.profile === 'string' && conversation.profile.trim()
      ? conversation.profile.trim()
      : undefined,
  reasoningEffort:
    typeof conversation.reasoningEffort === 'string' && conversation.reasoningEffort.trim()
      ? conversation.reasoningEffort.trim().toLowerCase()
      : undefined,
  environmentProfile:
    typeof conversation.environmentProfile === 'string' && conversation.environmentProfile.trim()
      ? conversation.environmentProfile.trim()
      : undefined,
  messages: (conversation.messages || []).map((message) => ({
    id: message.id,
    anchor: message.anchor,
    extensionId: message.extensionId,
    role: message.role || 'user',
    content: message.content || '',
    kind: message.kind,
    compaction: message.compaction,
    resourceReloadId: message.resourceReloadId,
    toolCalls: message.toolCalls || message.tool_calls || [],
    thinkingText: message.thinkingText,
    thinkingTexts: message.thinkingTexts || [],
  })),
  pendingSteer: (conversation.pendingSteer || []).map((message) => ({
    role: message.role || 'user',
    content: message.content || '',
  })),
  toolResults: conversation.toolResults || {},
});

export const mergeConversationUsage = (
  currentConversation: Conversation | null,
  usage: Conversation['usage']
): Conversation | null => {
  if (!currentConversation || !usage) {
    return currentConversation;
  }

  return {
    ...currentConversation,
    usage,
  };
};

const randomHex = (byteCount: number): string => {
  const bytes = new Uint8Array(byteCount);
  if (typeof crypto !== 'undefined' && crypto.getRandomValues) {
    crypto.getRandomValues(bytes);
  } else {
    for (let index = 0; index < bytes.length; index += 1) {
      bytes[index] = Math.floor(Math.random() * 256);
    }
  }

  return Array.from(bytes, (byte) => byte.toString(16).padStart(2, '0')).join('');
};

export const generateConversationId = (): string => {
  const timestamp = new Date()
    .toISOString()
    .replace(/[-:]/g, '')
    .replace(/\.\d{3}Z$/, '');
  return `${timestamp}-${randomHex(8)}`;
};

export const buildConversationPreview = (
  prompt: string,
  attachments: PendingImageAttachment[]
): string => {
  const trimmedPrompt = prompt.trim();
  if (trimmedPrompt) {
    return trimmedPrompt;
  }

  if (attachments.length === 1) {
    return attachments[0].name || 'Image attachment';
  }

  if (attachments.length > 1) {
    return `${attachments.length} image attachments`;
  }

  return 'Untitled conversation';
};

export const reconcileStreamCWDForDisplay = (
  canonicalCWD: string | undefined,
  currentCWD: string,
  isRemote: boolean
): string => {
  const canonical = canonicalCWD?.trim();
  const current = currentCWD.trim();
  if (!canonical) {
    return current;
  }

  // Conversation APIs compact paths under the control-plane home directory. Preserve that
  // display form when the live local stream confirms the equivalent absolute path.
  if (
    !isRemote &&
    (current === '~' || current.startsWith('~/')) &&
    (current === '~' || canonical.endsWith(current.slice(1)))
  ) {
    return current;
  }

  return canonical;
};
