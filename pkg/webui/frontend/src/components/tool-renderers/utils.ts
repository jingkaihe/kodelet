import { ToolResult } from '../../types';
import { escapeHtml, detectLanguageFromPath, formatFileSize, formatTimestamp, formatDuration, escapeUrl } from '../../utils';

// Helper functions for tool renderers
export const getMetadata = (toolResult: ToolResult, ...paths: string[]): unknown => {
  let value: unknown = toolResult.metadata;
  for (const path of paths) {
    if (!value || typeof value !== 'object' || value === null) return null;
    value = (value as Record<string, unknown>)[path];
  }
  return value;
};

export const getMetadataAny = (toolResult: ToolResult, paths: string[]): unknown => {
  for (const path of paths) {
    const value = getMetadata(toolResult, ...path.split('.'));
    if (value !== null && value !== undefined) return value;
  }
  return null;
};

// Check if image file
export const isImageFile = (path: string): boolean => {
  const imageExts = ['.png', '.jpg', '.jpeg', '.gif', '.bmp', '.webp'];
  return imageExts.some(ext => path.toLowerCase().endsWith(ext));
};

// Re-export utility functions for use in individual renderers
export {
  escapeHtml,
  detectLanguageFromPath,
  formatFileSize,
  formatTimestamp,
  formatDuration,
  escapeUrl
};
