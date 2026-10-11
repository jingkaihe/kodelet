import path from 'node:path';
import { compile } from '@tailwindcss/node';
import postcss from 'postcss';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import foundationStyles from './foundation.css?raw';
import appStyles from './index.css?raw';
import sidebarStyles from './sidebar.css?raw';

describe('Generated component styles', () => {
  it('excludes unused DaisyUI selectors that scan the transcript on textarea edits', async () => {
    const compiler = await compile(appStyles, {
      base: path.resolve('src/styles'),
      onDependency: () => {},
    });
    // Tailwind detects prose such as "a custom modal dialog" and lines.join(' ') as candidates.
    const css = compiler.build([
      'modal',
      'modal-open',
      'join',
      'drawer-toggle',
      'drawer-side',
      'btn',
      'flex',
    ]);
    const selectors: string[] = [];
    postcss.parse(css).walkRules((rule) => {
      selectors.push(rule.selector);
    });

    expect(
      selectors.filter((selector) => selector.includes(':has(') && /:root|\.join\b/.test(selector))
    ).toEqual([]);
    expect(selectors).toContain('.btn');
    expect(selectors).toContain('.flex');
    expect(selectors).toContain(':root[data-theme="gruvbox-dark"]');
  });
});

describe('Sidebar toggle visibility', () => {
  it('hides the mobile toggle on desktop in the same cascade layer as its button styles', () => {
    const stylesheet = postcss.parse(sidebarStyles);
    const displayRules: { selector: string; display: string; media?: string }[] = [];
    stylesheet.walkDecls('display', (declaration) => {
      const rule = declaration.parent;
      if (
        rule?.type !== 'rule' ||
        !['.sidebar-toggle-button', '.sidebar-toggle-button-mobile'].includes(rule.selector)
      )
        return;
      const parent = rule.parent;
      if (parent?.type === 'root') {
        displayRules.push({ selector: rule.selector, display: declaration.value });
      } else if (parent?.type === 'atrule' && parent.name === 'media') {
        expect(parent.parent?.type).toBe('root');
        displayRules.push({
          selector: rule.selector,
          display: declaration.value,
          media: parent.params,
        });
      }
    });

    expect(displayRules).toEqual([
      { selector: '.sidebar-toggle-button', display: 'inline-flex' },
      {
        selector: '.sidebar-toggle-button-mobile',
        display: 'none',
        media: '(min-width: 1024px)',
      },
    ]);
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
