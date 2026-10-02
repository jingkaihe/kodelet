import { useEffect, useRef, useState } from 'react';

const greeting = 'Hello! What would you like me to work on?';
const columns = 64;
const rows = 24;
const scale = 3.4;
const turnDuration = 6000;
const impulseDuration = 1500;
const brailleBits = [
  [1, 8],
  [2, 16],
  [4, 32],
  [64, 128],
];

type Particle = { x: number; y: number; z: number; edge: boolean; accent: boolean; seed: number };
type Impulse = { at: number; x: number; y: number };

function createParticles(): Particle[] {
  // Match the TUI sculpture: the favicon's four strokes, with a lighter,
  // separated underscore. Two faces keep the mark readable through a full turn.
  const strokes = [
    [16, 15, 16, 49, 6],
    [16, 37, 34, 20, 6],
    [22, 31, 36, 49, 6],
    [45, 49, 57, 49, 4],
  ];
  const particles: Particle[] = [];
  for (let y = 13.25; y < 52; y += 0.5) {
    for (let x = 12.25; x < 59; x += 0.5) {
      let distance = Number.POSITIVE_INFINITY;
      for (const [x1, y1, x2, y2, width] of strokes) {
        const dx = x2 - x1;
        const dy = y2 - y1;
        const length = Math.hypot(dx, dy);
        const along = ((x - x1) * dx + (y - y1) * dy) / length - length / 2;
        const across = ((x - x1) * dy - (y - y1) * dx) / length;
        distance = Math.min(
          distance,
          Math.max(Math.abs(along) - length / 2, Math.abs(across) - width / 2)
        );
      }
      if (distance > 0) continue;
      const seed = particles.length;
      for (const z of [-0.42, 0.42]) {
        particles.push({
          x: (x - 35) * 0.48,
          y: (y - 33) * 0.48,
          z,
          edge: distance > -0.9 && seed % 5 < 2,
          accent: x > 42,
          seed,
        });
      }
    }
  }
  return particles;
}

function renderParticles(particles: Particle[], elapsed: number, now: number, impulses: Impulse[]) {
  const phase = Math.min(1, elapsed / turnDuration);
  const turn = Math.min(1, Math.max(0, (phase - 0.08) / 0.84));
  const yaw = turn === 1 ? 0 : 2 * Math.PI * turn * turn * (3 - 2 * turn);
  const sin = Math.sin(yaw);
  const cos = Math.cos(yaw);
  const pitch = 0.09 * sin;
  const body = new Uint8Array(columns * rows);
  const accent = new Uint8Array(columns * rows);
  for (const p of particles) {
    let x = p.x * cos + p.z * sin;
    let y = p.y * Math.cos(pitch) + (p.x * sin - p.z * cos) * Math.sin(pitch);
    let drift = 0;
    if (p.edge) {
      const u = (phase - 0.12 - (p.seed % 30) / 100) / 0.4;
      drift = u > 0 && u < 1 ? Math.sin(Math.PI * u) ** 2 : 0;
      x += Math.sin(p.seed) * drift * 1.8;
      y += Math.cos(p.seed) * drift * 1.8;
      for (const impulse of impulses) {
        const age = (now - impulse.at) / 1000;
        const dx = x - impulse.x;
        const dy = y - impulse.y;
        const distance = Math.max(1, Math.hypot(dx, dy));
        const kick = (Math.sin(age * 9) * Math.exp(-age * 4) * 7) / (1 + distance / 14);
        x += (dx / distance) * kick;
        y += (dy / distance) * kick;
        drift += Math.abs(kick);
      }
    }
    const dotX = Math.floor(columns + x * scale);
    const dotY = Math.floor(rows * 2 + y * scale);
    if (dotX < 0 || dotX >= columns * 2 || dotY < 0 || dotY >= rows * 4) continue;
    const cell = Math.floor(dotY / 4) * columns + Math.floor(dotX / 2);
    const layer = p.accent || drift > 0.4 ? accent : body;
    layer[cell] |= brailleBits[dotY % 4][dotX % 2];
  }
  let bodyText = '';
  let accentText = '';
  for (let i = 0; i < body.length; i++) {
    // A warm stray dot must not paint an orange halo over the neutral body.
    bodyText += String.fromCharCode(0x2800 + (body[i] ? body[i] | accent[i] : 0));
    accentText += String.fromCharCode(0x2800 + (body[i] ? 0 : accent[i]));
    if ((i + 1) % columns === 0 && i + 1 < body.length) {
      bodyText += '\n';
      accentText += '\n';
    }
  }
  return [bodyText, accentText];
}

export default function ChatWelcome() {
  const [revealed, setRevealed] = useState(false);
  const bodyRef = useRef<HTMLSpanElement>(null);
  const accentRef = useRef<HTMLSpanElement>(null);
  const playRef = useRef<(x: number, y: number) => void>(() => {});

  useEffect(() => {
    if (!revealed) return;
    const particles = createParticles();
    const motion = window.matchMedia('(prefers-reduced-motion: reduce)');
    let frame = 0;
    let started = performance.now();
    let elapsed = 0;
    let impulses: Impulse[] = [];

    const paint = (now: number) => {
      elapsed = motion.matches ? turnDuration : Math.min(turnDuration, now - started);
      impulses = impulses.filter((impulse) => now - impulse.at < impulseDuration);
      const [body, accent] = renderParticles(particles, elapsed, now, impulses);
      if (bodyRef.current) bodyRef.current.textContent = body;
      if (accentRef.current) accentRef.current.textContent = accent;
      frame =
        !motion.matches && (elapsed < turnDuration || impulses.length > 0)
          ? requestAnimationFrame(paint)
          : 0;
    };
    const settle = () => {
      cancelAnimationFrame(frame);
      started = performance.now() - turnDuration;
      impulses = [];
      paint(performance.now());
    };
    playRef.current = (x, y) => {
      if (motion.matches) return;
      const now = performance.now();
      if (elapsed >= turnDuration) started = now;
      impulses.push({ at: now, x, y });
      // Keep one clock: repeated clicks add a ripple, not a second animation loop.
      if (!frame) frame = requestAnimationFrame(paint);
    };
    paint(started);
    motion.addEventListener('change', settle);
    return () => {
      cancelAnimationFrame(frame);
      motion.removeEventListener('change', settle);
      playRef.current = () => {};
    };
  }, [revealed]);

  return (
    <div className="chat-empty-state">
      <h1 className="chat-empty-state-title">
        <button
          type="button"
          className={`chat-welcome-button${revealed ? ' chat-welcome-sculpture' : ''}`}
          aria-label={revealed ? 'Spin the kodelet particle logo' : undefined}
          title={revealed ? 'Click to scatter and spin' : undefined}
          onClick={(event) => {
            if (!revealed) {
              setRevealed(true);
              return;
            }
            const bounds = bodyRef.current?.getBoundingClientRect();
            if (!bounds) return;
            const x = event.detail ? (event.clientX - bounds.left) / bounds.width - 0.5 : 0;
            const y = event.detail ? (event.clientY - bounds.top) / bounds.height - 0.5 : 0;
            playRef.current((x * columns * 2) / scale, (y * rows * 4) / scale);
          }}
        >
          <span className={revealed ? 'sr-only' : undefined}>{greeting}</span>
          {revealed ? (
            <span className="chat-welcome-particles" aria-hidden="true">
              <span ref={bodyRef} />
              <span ref={accentRef} className="chat-welcome-accent" />
            </span>
          ) : null}
        </button>
      </h1>
    </div>
  );
}
