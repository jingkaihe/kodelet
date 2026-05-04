import React, { useEffect, useMemo, useRef, useState } from 'react';

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

const GAME_OPTIONS = ['pong', 'tetris', 'flappy'] as const;
type GameId = (typeof GAME_OPTIONS)[number];

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

interface FlappyState {
  birdY: number;
  velocity: number;
  pipes: Array<{ x: number; gapY: number; scored: boolean }>;
  score: number;
  best: number;
  lastTime: number | null;
  gameOver: boolean;
}

interface TetrisPiece {
  shape: number[][];
  x: number;
  y: number;
  color: string;
}

interface TetrisState {
  board: Array<Array<string | null>>;
  piece: TetrisPiece;
  nextPiece: TetrisPiece;
  dropTimer: number;
  score: number;
  lines: number;
  lastTime: number | null;
  gameOver: boolean;
}

const TETRIS_COLORS = ['#788c5d', '#6a9bcc', '#d97757', '#b0aea5', '#8d8578', '#537aa5', '#a36f52'];
const TETRIS_SHAPES = [
  [[1, 1, 1, 1]],
  [[1, 1], [1, 1]],
  [[0, 1, 0], [1, 1, 1]],
  [[1, 0, 0], [1, 1, 1]],
  [[0, 0, 1], [1, 1, 1]],
  [[0, 1, 1], [1, 1, 0]],
  [[1, 1, 0], [0, 1, 1]],
];

const normalizeKey = (key: string): string => key.toLowerCase();
const clamp = (value: number, min: number, max: number): number => Math.min(max, Math.max(min, value));

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

