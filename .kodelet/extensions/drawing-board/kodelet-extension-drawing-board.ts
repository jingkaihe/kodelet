#!/usr/bin/env tsx

import {
  defineExtension,
  type UIFrameLine,
  type UISurface,
  type UISurfaceInputEvent,
  type UISurfaceMouseEvent,
} from "../../../sdk/src/index.js";
import { runExtension } from "../../../sdk/src/runtime.js";
import { writeFile } from "node:fs/promises";
import path from "node:path";
import { deflateSync } from "node:zlib";

const colors = ["#f38ba8", "#fab387", "#f9e2af", "#a6e3a1", "#89dceb", "#cba6f7"] as const;
const canvasBackground = "#181825";
const chromeBackground = "#313244";
const primaryText = "#cdd6f4";
const mutedText = "#a6adc8";

class DrawingBoard {
  private cells: Array<Array<string | undefined>> = [];
  private width = 0;
  private height = 0;
  private brushColor = 0;
  private brushRadius = 0;
  private closed = false;
  private saving = false;
  private status = "Changes are kept while this surface is open.";

  constructor(
    private readonly surface: UISurface,
    private readonly cwd: string,
    private readonly onSaved: (filename: string) => Promise<void>,
    private readonly onClosed: (board: DrawingBoard) => void,
  ) {}

  start(): void {
    this.surface.onResize(({ width, height }) => {
      this.resize(width, height);
      this.render();
    });
    this.surface.onInput((event) => this.handleInput(event));
  }

  async close(): Promise<void> {
    if (this.closed) return;
    this.closed = true;
    try {
      await this.surface.close();
    } finally {
      this.onClosed(this);
    }
  }

  private resize(width: number, height: number): void {
    const nextWidth = Math.max(1, width);
    const nextHeight = Math.max(1, height - 3);
    const next = Array.from({ length: nextHeight }, () => Array<string | undefined>(nextWidth));
    for (let y = 0; y < Math.min(nextHeight, this.cells.length); y++) {
      for (let x = 0; x < Math.min(nextWidth, this.cells[y]?.length ?? 0); x++) {
        next[y][x] = this.cells[y][x];
      }
    }
    this.width = nextWidth;
    this.height = nextHeight;
    this.cells = next;
  }

  private handleInput(event: UISurfaceInputEvent): void {
    if (event.kind === "key") {
      this.handleKey(event.text || event.key || "");
      return;
    }
    if (event.kind === "mouse" && event.mouse) {
      this.handleMouse(event.mouse);
    }
  }

  private handleKey(key: string): void {
    const normalized = key.toLowerCase();
    if (normalized === "q" || normalized === "esc" || normalized === "ctrl+c") {
      void this.close().catch(() => undefined);
      return;
    }
    if (normalized === "c") {
      this.cells = Array.from({ length: this.height }, () => Array<string | undefined>(this.width));
    } else if (normalized === "s") {
      void this.savePNG();
      return;
    } else if (/^[1-6]$/.test(normalized)) {
      this.brushColor = Number(normalized) - 1;
    } else if (normalized === "[" || normalized === "-") {
      this.brushRadius = Math.max(0, this.brushRadius - 1);
    } else if (normalized === "]" || normalized === "+" || normalized === "=") {
      this.brushRadius = Math.min(3, this.brushRadius + 1);
    } else {
      return;
    }
    this.render();
  }

  private async savePNG(): Promise<void> {
    if (this.saving) return;
    const bounds = this.drawingBounds();
    if (!bounds) {
      this.status = "Nothing to save yet.";
      this.render();
      return;
    }

    const timestamp = new Date().toISOString().replace(/[-:.]/g, "").replace("T", "-");
    const filename = `drawing-board-${timestamp}.png`;
    this.saving = true;
    this.status = "Saving…";
    this.render();
    try {
      await writeFile(path.join(this.cwd, filename), this.renderPNG(bounds), { flag: "wx" });
      this.status = `Saved ${filename}`;
      try {
        await this.onSaved(filename);
      } catch {
        this.status = `Saved ${filename}; could not add the path to the transcript.`;
      }
    } catch (error) {
      this.status = `Save failed: ${error instanceof Error ? error.message : String(error)}`;
    } finally {
      this.saving = false;
    }
    this.render();
  }

