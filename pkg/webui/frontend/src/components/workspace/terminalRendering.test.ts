import { readFile } from 'node:fs/promises';
import { createRequire } from 'node:module';
import { CanvasRenderer, Ghostty } from 'ghostty-web';
import { afterEach, expect, it, vi } from 'vitest';

afterEach(() => {
  vi.restoreAllMocks();
  vi.useRealTimers();
});

// Verify the installed dependency with its real WASM, not MockTerminal.
// Detailed renderer behavior is covered in the fork's own test suite.
it('does not draw static scrollback but still paints scrolling and new output', async () => {
  vi.useFakeTimers();
  const context = {
    measureText: () => ({ width: 8, actualBoundingBoxAscent: 11, actualBoundingBoxDescent: 3 }),
    scale: vi.fn(),
    clearRect: vi.fn(),
    fillRect: vi.fn(),
    fillText: vi.fn(),
    beginPath: vi.fn(),
    moveTo: vi.fn(),
    lineTo: vi.fn(),
    stroke: vi.fn(),
    save: vi.fn(),
    restore: vi.fn(),
    rect: vi.fn(),
    clip: vi.fn(),
  };
  vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockReturnValue(
    context as unknown as CanvasRenderingContext2D
  );
  const bytes = await readFile(
    createRequire(import.meta.url).resolve('ghostty-web/ghostty-vt.wasm')
  );
  const { instance } = await WebAssembly.instantiate(bytes, { env: { log: () => {} } });
  const buffer = new Ghostty(instance).createTerminal(80, 24, { scrollbackLimit: 100 });
  const renderer = new CanvasRenderer(document.createElement('canvas'), { cursorBlink: true });
  try {
    buffer.write('Static history\r\n'.repeat(150));
    renderer.render(buffer, false, 20, buffer, 0);
    expect(context.fillText).toHaveBeenCalled();
    vi.clearAllMocks();

    for (let frame = 0; frame < 300; frame++) {
      vi.advanceTimersByTime(1000 / 60);
      renderer.render(buffer, false, 20, buffer, 0);
    }
    expect(context.fillText.mock.calls.length).toBe(0);
    expect(context.fillRect.mock.calls.length).toBe(0);
    expect(context.clearRect.mock.calls.length).toBe(0);
    expect(context.stroke.mock.calls.length).toBe(0);

    renderer.render(buffer, false, 20.5, buffer, 0);
    expect(context.fillText).toHaveBeenCalled();
    vi.clearAllMocks();

    buffer.write('New output\r\n');
    renderer.render(buffer, false, 20.5, buffer, 0);
    expect(context.fillText).toHaveBeenCalled();
  } finally {
    renderer.dispose();
    buffer.free();
  }
});
