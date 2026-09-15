import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import foundationStyles from './foundation.css?raw';

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