const createPongState = (): PongState => ({
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

const resetPongBall = (state: PongState, direction: 1 | -1): PongState => ({
  ...state,
  ballX: 0.5,
  ballY: 0.5,
  ballVX: 0.34 * direction,
  ballVY: (Math.random() > 0.5 ? 1 : -1) * (0.15 + Math.random() * 0.12),
});

const createFlappyState = (): FlappyState => ({
  birdY: 0.45,
  velocity: 0,
  pipes: [{ x: 1.08, gapY: 0.48, scored: false }],
  score: 0,
  best: 0,
  lastTime: null,
  gameOver: false,
});

const createEmptyTetrisBoard = (): Array<Array<string | null>> => Array.from({ length: 20 }, () => Array.from({ length: 10 }, () => null));

const cloneShape = (shape: number[][]): number[][] => shape.map((row) => [...row]);

const createTetrisPiece = (): TetrisPiece => {
  const index = Math.floor(Math.random() * TETRIS_SHAPES.length);
  const shape = cloneShape(TETRIS_SHAPES[index]);
  return {
    shape,
    x: Math.floor((10 - shape[0].length) / 2),
    y: 0,
    color: TETRIS_COLORS[index],
  };
};

const createTetrisState = (): TetrisState => ({
  board: createEmptyTetrisBoard(),
  piece: createTetrisPiece(),
  nextPiece: createTetrisPiece(),
  dropTimer: 0,
  score: 0,
  lines: 0,
  lastTime: null,
  gameOver: false,
});

const rotateShape = (shape: number[][]): number[][] => shape[0].map((_, column) => shape.map((row) => row[column]).reverse());

const collides = (board: Array<Array<string | null>>, piece: TetrisPiece): boolean => {
  for (let row = 0; row < piece.shape.length; row += 1) {
    for (let column = 0; column < piece.shape[row].length; column += 1) {
      if (!piece.shape[row][column]) {
        continue;
      }

      const boardX = piece.x + column;
      const boardY = piece.y + row;
      if (boardX < 0 || boardX >= 10 || boardY >= 20) {
        return true;
      }
      if (boardY >= 0 && board[boardY][boardX]) {
        return true;
      }
    }
  }
  return false;
};

const lockTetrisPiece = (state: TetrisState): TetrisState => {
  const board = state.board.map((row) => [...row]);
  state.piece.shape.forEach((row, rowIndex) => {
    row.forEach((cell, columnIndex) => {
      if (!cell) {
        return;
      }
      const y = state.piece.y + rowIndex;
      const x = state.piece.x + columnIndex;
      if (y >= 0 && y < 20 && x >= 0 && x < 10) {
        board[y][x] = state.piece.color;
      }
    });
  });

  const remainingRows = board.filter((row) => row.some((cell) => !cell));
  const clearedLines = 20 - remainingRows.length;
  const clearedRows = Array.from({ length: clearedLines }, () => Array.from({ length: 10 }, () => null));
  const nextPiece = { ...state.nextPiece, x: Math.floor((10 - state.nextPiece.shape[0].length) / 2), y: 0 };
  const nextState: TetrisState = {
    ...state,
    board: [...clearedRows, ...remainingRows],
    piece: nextPiece,
    nextPiece: createTetrisPiece(),
    dropTimer: 0,
    score: state.score + [0, 100, 300, 500, 800][clearedLines],
    lines: state.lines + clearedLines,
  };

  if (collides(nextState.board, nextState.piece)) {
    nextState.gameOver = true;
  }

  return nextState;
};

const moveTetrisPiece = (state: TetrisState, dx: number, dy: number): TetrisState => {
  if (state.gameOver) {
    return state;
  }

  const movedPiece = { ...state.piece, x: state.piece.x + dx, y: state.piece.y + dy };
  if (!collides(state.board, movedPiece)) {
    return { ...state, piece: movedPiece };
  }

  if (dy > 0) {
    return lockTetrisPiece(state);
  }

  return state;
};

const rotateTetrisPiece = (state: TetrisState): TetrisState => {
  if (state.gameOver) {
    return state;
  }

  const rotatedPiece = { ...state.piece, shape: rotateShape(state.piece.shape) };
  for (const offset of [0, -1, 1, -2, 2]) {
    const kickedPiece = { ...rotatedPiece, x: rotatedPiece.x + offset };
    if (!collides(state.board, kickedPiece)) {
      return { ...state, piece: kickedPiece };
    }
  }
  return state;
};

const hardDropTetrisPiece = (state: TetrisState): TetrisState => {
  if (state.gameOver) {
    return createTetrisState();
  }

  let droppedPiece = state.piece;
  while (!collides(state.board, { ...droppedPiece, y: droppedPiece.y + 1 })) {
    droppedPiece = { ...droppedPiece, y: droppedPiece.y + 1 };
  }
  return lockTetrisPiece({ ...state, piece: droppedPiece, score: state.score + 2 });
};

const gameLabels: Record<GameId, string> = {
  pong: 'Pong',
  tetris: 'Tetris',
  flappy: 'Flappy Bird',
};

const gameHints: Record<GameId, string> = {
  pong: 'W/S or Up/Down. Mouse also works.',
  tetris: 'Arrow keys move. Up rotates. Space drops.',
  flappy: 'Space, Up, or click to flap.',
};

const KonamiGamesEgg: React.FC = () => {
  const [open, setOpen] = useState(false);
  const [selectedGame, setSelectedGame] = useState<GameId | null>(null);
  const canvasRef = useRef<HTMLCanvasElement | null>(null);
  const sequenceIndexRef = useRef(0);
  const pongRef = useRef<PongState>(createPongState());
  const flappyRef = useRef<FlappyState>(createFlappyState());
  const tetrisRef = useRef<TetrisState>(createTetrisState());
  const pointerYRef = useRef<number | null>(null);
  const pressedKeysRef = useRef(new Set<string>());

  const startGame = (game: GameId) => {
    pongRef.current = createPongState();
    flappyRef.current = { ...createFlappyState(), best: flappyRef.current.best };
    tetrisRef.current = createTetrisState();
    pointerYRef.current = null;
    pressedKeysRef.current.clear();
    setSelectedGame(game);
  };

  const close = () => {
    setOpen(false);
    setSelectedGame(null);
  };

  const flap = () => {
    const state = flappyRef.current;
    if (state.gameOver) {
      flappyRef.current = { ...createFlappyState(), best: state.best };
      return;
    }
    state.velocity = -0.54;
  };

  useEffect(() => {
    const handleKeyDown = (event: KeyboardEvent) => {
      if (open) {
        if (event.key === 'Escape') {
          close();
          return;
        }

        if (!selectedGame) {
          const gameNumber = Number(event.key);
          if (gameNumber >= 1 && gameNumber <= GAME_OPTIONS.length) {
            event.preventDefault();
            startGame(GAME_OPTIONS[gameNumber - 1]);
          }
          return;
        }

        if (selectedGame === 'pong' && ['ArrowUp', 'ArrowDown', 'w', 'W', 's', 'S'].includes(event.key)) {
          event.preventDefault();
          pressedKeysRef.current.add(event.key.toLowerCase());
          return;
        }

        if (selectedGame === 'flappy' && [' ', 'ArrowUp', 'w', 'W'].includes(event.key)) {
          event.preventDefault();
          flap();
          return;
        }

        if (selectedGame === 'tetris') {
          if (event.key === 'ArrowLeft') {
            event.preventDefault();
            tetrisRef.current = moveTetrisPiece(tetrisRef.current, -1, 0);
          } else if (event.key === 'ArrowRight') {
            event.preventDefault();
            tetrisRef.current = moveTetrisPiece(tetrisRef.current, 1, 0);
          } else if (event.key === 'ArrowDown') {
            event.preventDefault();
            tetrisRef.current = moveTetrisPiece({ ...tetrisRef.current, score: tetrisRef.current.score + 1 }, 0, 1);
          } else if (event.key === 'ArrowUp') {
            event.preventDefault();
            tetrisRef.current = rotateTetrisPiece(tetrisRef.current);
          } else if (event.key === ' ') {
            event.preventDefault();
            tetrisRef.current = hardDropTetrisPiece(tetrisRef.current);
          }
          return;
        }
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
          setSelectedGame(null);
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
  }, [open, selectedGame]);

  useEffect(() => {
    if (!open || !selectedGame) {
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

    const handlePointerDown = () => {
      if (selectedGame === 'flappy') {
        flap();
      }
    };

    canvas.addEventListener('pointermove', handlePointerMove);
    canvas.addEventListener('pointerleave', handlePointerLeave);
    canvas.addEventListener('pointerdown', handlePointerDown);

    const drawBackdrop = (width: number, height: number) => {
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
    };

    const drawPong = (time: number, width: number, height: number) => {
      const state = pongRef.current;
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
        Object.assign(state, resetPongBall(state, 1));
      } else if (state.ballX > 1.04) {
        state.playerScore += 1;
        Object.assign(state, resetPongBall(state, -1));
      }

      context.save();
      context.strokeStyle = 'rgba(120, 140, 93, 0.28)';
      context.setLineDash([8, 10]);
      context.lineWidth = 1.5;
      context.beginPath();
      context.moveTo(width / 2, 18);
      context.lineTo(width / 2, height - 18);
      context.stroke();
      context.restore();

      context.font = '700 13px "IBM Plex Sans", "Helvetica Neue", sans-serif';
      context.fillStyle = 'rgba(20, 20, 19, 0.46)';
      context.textAlign = 'left';
      context.fillText('YOU', 22, 30);
      context.textAlign = 'right';
      context.fillText('KODELET', width - 22, 30);

      context.font = '600 42px "IBM Plex Sans", "Helvetica Neue", sans-serif';
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
    };

    const drawFlappy = (time: number, width: number, height: number) => {
      const state = flappyRef.current;
      const deltaSeconds = state.lastTime === null ? 0 : Math.min(0.032, (time - state.lastTime) / 1000);
      state.lastTime = time;
      const birdX = width * 0.28;
      const birdRadius = Math.max(11, Math.min(16, width * 0.023));
      const pipeWidth = Math.max(46, width * 0.08);
      const gapHeight = Math.max(96, height * 0.31);
      const birdY = state.birdY * height;

      if (!state.gameOver) {
        state.velocity += 1.28 * deltaSeconds;
        state.birdY += state.velocity * deltaSeconds;
        state.pipes = state.pipes.map((pipe) => ({ ...pipe, x: pipe.x - deltaSeconds * 0.34 }));

        const lastPipe = state.pipes[state.pipes.length - 1];
        if (lastPipe.x < 0.62) {
          state.pipes.push({ x: 1.08, gapY: 0.24 + Math.random() * 0.48, scored: false });
        }
        state.pipes = state.pipes.filter((pipe) => pipe.x > -0.18);

        state.pipes.forEach((pipe) => {
          const pipeX = pipe.x * width;
          const gapCenter = pipe.gapY * height;
          const gapTop = gapCenter - gapHeight / 2;
          const gapBottom = gapCenter + gapHeight / 2;
          const overlapsX = birdX + birdRadius > pipeX && birdX - birdRadius < pipeX + pipeWidth;
          const overlapsY = birdY - birdRadius < gapTop || birdY + birdRadius > gapBottom;
          if (overlapsX && overlapsY) {
            state.gameOver = true;
          }
          if (!pipe.scored && pipeX + pipeWidth < birdX) {
            pipe.scored = true;
            state.score += 1;
            state.best = Math.max(state.best, state.score);
          }
        });

        if (state.birdY < birdRadius / height || state.birdY > 1 - birdRadius / height) {
          state.gameOver = true;
        }
      }

      state.pipes.forEach((pipe) => {
        const pipeX = pipe.x * width;
        const gapCenter = pipe.gapY * height;
        const gapTop = gapCenter - gapHeight / 2;
        const gapBottom = gapCenter + gapHeight / 2;
        context.fillStyle = '#788c5d';
        context.fillRect(pipeX, 0, pipeWidth, gapTop);
        context.fillRect(pipeX, gapBottom, pipeWidth, height - gapBottom);
        context.fillStyle = 'rgba(255, 255, 255, 0.2)';
        context.fillRect(pipeX + 7, 0, 3, gapTop);
        context.fillRect(pipeX + 7, gapBottom, 3, height - gapBottom);
      });

      const tilt = clamp(state.velocity * 0.9, -0.55, 0.75);
      const wingLift = Math.sin(time / 90) * 0.55 - clamp(state.velocity, -0.45, 0.45);
      context.save();
      context.translate(birdX, birdY);
      context.rotate(tilt);
      context.shadowBlur = 12;
      context.shadowColor = 'rgba(217, 119, 87, 0.18)';

      context.fillStyle = '#d97757';
      context.beginPath();
      context.ellipse(0, 0, birdRadius * 1.25, birdRadius * 0.86, 0, 0, Math.PI * 2);
      context.fill();

      context.fillStyle = '#c96647';
      context.beginPath();
      context.moveTo(-birdRadius * 0.88, -birdRadius * 0.16);
      context.lineTo(-birdRadius * 1.48, -birdRadius * 0.52);
      context.lineTo(-birdRadius * 1.24, birdRadius * 0.18);
      context.closePath();
      context.fill();

      context.fillStyle = '#f0b06f';
      context.beginPath();
      context.moveTo(birdRadius * 1.08, -birdRadius * 0.12);
      context.lineTo(birdRadius * 1.72, birdRadius * 0.08);
      context.lineTo(birdRadius * 1.08, birdRadius * 0.34);
      context.closePath();
      context.fill();

      context.save();
      context.rotate(wingLift * 0.42);
      context.fillStyle = '#e8c8a0';
      context.beginPath();
      context.ellipse(-birdRadius * 0.18, birdRadius * 0.08, birdRadius * 0.72, birdRadius * 0.32, -0.28, 0, Math.PI * 2);
      context.fill();
      context.restore();

      context.fillStyle = '#faf9f5';
      context.beginPath();
      context.arc(birdRadius * 0.42, -birdRadius * 0.32, birdRadius * 0.24, 0, Math.PI * 2);
      context.fill();
      context.fillStyle = '#141413';
      context.beginPath();
      context.arc(birdRadius * 0.5, -birdRadius * 0.3, birdRadius * 0.09, 0, Math.PI * 2);
      context.fill();
      context.restore();
      context.shadowBlur = 0;

      context.font = '700 14px "IBM Plex Sans", "Helvetica Neue", sans-serif';
      context.fillStyle = 'rgba(20, 20, 19, 0.54)';
      context.textAlign = 'left';
      context.fillText(`score ${state.score}`, 22, 30);
      context.textAlign = 'right';
      context.fillText(`best ${state.best}`, width - 22, 30);

      if (state.gameOver) {
        context.font = '700 24px "IBM Plex Sans", "Helvetica Neue", sans-serif';
        context.fillStyle = '#4d473f';
        context.textAlign = 'center';
        context.fillText('Press Space to try again', width / 2, height / 2);
      }
    };

    const drawTetris = (time: number, width: number, height: number) => {
      const state = tetrisRef.current;
      const deltaMs = state.lastTime === null ? 0 : Math.min(48, time - state.lastTime);
      state.lastTime = time;

      if (!state.gameOver) {
        state.dropTimer += deltaMs;
        const interval = Math.max(130, 620 - state.lines * 14);
        if (state.dropTimer >= interval) {
          state.dropTimer = 0;
          tetrisRef.current = moveTetrisPiece(state, 0, 1);
        }
      }

      const currentState = tetrisRef.current;
      const boardWidth = Math.min(width * 0.52, height * 0.42);
      const cell = Math.floor(boardWidth / 10);
      const boardPixelWidth = cell * 10;
      const boardPixelHeight = cell * 20;
      const boardX = Math.max(20, width * 0.18);
      const boardY = Math.max(18, (height - boardPixelHeight) / 2);

      context.fillStyle = 'rgba(250, 249, 245, 0.72)';
      context.fillRect(boardX - 8, boardY - 8, boardPixelWidth + 16, boardPixelHeight + 16);
      context.strokeStyle = 'rgba(20, 20, 19, 0.12)';
      context.strokeRect(boardX - 8, boardY - 8, boardPixelWidth + 16, boardPixelHeight + 16);

      const drawCell = (x: number, y: number, color: string) => {
        context.fillStyle = color;
        context.fillRect(boardX + x * cell + 1, boardY + y * cell + 1, cell - 2, cell - 2);
        context.fillStyle = 'rgba(255, 255, 255, 0.22)';
        context.fillRect(boardX + x * cell + 3, boardY + y * cell + 3, cell - 6, Math.max(2, cell * 0.16));
      };

      currentState.board.forEach((row, y) => {
        row.forEach((color, x) => {
          context.strokeStyle = 'rgba(20, 20, 19, 0.04)';
          context.strokeRect(boardX + x * cell, boardY + y * cell, cell, cell);
          if (color) {
            drawCell(x, y, color);
          }
        });
      });

      currentState.piece.shape.forEach((row, rowIndex) => {
        row.forEach((filled, columnIndex) => {
          if (filled) {
            drawCell(currentState.piece.x + columnIndex, currentState.piece.y + rowIndex, currentState.piece.color);
          }
        });
      });

      context.font = '700 18px "IBM Plex Sans", "Helvetica Neue", sans-serif';
      context.fillStyle = '#4d473f';
      context.textAlign = 'left';
      const sideX = boardX + boardPixelWidth + 34;
      context.fillText('Tetris', sideX, boardY + 6);
      context.font = '600 13px "IBM Plex Sans", "Helvetica Neue", sans-serif';
      context.fillStyle = 'rgba(20, 20, 19, 0.58)';
      context.fillText(`Score ${currentState.score}`, sideX, boardY + 40);
      context.fillText(`Lines ${currentState.lines}`, sideX, boardY + 64);
      context.fillText('Next', sideX, boardY + 106);

      currentState.nextPiece.shape.forEach((row, rowIndex) => {
        row.forEach((filled, columnIndex) => {
          if (!filled) {
            return;
          }
          context.fillStyle = currentState.nextPiece.color;
          context.fillRect(sideX + columnIndex * cell * 0.7, boardY + 124 + rowIndex * cell * 0.7, cell * 0.62, cell * 0.62);
        });
      });

      if (currentState.gameOver) {
        context.font = '700 21px "IBM Plex Sans", "Helvetica Neue", sans-serif';
        context.fillStyle = '#d97757';
        context.fillText('Game over', sideX, boardY + 202);
        context.font = '600 12px "IBM Plex Sans", "Helvetica Neue", sans-serif';
        context.fillStyle = 'rgba(20, 20, 19, 0.58)';
        context.fillText('Space restarts', sideX, boardY + 226);
      }
    };

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
      drawBackdrop(width, height);

      if (selectedGame === 'pong') {
        drawPong(time, width, height);
      } else if (selectedGame === 'flappy') {
        drawFlappy(time, width, height);
      } else {
        drawTetris(time, width, height);
      }

      context.font = '500 12px "Monaco", "Menlo", "Ubuntu Mono", monospace';
      context.fillStyle = 'rgba(20, 20, 19, 0.42)';
      context.textAlign = 'center';
      context.fillText(gameHints[selectedGame], width / 2, height - 18);

      animationFrame = requestAnimationFrame(render);
    };

    animationFrame = requestAnimationFrame(render);
    return () => {
      cancelAnimationFrame(animationFrame);
      canvas.removeEventListener('pointermove', handlePointerMove);
      canvas.removeEventListener('pointerleave', handlePointerLeave);
      canvas.removeEventListener('pointerdown', handlePointerDown);
    };
  }, [open, selectedGame]);

  const selectedLabel = useMemo(() => selectedGame ? gameLabels[selectedGame] : null, [selectedGame]);

  if (!open) {
    return null;
  }

  return (
    <div className="konami-egg-backdrop" data-testid="konami-egg-backdrop" onClick={close}>
      <div
        aria-label="Kodelet games"
        aria-modal="true"
        className="konami-egg-modal surface-panel"
        data-testid="konami-egg-modal"
        onClick={(event) => event.stopPropagation()}
        role="dialog"
      >
        <div className="konami-egg-header">
          <div className="konami-game-title-block">
            <p className="konami-game-kicker">Select game</p>
            <p className="konami-game-title">{selectedLabel || 'Hidden arcade'}</p>
          </div>
          <div className="konami-game-header-actions">
            {selectedGame ? (
              <button className="composer-capsule" onClick={() => setSelectedGame(null)} type="button">
                Games
              </button>
            ) : null}
            <button className="composer-capsule konami-egg-close" onClick={close} type="button">
              Close
            </button>
          </div>
        </div>

        {selectedGame ? (
          <canvas className="konami-egg-canvas" data-testid="konami-egg-canvas" ref={canvasRef} />
        ) : (
          <div className="konami-game-picker" data-testid="konami-game-picker">
            {GAME_OPTIONS.map((game) => (
              <button className="konami-game-card" key={game} onClick={() => startGame(game)} type="button">
                <span className="konami-game-name">{gameLabels[game]}</span>
                <span className="konami-game-copy">{gameHints[game]}</span>
              </button>
            ))}
          </div>
        )}

        <p className="konami-egg-hint">Esc or click outside to close.</p>
      </div>
    </div>
  );
};

export default KonamiGamesEgg;
