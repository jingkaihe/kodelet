import React, { useEffect, useRef, useState } from 'react';

const KONAMI_SEQUENCE = [
  'ArrowUp',
  'ArrowUp',
  'ArrowDown',
  'ArrowDown',
  'ArrowLeft',
  'ArrowRight',
  'ArrowLeft',
  'ArrowRight',
  'b',
  'a',
];

interface PongState {
  ballX: number;
  ballY: number;
  ballVX: number;
  ballVY: number;
  playerY: number;
  kodeletY: number;
  playerScore: number;
  kodeletScore: number;
  lastTime: number | null;
}

const normalizeKey = (key: string): string => key.toLowerCase();

const shouldIgnoreKonamiEvent = (event: KeyboardEvent): boolean => {
  if (event.repeat || event.metaKey || event.ctrlKey || event.altKey) {
    return true;
  }

  const target = event.target as HTMLElement | null;
  return Boolean(target && (target.isContentEditable || target.tagName === 'SELECT'));
};

const isTextEntryTarget = (event: KeyboardEvent): boolean => {
  const target = event.target as HTMLElement | null;
  return Boolean(target && (target.tagName === 'INPUT' || target.tagName === 'TEXTAREA'));
};

const createInitialState = (): PongState => ({
  ballX: 0.5,
  ballY: 0.5,
  ballVX: 0.34,
  ballVY: -0.18,
  playerY: 0.5,
  kodeletY: 0.5,
  playerScore: 0,
  kodeletScore: 0,
  lastTime: null,
});

const clamp = (value: number, min: number, max: number): number => Math.min(max, Math.max(min, value));

const resetBall = (state: PongState, direction: 1 | -1): PongState => ({
  ...state,
  ballX: 0.5,
  ballY: 0.5,
  ballVX: 0.34 * direction,
  ballVY: (Math.random() > 0.5 ? 1 : -1) * (0.15 + Math.random() * 0.12),
});

