import { fireEvent, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it } from 'vitest';
import type { ToolAttachment, ToolResult } from '../../types';
import ToolImageAttachments from './ToolImageAttachments';

const image: ToolAttachment = {
  type: 'image',
  artifactId: 'art_internal',
  shortCode: 'public_image-code',
  viewUrl: 'https://public.example/i/public_image-code',
  filename: 'chart.png',
  mimeType: 'image/png',
  alt: 'Monthly build durations',
};

const result: ToolResult = {
  toolName: 'draw_chart',
  success: true,
  attachments: [image],
};

describe('ToolImageAttachments', () => {
  it('uses a same-origin image source and provides open and download actions', () => {
    render(<ToolImageAttachments toolResult={result} />);

    const preview = screen.getByRole('img', { name: image.alt });
    expect(preview).toHaveAttribute('src', '/i/public_image-code');
    expect(preview).toHaveAttribute('loading', 'lazy');
    expect(preview.closest('figure')).toHaveClass('tool-image-attachment');
    expect(preview.closest('figure')).not.toHaveClass('chat-uploaded-image');
    const open = screen.getByRole('link', { name: `Open full size in a new tab: ${image.alt}` });
    expect(open).toHaveTextContent('Open full size');
    expect(open).toHaveAttribute('href', '/i/public_image-code');
    expect(open).toHaveAttribute('target', '_blank');
    expect(open).toHaveAttribute('rel', 'noopener noreferrer');
    expect(open).toHaveAttribute('title', 'Open full-size image in a new tab');
    expect(open).not.toHaveAttribute('download');

    const download = screen.getByRole('link', { name: `Download image: ${image.alt}` });
    expect(download).toHaveTextContent('Download');
    expect(download).toHaveAttribute('href', '/i/public_image-code?download=1');
    expect(download).toHaveAttribute('download');
    expect(download).toHaveAttribute('title', 'Save image to your device');
    expect(download).not.toHaveAttribute('target');
    for (const action of [open, download]) {
      expect(action).toHaveClass('tool-image-action');
      expect(action).not.toHaveClass('panel-action-button');
      expect(action.querySelector('svg')).toHaveAttribute('aria-hidden', 'true');
    }
  });

  it('keeps the preview and both actions in the keyboard tab order', async () => {
    const user = userEvent.setup();
    render(<ToolImageAttachments toolResult={result} />);

    await user.tab();
    expect(screen.getByRole('link', { name: `Open full-size image: ${image.alt}` })).toHaveFocus();
    await user.tab();
    expect(
      screen.getByRole('link', { name: `Open full size in a new tab: ${image.alt}` })
    ).toHaveFocus();
    await user.tab();
    expect(screen.getByRole('link', { name: `Download image: ${image.alt}` })).toHaveFocus();
  });

  it('renders all attached images and uses the filename as alternate text when needed', () => {
    render(
      <ToolImageAttachments
        toolResult={{
          ...result,
          attachments: [
            image,
            { ...image, artifactId: 'art_second', shortCode: 'second', alt: '' },
          ],
        }}
      />
    );

    expect(screen.getAllByRole('img')).toHaveLength(2);
    expect(screen.getByRole('img', { name: 'chart.png' })).toHaveAttribute('src', '/i/second');
  });

  it.each([
    { ...image, artifactId: undefined, path: '/tmp/chart.png', viewUrl: 'file:///tmp/chart.png' },
    { ...image, shortCode: '../private' },
    { ...image, shortCode: 'invalid?token=secret' },
    { ...image, mimeType: 'image/svg+xml' },
  ])('does not load unregistered or unsafe image references', (attachment) => {
    render(<ToolImageAttachments toolResult={{ ...result, attachments: [attachment] }} />);

    expect(screen.queryByRole('img')).not.toBeInTheDocument();
    expect(screen.queryByRole('link')).not.toBeInTheDocument();
    expect(screen.getByRole('status')).toHaveTextContent('Image preview unavailable.');
    expect(screen.getByRole('status').tagName).toBe('OUTPUT');
  });

  it('ignores arbitrary view URLs and fetches only the registered image route', () => {
    render(
      <ToolImageAttachments
        toolResult={{
          ...result,
          attachments: [{ ...image, viewUrl: 'javascript:alert(1)' }],
        }}
      />
    );

    expect(screen.getByRole('img')).toHaveAttribute('src', '/i/public_image-code');
  });

  it('shows an upload failure without trying to fetch the image', () => {
    render(
      <ToolImageAttachments
        toolResult={{
          ...result,
          attachments: [{ type: 'image', error: 'Upload interrupted' }],
        }}
      />
    );

    expect(screen.getByRole('status')).toHaveTextContent('Image unavailable: Upload interrupted');
    expect(screen.queryByRole('img')).not.toBeInTheDocument();
  });

  it('replaces a failed preview with a useful fallback while retaining the open action', () => {
    render(<ToolImageAttachments toolResult={result} />);
    fireEvent.error(screen.getByRole('img'));

    expect(screen.getByRole('status')).toHaveTextContent('Image preview unavailable.');
    expect(screen.queryByRole('img')).not.toBeInTheDocument();
    expect(
      screen.getByRole('link', { name: `Open full size in a new tab: ${image.alt}` })
    ).toBeInTheDocument();
  });
});
