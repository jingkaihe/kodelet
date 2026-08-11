import { describe, it, expect } from 'vitest';
import { getMetadata, getMetadataAny, isImageFile } from './utils';
import { ToolResult } from '../../types';

describe('getMetadata', () => {
  const mockToolResult: ToolResult = {
    toolName: 'test-tool',
    success: true,
    error: undefined,
    timestamp: '2023-01-01T00:00:00Z',
    metadata: {
      level1: {
        level2: {
          level3: 'deep value'
        },
        simple: 'simple value'
      },
      topLevel: 'top value'
    }
  };

  it('retrieves top-level metadata', () => {
    const result = getMetadata(mockToolResult, 'topLevel');
    expect(result).toBe('top value');
  });

  it('retrieves nested metadata', () => {
    const result = getMetadata(mockToolResult, 'level1', 'simple');
    expect(result).toBe('simple value');
  });

  it('retrieves deeply nested metadata', () => {
    const result = getMetadata(mockToolResult, 'level1', 'level2', 'level3');
    expect(result).toBe('deep value');
  });

  it('returns undefined for non-existent path', () => {
    const result = getMetadata(mockToolResult, 'nonExistent');
    expect(result).toBeUndefined();
  });

  it('returns null when traversing through non-object', () => {
    const result = getMetadata(mockToolResult, 'topLevel', 'invalid');
    expect(result).toBeNull();
  });

  it('handles null metadata', () => {
    const nullResult: ToolResult = {
      ...mockToolResult,
      metadata: undefined
    };
    const result = getMetadata(nullResult, 'any');
    expect(result).toBeNull();
  });

  it('handles undefined metadata', () => {
    const undefinedResult: ToolResult = {
      ...mockToolResult,
      metadata: undefined
    };
    const result = getMetadata(undefinedResult, 'any');
    expect(result).toBeNull();
  });
});

describe('getMetadataAny', () => {
  const mockToolResult: ToolResult = {
    toolName: 'test-tool',
    success: true,
    error: undefined,
    timestamp: '2023-01-01T00:00:00Z',
    metadata: {
      option1: 'value1',
      nested: {
        option2: 'value2'
      }
    }
  };

  it('returns first found value', () => {
    const result = getMetadataAny(mockToolResult, ['nonExistent', 'option1']);
    expect(result).toBe('value1');
  });

  it('handles dot notation paths', () => {
    const result = getMetadataAny(mockToolResult, ['nonExistent', 'nested.option2']);
    expect(result).toBe('value2');
  });

  it('returns null when no paths match', () => {
    const result = getMetadataAny(mockToolResult, ['nonExistent1', 'nonExistent2']);
    expect(result).toBeNull();
  });

  it('returns first non-null value', () => {
    const toolResult: ToolResult = {
      ...mockToolResult,
      metadata: {
        option1: null,
        option2: undefined,
        option3: 'found'
      }
    };
    const result = getMetadataAny(toolResult, ['option1', 'option2', 'option3']);
    expect(result).toBe('found');
  });
});

describe('isImageFile', () => {
  it('returns true for image extensions', () => {
    expect(isImageFile('photo.png')).toBe(true);
    expect(isImageFile('image.jpg')).toBe(true);
    expect(isImageFile('picture.jpeg')).toBe(true);
    expect(isImageFile('animation.gif')).toBe(true);
    expect(isImageFile('bitmap.bmp')).toBe(true);
    expect(isImageFile('modern.webp')).toBe(true);
  });

  it('returns false for non-image extensions', () => {
    expect(isImageFile('document.pdf')).toBe(false);
    expect(isImageFile('script.js')).toBe(false);
    expect(isImageFile('data.json')).toBe(false);
    expect(isImageFile('readme.md')).toBe(false);
  });

  it('handles case insensitive matching', () => {
    expect(isImageFile('PHOTO.PNG')).toBe(true);
    expect(isImageFile('Image.JPG')).toBe(true);
    expect(isImageFile('Picture.JPEG')).toBe(true);
  });

  it('handles files with multiple dots', () => {
    expect(isImageFile('my.photo.backup.png')).toBe(true);
    expect(isImageFile('screenshot.2023.01.01.jpg')).toBe(true);
  });

  it('returns false for files without extensions', () => {
    expect(isImageFile('photo')).toBe(false);
    expect(isImageFile('image')).toBe(false);
  });

  it('returns false for partial matches', () => {
    expect(isImageFile('photo.png.txt')).toBe(false);
    expect(isImageFile('jpgfile')).toBe(false);
  });
});