  private drawingBounds(): { left: number; top: number; right: number; bottom: number } | undefined {
    let left = this.width;
    let top = this.height;
    let right = -1;
    let bottom = -1;
    for (let y = 0; y < this.cells.length; y++) {
      for (let x = 0; x < (this.cells[y]?.length ?? 0); x++) {
        if (!this.cells[y][x]) continue;
        left = Math.min(left, x);
        top = Math.min(top, y);
        right = Math.max(right, x);
        bottom = Math.max(bottom, y);
      }
    }
    if (right < left || bottom < top) return undefined;
    return {
      left: Math.max(0, left - 1),
      top: Math.max(0, top - 1),
      right: Math.min(this.width - 1, right + 1),
      bottom: Math.min(this.height - 1, bottom + 1),
    };
  }

  private renderPNG(bounds: { left: number; top: number; right: number; bottom: number }): Buffer {
    const cellWidth = 8;
    const cellHeight = 16;
    const width = (bounds.right - bounds.left + 1) * cellWidth;
    const height = (bounds.bottom - bounds.top + 1) * cellHeight;
    const scanlines = Buffer.alloc((width * 3 + 1) * height);
    const background = hexRGB(canvasBackground);
    for (let pixelY = 0; pixelY < height; pixelY++) {
      const rowOffset = pixelY * (width * 3 + 1);
      scanlines[rowOffset] = 0;
      const cellY = bounds.top + Math.floor(pixelY / cellHeight);
      for (let pixelX = 0; pixelX < width; pixelX++) {
        const cellX = bounds.left + Math.floor(pixelX / cellWidth);
        const color = hexRGB(this.cells[cellY]?.[cellX] ?? canvasBackground, background);
        const offset = rowOffset + 1 + pixelX * 3;
        scanlines[offset] = color[0];
        scanlines[offset + 1] = color[1];
        scanlines[offset + 2] = color[2];
      }
    }
    return encodePNG(width, height, scanlines);
  }

  private handleMouse(mouse: UISurfaceMouseEvent): void {
    if (mouse.action !== "press" && mouse.action !== "motion") return;
    if (mouse.button !== "left" && mouse.button !== "right") return;

    const canvasY = mouse.y - 2;
    if (mouse.x < 0 || mouse.x >= this.width || canvasY < 0 || canvasY >= this.height) return;
    const erase = mouse.button === "right" || mouse.ctrl;
    this.paint(mouse.x, canvasY, erase ? undefined : this.selectedColor());
    this.render();
  }

  private selectedColor(): string | undefined {
    return colors[this.brushColor];
  }

  private paint(centerX: number, centerY: number, color: string | undefined): void {
    for (let y = centerY - this.brushRadius; y <= centerY + this.brushRadius; y++) {
      if (y < 0 || y >= this.height) continue;
      for (let x = centerX - this.brushRadius; x <= centerX + this.brushRadius; x++) {
        if (x >= 0 && x < this.width) this.cells[y][x] = color;
      }
    }
  }

  private render(): void {
    if (this.closed || this.width === 0) return;
    const selected = this.selectedColor();
    const brushLabel = `color ${this.brushColor + 1}`;
    const lines: UIFrameLine[] = [
      this.chromeLine([
        { text: " DRAWING BOARD ", background: selected ?? canvasBackground, foreground: selected ? canvasBackground : primaryText },
        { text: `  ${brushLabel}  size ${this.brushRadius * 2 + 1}×${this.brushRadius * 2 + 1}` },
      ]),
      this.chromeLine([{ text: " left-drag: draw   right/Ctrl-left: erase   1-6: color   [ ]: size   c: clear   s: save PNG   q: close", foreground: mutedText }]),
      ...this.cells.map((row) => this.canvasLine(row)),
      this.chromeLine([
        ...colors.flatMap((color, index) => [
          { text: ` ${index + 1} `, foreground: canvasBackground, background: color },
          { text: " ", foreground: mutedText },
        ]),
        { text: ` ${this.status}`, foreground: mutedText },
      ]),
    ];
    this.surface.update(lines);
  }