const KonamiPongEgg: React.FC = () => {
  const [open, setOpen] = useState(false);
  const canvasRef = useRef<HTMLCanvasElement | null>(null);
  const sequenceIndexRef = useRef(0);
  const stateRef = useRef<PongState>(createInitialState());
  const pointerYRef = useRef<number | null>(null);
  const pressedKeysRef = useRef(new Set<string>());

  useEffect(() => {
    const handleKeyDown = (event: KeyboardEvent) => {
      if (open) {
        if (event.key === 'Escape') {
          setOpen(false);
          return;
        }

        if (['ArrowUp', 'ArrowDown', 'w', 'W', 's', 'S'].includes(event.key)) {
          event.preventDefault();
          pressedKeysRef.current.add(event.key.toLowerCase());
        }
        return;
      }

      if (shouldIgnoreKonamiEvent(event)) {
        sequenceIndexRef.current = 0;
        return;
      }

      const expectedKey = KONAMI_SEQUENCE[sequenceIndexRef.current];
      const pressedKey = normalizeKey(event.key);

      if (pressedKey === normalizeKey(expectedKey)) {
        sequenceIndexRef.current += 1;

        if (isTextEntryTarget(event) && (pressedKey === 'b' || pressedKey === 'a')) {
          event.preventDefault();
        }

        if (sequenceIndexRef.current === KONAMI_SEQUENCE.length) {
          sequenceIndexRef.current = 0;
          event.preventDefault();
          stateRef.current = createInitialState();
          setOpen(true);
        }
        return;
      }

      sequenceIndexRef.current = pressedKey === normalizeKey(KONAMI_SEQUENCE[0]) ? 1 : 0;
    };

    const handleKeyUp = (event: KeyboardEvent) => {
      pressedKeysRef.current.delete(event.key.toLowerCase());
    };

    window.addEventListener('keydown', handleKeyDown);
    window.addEventListener('keyup', handleKeyUp);
    return () => {
      window.removeEventListener('keydown', handleKeyDown);
      window.removeEventListener('keyup', handleKeyUp);
    };
  }, [open]);

  useEffect(() => {
    if (!open) {
      pointerYRef.current = null;
      pressedKeysRef.current.clear();
      return undefined;
    }

    const canvas = canvasRef.current;
    const context = canvas?.getContext('2d');
    if (!canvas || !context) {
      return undefined;
    }

    let animationFrame = 0;
    const dpr = window.devicePixelRatio || 1;

    const handlePointerMove = (event: PointerEvent) => {
      const bounds = canvas.getBoundingClientRect();
      pointerYRef.current = clamp((event.clientY - bounds.top) / bounds.height, 0, 1);
    };

    const handlePointerLeave = () => {
      pointerYRef.current = null;
    };

    canvas.addEventListener('pointermove', handlePointerMove);
    canvas.addEventListener('pointerleave', handlePointerLeave);

    const render = (time: number) => {
      const bounds = canvas.getBoundingClientRect();
      const width = Math.max(320, bounds.width);
      const height = Math.max(220, bounds.height);

      if (canvas.width !== Math.floor(width * dpr) || canvas.height !== Math.floor(height * dpr)) {
        canvas.width = Math.floor(width * dpr);
        canvas.height = Math.floor(height * dpr);
      }

      context.setTransform(dpr, 0, 0, dpr, 0, 0);
      context.clearRect(0, 0, width, height);

      const state = stateRef.current;
      const deltaSeconds = state.lastTime === null ? 0 : Math.min(0.032, (time - state.lastTime) / 1000);
      state.lastTime = time;

      const paddleHeight = Math.min(0.3, Math.max(0.18, 78 / height));
      const paddleWidth = Math.max(8, width * 0.015);
      const ballRadius = Math.max(5, Math.min(8, width * 0.012));
      const playerX = 0.06;
      const kodeletX = 0.94;
      const keys = pressedKeysRef.current;

      if (pointerYRef.current !== null) {
        state.playerY = pointerYRef.current;
      } else {
        const keyboardDirection = (keys.has('arrowdown') || keys.has('s') ? 1 : 0) - (keys.has('arrowup') || keys.has('w') ? 1 : 0);
        state.playerY = clamp(state.playerY + keyboardDirection * deltaSeconds * 0.9, paddleHeight / 2, 1 - paddleHeight / 2);
      }

      const aiTarget = state.ballY + Math.sin(time / 520) * 0.035;
      state.kodeletY = clamp(state.kodeletY + clamp(aiTarget - state.kodeletY, -deltaSeconds * 0.62, deltaSeconds * 0.62), paddleHeight / 2, 1 - paddleHeight / 2);

      state.ballX += state.ballVX * deltaSeconds;
      state.ballY += state.ballVY * deltaSeconds;

      if (state.ballY < ballRadius / height || state.ballY > 1 - ballRadius / height) {
        state.ballY = clamp(state.ballY, ballRadius / height, 1 - ballRadius / height);
        state.ballVY *= -1;
      }

      const ballLeft = state.ballX - ballRadius / width;
      const ballRight = state.ballX + ballRadius / width;
      const ballTop = state.ballY - ballRadius / height;
      const ballBottom = state.ballY + ballRadius / height;
      const playerTop = state.playerY - paddleHeight / 2;
      const playerBottom = state.playerY + paddleHeight / 2;
      const kodeletTop = state.kodeletY - paddleHeight / 2;
      const kodeletBottom = state.kodeletY + paddleHeight / 2;

      if (state.ballVX < 0 && ballLeft <= playerX + paddleWidth / width && ballBottom >= playerTop && ballTop <= playerBottom) {
        const impact = clamp((state.ballY - state.playerY) / (paddleHeight / 2), -1, 1);
        state.ballX = playerX + paddleWidth / width + ballRadius / width;
        state.ballVX = Math.abs(state.ballVX) * 1.035;
        state.ballVY = impact * 0.44;
      }

      if (state.ballVX > 0 && ballRight >= kodeletX - paddleWidth / width && ballBottom >= kodeletTop && ballTop <= kodeletBottom) {
        const impact = clamp((state.ballY - state.kodeletY) / (paddleHeight / 2), -1, 1);
        state.ballX = kodeletX - paddleWidth / width - ballRadius / width;
        state.ballVX = -Math.abs(state.ballVX) * 1.035;
        state.ballVY = impact * 0.42;
      }

      if (state.ballX < -0.04) {
        state.kodeletScore += 1;
        Object.assign(state, resetBall(state, 1));
      } else if (state.ballX > 1.04) {
        state.playerScore += 1;
        Object.assign(state, resetBall(state, -1));
      }

      const gradient = context.createLinearGradient(0, 0, width, height);
      gradient.addColorStop(0, '#fbf8ef');
      gradient.addColorStop(0.58, '#f3ede2');
      gradient.addColorStop(1, '#e8e0d1');
      context.fillStyle = gradient;
      context.fillRect(0, 0, width, height);

      context.save();
      context.strokeStyle = 'rgba(93, 83, 64, 0.08)';
      context.lineWidth = 1;
      for (let y = 18; y < height; y += 18) {
        context.beginPath();
        context.moveTo(0, y + 0.5);
        context.lineTo(width, y + 0.5);
        context.stroke();
      }
      context.restore();

      context.save();
      context.strokeStyle = 'rgba(120, 140, 93, 0.28)';
      context.setLineDash([8, 10]);
      context.lineWidth = 1.5;
      context.beginPath();
      context.moveTo(width / 2, 18);
      context.lineTo(width / 2, height - 18);
      context.stroke();
      context.restore();

      context.font = '700 13px "Poppins", "Helvetica Neue", sans-serif';
      context.fillStyle = 'rgba(20, 20, 19, 0.46)';
      context.textAlign = 'left';
      context.fillText('YOU', 22, 30);
      context.textAlign = 'right';
      context.fillText('KODELET', width - 22, 30);

      context.font = '600 42px "Poppins", "Helvetica Neue", sans-serif';
      context.fillStyle = 'rgba(20, 20, 19, 0.13)';
      context.textAlign = 'center';
      context.fillText(`${state.playerScore}  ${state.kodeletScore}`, width / 2, 58);

      context.fillStyle = '#788c5d';
      context.shadowBlur = 16;
      context.shadowColor = 'rgba(120, 140, 93, 0.24)';
      context.fillRect(playerX * width, (state.playerY - paddleHeight / 2) * height, paddleWidth, paddleHeight * height);
      context.fillStyle = '#6a9bcc';
      context.shadowColor = 'rgba(106, 155, 204, 0.24)';
      context.fillRect(kodeletX * width - paddleWidth, (state.kodeletY - paddleHeight / 2) * height, paddleWidth, paddleHeight * height);

      context.beginPath();
      context.fillStyle = '#d97757';
      context.shadowColor = 'rgba(217, 119, 87, 0.28)';
      context.arc(state.ballX * width, state.ballY * height, ballRadius, 0, Math.PI * 2);
      context.fill();
      context.shadowBlur = 0;

      context.font = '500 12px "Monaco", "Menlo", "Ubuntu Mono", monospace';
      context.fillStyle = 'rgba(20, 20, 19, 0.42)';
      context.textAlign = 'center';
      context.fillText('W/S or ↑/↓ · mouse also works', width / 2, height - 18);

      animationFrame = requestAnimationFrame(render);
    };

    animationFrame = requestAnimationFrame(render);
    return () => {
      cancelAnimationFrame(animationFrame);
      canvas.removeEventListener('pointermove', handlePointerMove);
      canvas.removeEventListener('pointerleave', handlePointerLeave);
    };
  }, [open]);

  if (!open) {
    return null;
  }

  return (
    <div className="konami-egg-backdrop" data-testid="konami-egg-backdrop" onClick={() => setOpen(false)}>
      <div
        aria-label="Kodelet Pong"
        aria-modal="true"
        className="konami-egg-modal surface-panel"
        data-testid="konami-egg-modal"
        onClick={(event) => event.stopPropagation()}
        role="dialog"
      >
        <div className="konami-egg-header konami-egg-header-compact">
          <button className="composer-capsule konami-egg-close" onClick={() => setOpen(false)} type="button">
            Close
          </button>
        </div>
        <canvas className="konami-egg-canvas" data-testid="konami-egg-canvas" ref={canvasRef} />
        <p className="konami-egg-hint">Esc or click outside to close.</p>
      </div>
    </div>
  );
};

export default KonamiPongEgg;
