import React from 'react';

export const GAME_OPTIONS = ['pong', 'tetris', 'flappy'] as const;
export type GameId = (typeof GAME_OPTIONS)[number];

export const gameLabels: Record<GameId, string> = {
  pong: 'Pong',
  tetris: 'Tetris',
  flappy: 'Flappy Bird',
};

export const gameHints: Record<GameId, string> = {
  pong: 'W/S or Up/Down. Mouse also works.',
  tetris: 'Arrow keys move. Up rotates. Space drops.',
  flappy: 'Space, Up, or click to flap.',
};

interface ArcadeGamesModalProps {
  canvasRef?: React.Ref<HTMLCanvasElement>;
  modalRef?: React.Ref<HTMLDivElement>;
  selectedGame: GameId | null;
  onBackToGames: () => void;
  onClose: () => void;
  onSelectGame: (game: GameId) => void;
}

const ArcadeGamesModal: React.FC<ArcadeGamesModalProps> = ({
  canvasRef,
  modalRef,
  selectedGame,
  onBackToGames,
  onClose,
  onSelectGame,
}) => {
  const selectedLabel = selectedGame ? gameLabels[selectedGame] : null;

  return (
    <div className="arcade-games-backdrop" data-testid="arcade-games-backdrop" onClick={onClose}>
      <div
        aria-label="Kodelet games"
        aria-modal="true"
        className="arcade-games-modal surface-panel"
        data-testid="arcade-games-modal"
        onClick={(event) => event.stopPropagation()}
        ref={modalRef}
        role="dialog"
        tabIndex={-1}
      >
        <div className="arcade-games-header">
          <div className="arcade-game-title-block">
            <p className="arcade-game-kicker">Select game</p>
            <p className="arcade-game-title">{selectedLabel || 'Hidden arcade'}</p>
          </div>
          <div className="arcade-game-header-actions">
            {selectedGame ? (
              <button className="composer-capsule" onClick={onBackToGames} type="button">
                Games
              </button>
            ) : null}
            <button className="composer-capsule arcade-games-close" onClick={onClose} type="button">
              Close
            </button>
          </div>
        </div>

        {selectedGame ? (
          <canvas
            aria-label={`${selectedLabel} play area`}
            className="arcade-games-canvas"
            data-arcade-autofocus="true"
            data-testid="arcade-games-canvas"
            ref={canvasRef}
            tabIndex={0}
          />
        ) : (
          <div className="arcade-game-picker" data-testid="arcade-game-picker">
            {GAME_OPTIONS.map((game) => (
              <button
                className="arcade-game-card"
                key={game}
                onClick={() => onSelectGame(game)}
                type="button"
              >
                <span className="arcade-game-name">{gameLabels[game]}</span>
                <span className="arcade-game-copy">{gameHints[game]}</span>
              </button>
            ))}
          </div>
        )}

        <p className="arcade-games-hint">Esc or click outside to close.</p>
      </div>
    </div>
  );
};

export default ArcadeGamesModal;