  private chromeLine(parts: Array<{ text: string; foreground?: string; background?: string }>): UIFrameLine {
    const spans = parts.map((part) => ({
      text: part.text,
      style: {
        foreground: part.foreground ?? primaryText,
        background: part.background ?? chromeBackground,
        bold: true,
      },
    }));
    const used = parts.reduce((total, part) => total + part.text.length, 0);
    if (used < this.width) spans.push({ text: " ".repeat(this.width - used), style: { foreground: primaryText, background: chromeBackground, bold: true } });
    return { spans };
  }

  private canvasLine(row: Array<string | undefined>): UIFrameLine {
    const spans: Array<{ text: string; style: { background: string } }> = [];
    let current = row[0] ?? canvasBackground;
    let count = 0;
    for (const cell of row) {
      const color = cell ?? canvasBackground;
      if (color !== current) {
        spans.push({ text: " ".repeat(count), style: { background: current } });
        current = color;
        count = 0;
      }
      count++;
    }
    if (count > 0) spans.push({ text: " ".repeat(count), style: { background: current } });
    return { spans };
  }
}

function hexRGB(color: string, fallback: readonly [number, number, number] = [0, 0, 0]): readonly [number, number, number] {
  if (!/^#[0-9a-f]{6}$/i.test(color)) return fallback;
  return [Number.parseInt(color.slice(1, 3), 16), Number.parseInt(color.slice(3, 5), 16), Number.parseInt(color.slice(5, 7), 16)];
}

function encodePNG(width: number, height: number, scanlines: Buffer): Buffer {
  const header = Buffer.alloc(13);
  header.writeUInt32BE(width, 0);
  header.writeUInt32BE(height, 4);
  header[8] = 8;
  header[9] = 2;
  return Buffer.concat([
    Buffer.from([137, 80, 78, 71, 13, 10, 26, 10]),
    pngChunk("IHDR", header),
    pngChunk("IDAT", deflateSync(scanlines)),
    pngChunk("IEND", Buffer.alloc(0)),
  ]);
}

function pngChunk(type: string, data: Buffer): Buffer {
  const typeBytes = Buffer.from(type, "ascii");
  const chunk = Buffer.alloc(12 + data.length);
  chunk.writeUInt32BE(data.length, 0);
  typeBytes.copy(chunk, 4);
  data.copy(chunk, 8);
  chunk.writeUInt32BE(crc32(Buffer.concat([typeBytes, data])), 8 + data.length);
  return chunk;
}

function crc32(data: Buffer): number {
  let crc = 0xffffffff;
  for (const byte of data) {
    crc ^= byte;
    for (let bit = 0; bit < 8; bit++) crc = (crc >>> 1) ^ (crc & 1 ? 0xedb88320 : 0);
  }
  return (crc ^ 0xffffffff) >>> 0;
}

let activeBoard: DrawingBoard | undefined;

const extension = defineExtension((ext) => {
  ext.setMetadata({ name: "drawing-board", version: "0.1.0" });
  ext.registerCommand({
    name: "draw",
    aliases: ["drawing-board"],
    description: "Open an interactive terminal drawing board",
    async execute(_input, ctx) {
      if (activeBoard) {
        return { action: "respond", response: "The drawing board is already open. Press q or Esc inside it to close it first." };
      }
      const surface = await ctx.ui.openSurface({
        id: "drawing-board",
        initialLines: ["Opening drawing board…"],
        width: "90%",
        height: "85%",
        maxWidth: 120,
        anchor: "center",
        margin: { top: 1, right: 1, bottom: 1, left: 1 },
      });
      const board = new DrawingBoard(
        surface,
        ctx.cwd,
        async (filename) => {
          await ctx.ui.appendTranscript({
            title: "Drawing board saved",
            message: `./${filename}\nCopy the path above and paste it into chat to inspect it with view_image or look_at.`,
          });
        },
        (closed) => {
          if (activeBoard === closed) activeBoard = undefined;
        },
      );
      activeBoard = board;
      board.start();
      return { action: "respond", response: "" };
    },
  });
});

await runExtension(extension);
