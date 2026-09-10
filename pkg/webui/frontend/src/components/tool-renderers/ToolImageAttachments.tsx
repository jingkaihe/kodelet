import React, { useState } from 'react';
import { Download, ExternalLink } from 'lucide-react';
import type { ToolAttachment, ToolResult } from '../../types';

// Build a same-origin URL from the server-issued code. Never fetch extension
// paths or arbitrary view URLs, including a configured external public origin.
export const imageAttachmentURL = (attachment: ToolAttachment): string | undefined => {
  if (
    attachment.error ||
    !attachment.artifactId ||
    !attachment.shortCode ||
    !/^[A-Za-z0-9_-]{1,128}$/.test(attachment.shortCode) ||
    !['image/png', 'image/jpeg', 'image/gif', 'image/webp'].includes(attachment.mimeType || '')
  ) {
    return undefined;
  }
  return `/i/${attachment.shortCode}`;
};

const ImageAttachment: React.FC<{ attachment: ToolAttachment; viewed: boolean }> = ({
  attachment,
  viewed,
}) => {
  const [failed, setFailed] = useState(false);
  const url = imageAttachmentURL(attachment);
  const alt =
    attachment.alt?.trim() || attachment.filename || (viewed ? 'Viewed image' : 'Generated image');

  return (
    <figure className="tool-image-attachment chat-uploaded-image">
      {url && !failed ? (
        <a
          href={url}
          target="_blank"
          rel="noopener noreferrer"
          aria-label={`Open full-size image: ${alt}`}
          title="Open full-size image in a new tab"
        >
          <img
            src={url}
            alt={alt}
            className="chat-uploaded-image-media"
            loading="lazy"
            decoding="async"
            referrerPolicy="no-referrer"
            onError={() => setFailed(true)}
          />
        </a>
      ) : (
        <p className="quiet-tool-warning" role="status">
          {attachment.error ? `Image unavailable: ${attachment.error}` : 'Image preview unavailable.'}
        </p>
      )}
      {url ? (
        <figcaption className="tool-image-actions">
          <a
            href={url}
            target="_blank"
            rel="noopener noreferrer"
            className="panel-action-button tool-image-action"
            aria-label={`Open full size in a new tab: ${alt}`}
            title="Open full-size image in a new tab"
          >
            <ExternalLink aria-hidden="true" className="h-3.5 w-3.5" strokeWidth={1.8} />
            <span>Open full size</span>
          </a>
          <a
            href={`${url}?download=1`}
            download
            className="panel-action-button tool-image-action"
            aria-label={`Download image: ${alt}`}
            title="Save image to your device"
          >
            <Download aria-hidden="true" className="h-3.5 w-3.5" strokeWidth={1.8} />
            <span>Download</span>
          </a>
        </figcaption>
      ) : null}
    </figure>
  );
};

const ToolImageAttachments: React.FC<{ toolResult: ToolResult }> = ({ toolResult }) => {
  const images = toolResult.attachments?.filter((attachment) => attachment.type === 'image') || [];
  if (images.length === 0) return null;

  return (
    <div className="tool-image-attachments">
      {images.map((attachment, index) => (
        <ImageAttachment
          key={`${attachment.artifactId || index}-${attachment.shortCode || ''}`}
          attachment={attachment}
          viewed={toolResult.toolName === 'view_image'}
        />
      ))}
    </div>
  );
};

export default ToolImageAttachments;
