import { type ClipboardEvent, type DragEvent, useState } from 'react';
import type { ContentBlock, PendingImageAttachment } from '../../types';
import { showToast } from '../../utils';

const MAX_IMAGE_ATTACHMENTS = 10;
const MAX_IMAGE_BYTES = 5 * 1024 * 1024;
const SUPPORTED_IMAGE_TYPES = new Set(['image/png', 'image/jpeg', 'image/gif', 'image/webp']);

const attachmentId = (): string =>
  typeof crypto !== 'undefined' && 'randomUUID' in crypto
    ? crypto.randomUUID()
    : `attachment-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;

const readFileAsDataUrl = (file: File): Promise<string> =>
  new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => {
      if (typeof reader.result === 'string') {
        resolve(reader.result);
        return;
      }
      reject(new Error('Failed to read image data'));
    };
    reader.onerror = () => reject(reader.error || new Error('Failed to read image data'));
    reader.readAsDataURL(file);
  });

const fileToPendingAttachment = async (file: File): Promise<PendingImageAttachment> => {
  if (!SUPPORTED_IMAGE_TYPES.has(file.type)) {
    throw new Error('Only PNG, JPEG, GIF, and WebP images are supported');
  }
  if (file.size > MAX_IMAGE_BYTES) {
    throw new Error('Each image must be 5MB or smaller');
  }

  const dataUrl = await readFileAsDataUrl(file);
  const [, base64 = ''] = dataUrl.split(',', 2);
  return {
    id: attachmentId(),
    name: file.name || 'Pasted image',
    mediaType: file.type,
    data: base64,
    previewUrl: dataUrl,
    size: file.size,
  };
};

export const buildUserContent = (
  prompt: string,
  attachments: PendingImageAttachment[]
): ContentBlock[] => [
  ...(prompt ? [{ type: 'text' as const, text: prompt }] : []),
  ...attachments.map((attachment) => ({
    type: 'image' as const,
    source: { data: attachment.data, media_type: attachment.mediaType },
  })),
];

export const useChatAttachments = () => {
  const [attachments, setAttachments] = useState<PendingImageAttachment[]>([]);
  const [dragActive, setDragActive] = useState(false);

  const append = async (files: File[]) => {
    if (files.length === 0) return;
    const remainingSlots = Math.max(MAX_IMAGE_ATTACHMENTS - attachments.length, 0);
    if (remainingSlots === 0) {
      showToast(`You can attach up to ${MAX_IMAGE_ATTACHMENTS} images`, 'error');
      return;
    }
    try {
      const nextAttachments = await Promise.all(
        files.slice(0, remainingSlots).map(fileToPendingAttachment)
      );
      setAttachments((current) => [...current, ...nextAttachments]);
    } catch (error) {
      showToast(error instanceof Error ? error.message : 'Failed to add image', 'error');
    }
  };

  const onPaste = async (event: ClipboardEvent<HTMLTextAreaElement>) => {
    const imageFiles = Array.from(event.clipboardData?.items || [])
      .filter((item) => item.kind === 'file' && item.type.startsWith('image/'))
      .map((item) => item.getAsFile())
      .filter((file): file is File => file !== null);
    if (imageFiles.length === 0) return;
    event.preventDefault();
    await append(imageFiles);
  };

  const onDragOver = (event: DragEvent<HTMLDivElement>) => {
    if (Array.from(event.dataTransfer.items || []).some((item) => item.kind === 'file')) {
      event.preventDefault();
      setDragActive(true);
    }
  };

  const onDragLeave = (event: DragEvent<HTMLDivElement>) => {
    if (!event.currentTarget.contains(event.relatedTarget as Node | null)) setDragActive(false);
  };

  const onDrop = async (event: DragEvent<HTMLDivElement>) => {
    event.preventDefault();
    setDragActive(false);
    await append(
      Array.from(event.dataTransfer.files || []).filter((file) => file.type.startsWith('image/'))
    );
  };

  return {
    attachments,
    setAttachments,
    composerProps: {
      attachments,
      dragActive,
      onAttachImages: append,
      onRemoveAttachment: (id: string) =>
        setAttachments((current) => current.filter((attachment) => attachment.id !== id)),
      onPaste,
      onDragOver,
      onDragLeave,
      onDrop,
    },
  };
};
