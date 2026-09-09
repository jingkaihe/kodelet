package controlplane

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"

	"github.com/jingkaihe/kodelet/pkg/chat"
	"github.com/jingkaihe/kodelet/pkg/db"
	convtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
	"github.com/jmoiron/sqlx"
	"github.com/pkg/errors"
)

type (
	turnStore struct{ db *sqlx.DB }
	turnRow   struct {
		chat.TurnReceipt
		RequestHash string `db:"request_hash"`
	}
)

var (
	errTurnConflict     = errors.New("turn ID already used with different input")
	errConversationBusy = errors.New("conversation already has an active run")
)

func newTurnStore(ctx context.Context, path string) (*turnStore, error) {
	database, err := db.Open(ctx, path)
	if err != nil {
		return nil, err
	}
	// A daemon restart never replays an admitted model/tool operation. Pending
	// exact cancellation is durable, including before the conversation exists.
	_, err = database.ExecContext(ctx, `UPDATE chat_turns SET
		status = CASE WHEN cancel_requested THEN 'cancelled' ELSE 'interrupted' END,
		error = 'server stopped before a terminal outcome was recorded', updated_at = ?
		WHERE status IN ('accepted', 'running')`, time.Now().UTC())
	if err != nil {
		_ = database.Close()
		return nil, errors.Wrap(err, "failed to restore saved turn statuses")
	}
	return &turnStore{db: database}, nil
}

func turnRequestHash(req chat.ChatRequest) (string, error) {
	// UI ownership is observer state, not an execution input. Normalize text,
	// images and selector whitespace; retain explicit restrictions/zero values.
	message, images, err := chat.NormalizeRequest(req)
	if err != nil {
		return "", err
	}
	req.Message = message
	req.Content = chat.ContentBlocksForUserInput(message, images)
	if len(req.Content) > 0 {
		req.Message = ""
	}
	req.ClientCapabilities = nil
	req.RunnerID, req.Profile = strings.TrimSpace(req.RunnerID), strings.TrimSpace(req.Profile)
	req.EnvironmentProfile, req.CWD = chat.NormalizeEnvironmentProfile(req.EnvironmentProfile), strings.TrimSpace(req.CWD)
	data, err := json.Marshal(req)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}

func (s *turnStore) get(ctx context.Context, conversationID, turnID string) (chat.TurnReceipt, error) {
	var row turnRow
	err := s.db.GetContext(ctx, &row, `SELECT * FROM chat_turns WHERE conversation_id = ? AND turn_id = ?`, conversationID, turnID)
	return row.TurnReceipt, err
}

func (s *turnStore) admit(ctx context.Context, req chat.ChatRequest) (chat.TurnReceipt, bool, error) {
	hash, err := turnRequestHash(req)
	if err != nil {
		return chat.TurnReceipt{}, false, err
	}
	connection, err := s.db.Connx(ctx)
	if err != nil {
		return chat.TurnReceipt{}, false, err
	}
	defer connection.Close()
	// Admission reads before inserting. Reserve the write transaction before
	// taking a WAL snapshot, so concurrent conversation persistence cannot make
	// that snapshot unwritable (SQLITE_BUSY_SNAPSHOT). Never replay a query.
	if _, err := connection.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return chat.TurnReceipt{}, false, err
	}
	defer func() { _, _ = connection.ExecContext(context.WithoutCancel(ctx), "ROLLBACK") }()
	var row turnRow
	err = connection.GetContext(ctx, &row, `SELECT * FROM chat_turns WHERE conversation_id = ? AND turn_id = ?`, req.ConversationID, req.TurnID)
	if err == nil {
		if row.RequestHash != "" && row.RequestHash != hash {
			return chat.TurnReceipt{}, false, errTurnConflict
		}
		return row.TurnReceipt, false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return chat.TurnReceipt{}, false, err
	}
	var active int
	if err := connection.GetContext(ctx, &active, `SELECT COUNT(*) FROM chat_turns WHERE conversation_id = ? AND status IN ('accepted','running')`, req.ConversationID); err != nil {
		return chat.TurnReceipt{}, false, err
	}
	if active != 0 {
		return chat.TurnReceipt{}, false, errConversationBusy
	}
	now := time.Now().UTC()
	receipt := chat.TurnReceipt{ConversationID: req.ConversationID, TurnID: req.TurnID, RunID: convtypes.GenerateID(), Status: "accepted", CreatedAt: now, UpdatedAt: now}
	_, err = connection.ExecContext(ctx, `INSERT INTO chat_turns (conversation_id,turn_id,request_hash,run_id,status,created_at,updated_at) VALUES (?,?,?,?,?,?,?)`, receipt.ConversationID, receipt.TurnID, hash, receipt.RunID, receipt.Status, now, now)
	if err != nil {
		return chat.TurnReceipt{}, false, err
	}
	_, err = connection.ExecContext(ctx, "COMMIT")
	return receipt, true, err
}

func (s *turnStore) start(ctx context.Context, conversationID, turnID string) (bool, error) {
	result, err := s.db.ExecContext(ctx, `UPDATE chat_turns SET status='running', updated_at=? WHERE conversation_id=? AND turn_id=? AND status='accepted' AND NOT cancel_requested`, time.Now().UTC(), conversationID, turnID)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	return rows == 1, err
}

func (s *turnStore) finish(ctx context.Context, conversationID, turnID, status string, output *string, runErr error) error {
	message := ""
	if runErr != nil {
		message = runErr.Error()
	}
	_, err := s.db.ExecContext(ctx, `UPDATE chat_turns SET
		status = CASE WHEN cancel_requested THEN 'cancelled' ELSE ? END,
		result=?, error=?, updated_at=? WHERE conversation_id=? AND turn_id=? AND status IN ('accepted','running')`,
		status, output, message, time.Now().UTC(), conversationID, turnID)
	return errors.Wrap(err, "failed to save the turn's final result")
}

func (s *turnStore) stop(ctx context.Context, conversationID, turnID string) (chat.TurnReceipt, error) {
	now := time.Now().UTC()
	_, err := s.db.ExecContext(ctx, `INSERT INTO chat_turns (conversation_id,turn_id,status,cancel_requested,created_at,updated_at)
		VALUES (?,?,'cancelled',TRUE,?,?) ON CONFLICT(conversation_id,turn_id) DO UPDATE SET
		cancel_requested=TRUE, status=CASE WHEN status='accepted' THEN 'cancelled' ELSE status END,
		updated_at=excluded.updated_at WHERE status IN ('accepted','running')`, conversationID, turnID, now, now)
	if err != nil {
		return chat.TurnReceipt{}, err
	}
	return s.get(ctx, conversationID, turnID)
}
