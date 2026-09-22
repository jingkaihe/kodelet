import path from 'node:path';
import postcss from 'postcss';
import tailwindcss from 'tailwindcss';
import loadConfig from 'tailwindcss/loadConfig';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import foundationStyles from './foundation.css?raw';

describe('Generated component styles', () => {
  it('excludes unused DaisyUI selectors that scan the transcript on textarea edits', async () => {
    const config = loadConfig(path.resolve('tailwind.config.js'));
    const result = await postcss([
      tailwindcss({
        ...config,
        content: [
          {
            // Tailwind detects these prose and method names as class candidates.
            raw: `// A custom modal dialog
const text = lines.join(' ');
const classes = 'btn flex';`,
            extension: 'tsx',
          },
        ],
      }),
    ]).process('@tailwind base; @tailwind components; @tailwind utilities;', { from: undefined });
    const selectors: string[] = [];
    result.root.walkRules((rule) => {
      selectors.push(rule.selector);
    });

    expect(
      selectors.filter((selector) => selector.includes(':has(') && /:root|\.join\b/.test(selector))
    ).toEqual([]);
    expect(selectors).toContain('.btn');
    expect(selectors).toContain('.flex');
    expect(result.css).toContain('[data-theme=gruvbox-dark]');
  });
});

describe('Text-entry typography', () => {
  let stylesheet: HTMLStyleElement;
  let container: HTMLDivElement;

  beforeEach(() => {
    stylesheet = document.createElement('style');
    stylesheet.textContent = foundationStyles;
    document.head.append(stylesheet);
    container = document.createElement('div');
    document.body.append(container);
  });

  afterEach(() => {
    stylesheet.remove();
    container.remove();
  });

  it.each([
    '<input>',
    '<input type="text">',
    '<input type="search" class="conversation-search-input">',
    '<input type="password" class="new-chat-field-control">',
    '<input type="email">',
    '<input type="url" class="font-mono">',
    '<input type="tel">',
    '<input type="number">',
    '<textarea></textarea>',
    '<textarea class="composer-editor"></textarea>',
    '<div contenteditable="true"></div>',
    '<div contenteditable></div>',
    '<div contenteditable="plaintext-only"></div>',
  ])('disables contextual ligatures for %s without a composer-specific rule', (markup) => {
    container.innerHTML = markup;
    const field = container.firstElementChild as HTMLElement;

    expect(getComputedStyle(field).fontVariantLigatures).toBe('no-contextual');
    field.focus();
    expect(getComputedStyle(field).fontVariantLigatures).toBe('no-contextual');
    field.blur();
    expect(getComputedStyle(field).fontVariantLigatures).toBe('no-contextual');
  });

  it.each([
    '<p>... &gt;=</p>',
    '<pre><code>... &gt;=</code></pre>',
    '<button>...</button>',
    '<select><option>...</option></select>',
    '<div contenteditable="false">...</div>',
  ])('leaves non-editable typography unchanged for %s', (markup) => {
    container.innerHTML = markup;

    for (const element of container.querySelectorAll('*')) {
      expect(getComputedStyle(element).fontVariantLigatures).not.toBe('no-contextual');
    }
  });
});
