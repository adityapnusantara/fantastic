// Package postgres implements the driven ConversationStore port on
// PostgreSQL (pgx).
package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/adityapratamanusantara/fantastic/internal/application/ports/driven"
	"github.com/adityapratamanusantara/fantastic/internal/domain"
)

// Store is the pgx-backed ConversationStore.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore builds a Store over the given pool.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// compile-time port conformance check.
var _ driven.ConversationStore = (*Store)(nil)

// CreateConversation persists a new conversation row with its agent
// binding; timestamps are assigned by the database so all rows share one
// clock.
func (s *Store) CreateConversation(ctx context.Context, conv *domain.Conversation) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO conversations (id, user_id, agent_id, title, rolling_summary, summary_until_seq)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		conv.ID, conv.UserID, conv.AgentID, conv.Title, conv.RollingSummary, conv.SummaryUntilSeq,
	)
	if err != nil {
		return fmt.Errorf("insert conversation: %w", err)
	}
	return nil
}

// GetConversation returns the conversation when owned by userID.
func (s *Store) GetConversation(ctx context.Context, id uuid.UUID, userID string) (*domain.Conversation, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT id, user_id, agent_id, title, rolling_summary, summary_until_seq, created_at, updated_at
		FROM conversations WHERE id = $1 AND user_id = $2`, id, userID)
	return scanConversation(row)
}

// ListConversations returns the caller's conversations, newest activity
// first. When agentID is non-nil, only conversations bound to that agent
// are returned (the (user_id, agent_id, updated_at DESC) index serves the
// filtered form).
func (s *Store) ListConversations(ctx context.Context, userID string, agentID *string) ([]domain.Conversation, error) {
	query := `
		SELECT id, user_id, agent_id, title, rolling_summary, summary_until_seq, created_at, updated_at
		FROM conversations WHERE user_id = $1`
	args := []any{userID}
	if agentID != nil {
		query += ` AND agent_id = $2`
		args = append(args, *agentID)
	}
	query += ` ORDER BY updated_at DESC`

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list conversations: %w", err)
	}
	defer rows.Close()

	var out []domain.Conversation
	for rows.Next() {
		conv, err := scanConversation(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *conv)
	}
	return out, rows.Err()
}

// DeleteConversation removes the conversation; messages cascade.
func (s *Store) DeleteConversation(ctx context.Context, id uuid.UUID, userID string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM conversations WHERE id = $1 AND user_id = $2`, id, userID)
	if err != nil {
		return fmt.Errorf("delete conversation: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// CountByAgent returns the number of conversations bound to agentID.
// Unknown agents simply count zero.
func (s *Store) CountByAgent(ctx context.Context, agentID string) (int64, error) {
	var n int64
	err := s.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM conversations WHERE agent_id = $1`, agentID,
	).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count conversations for agent: %w", err)
	}
	return n, nil
}

// AppendMessage persists one message atomically (single-message batch).
func (s *Store) AppendMessage(ctx context.Context, conversationID uuid.UUID, userID string, msg *domain.Message, title *string) (int64, error) {
	var seq []int64
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		var txErr error
		seq, txErr = appendBatch(ctx, tx, conversationID, userID, []*domain.Message{msg}, title)
		return txErr
	})
	if err != nil {
		return 0, err
	}
	return seq[0], nil
}

// AppendMessages persists several messages in one transaction.
func (s *Store) AppendMessages(ctx context.Context, conversationID uuid.UUID, userID string, msgs []*domain.Message, title *string) ([]int64, error) {
	var seq []int64
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		var txErr error
		seq, txErr = appendBatch(ctx, tx, conversationID, userID, msgs, title)
		return txErr
	})
	return seq, err
}

// GetMessages returns all messages of the conversation in seq order.
func (s *Store) GetMessages(ctx context.Context, id uuid.UUID, userID string) ([]domain.Message, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT m.id, m.conversation_id, m.seq, m.role, m.content, m.tool_calls, m.tool_call_id, m.created_at
		FROM messages m
		JOIN conversations c ON c.id = m.conversation_id
		WHERE m.conversation_id = $1 AND c.user_id = $2
		ORDER BY m.seq ASC`, id, userID)
	if err != nil {
		return nil, fmt.Errorf("get messages: %w", err)
	}
	defer rows.Close()
	return scanMessages(rows)
}

// LoadWindow returns the conversation plus its most recent messages.
func (s *Store) LoadWindow(ctx context.Context, id uuid.UUID, userID string, limit int) (*domain.Conversation, []domain.Message, error) {
	conv, err := s.GetConversation(ctx, id, userID)
	if err != nil {
		return nil, nil, err
	}

	rows, err := s.pool.Query(ctx, `
		SELECT id, conversation_id, seq, role, content, tool_calls, tool_call_id, created_at
		FROM messages WHERE conversation_id = $1
		ORDER BY seq DESC LIMIT $2`, id, limit)
	if err != nil {
		return nil, nil, fmt.Errorf("load window: %w", err)
	}
	defer rows.Close()

	msgs, err := scanMessages(rows)
	if err != nil {
		return nil, nil, err
	}
	// reverse to ascending seq order
	for i, j := 0, len(msgs)-1; i < j; i, j = i+1, j-1 {
		msgs[i], msgs[j] = msgs[j], msgs[i]
	}
	return conv, msgs, nil
}

// SaveSummary updates the rolling summary and coverage marker.
func (s *Store) SaveSummary(ctx context.Context, id uuid.UUID, userID, summary string, untilSeq int64) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE conversations
		SET rolling_summary = $3, summary_until_seq = $4, updated_at = now()
		WHERE id = $1 AND user_id = $2`, id, userID, summary, untilSeq)
	if err != nil {
		return fmt.Errorf("save summary: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// appendBatch assigns consecutive seq values starting at MAX(seq)+1 and
// inserts with ON CONFLICT (id) DO NOTHING for idempotent retries. The
// conversation row is verified against userID and locked FOR UPDATE, so
// foreign users can neither append nor race the seq counter.
func appendBatch(ctx context.Context, tx pgx.Tx, conversationID uuid.UUID, userID string, msgs []*domain.Message, title *string) ([]int64, error) {
	var owned int
	err := tx.QueryRow(ctx,
		`SELECT 1 FROM conversations WHERE id = $1 AND user_id = $2 FOR UPDATE`, conversationID, userID,
	).Scan(&owned)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("lock conversation: %w", err)
	}

	var base int64
	if err := tx.QueryRow(ctx,
		`SELECT COALESCE(MAX(seq), 0) FROM messages WHERE conversation_id = $1`, conversationID,
	).Scan(&base); err != nil {
		return nil, fmt.Errorf("select max seq: %w", err)
	}

	seqs := make([]int64, 0, len(msgs))
	for i, msg := range msgs {
		var toolCallsJSON []byte
		if len(msg.ToolCalls) > 0 {
			raw, err := json.Marshal(msg.ToolCalls)
			if err != nil {
				return nil, fmt.Errorf("marshal tool_calls: %w", err)
			}
			toolCallsJSON = raw
		}

		var toolCallID any
		if msg.ToolCallID != "" {
			toolCallID = msg.ToolCallID
		}

		if _, err := tx.Exec(ctx, `
			INSERT INTO messages (id, conversation_id, seq, role, content, tool_calls, tool_call_id)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
			ON CONFLICT (id) DO NOTHING`,
			msg.ID, conversationID, base+int64(i)+1, string(msg.Role), msg.Content, toolCallsJSON, toolCallID,
		); err != nil {
			return nil, fmt.Errorf("insert message: %w", err)
		}
		seqs = append(seqs, base+int64(i)+1)
		msg.Seq = base + int64(i) + 1
	}

	if _, err := tx.Exec(ctx, `
		UPDATE conversations
		SET updated_at = now(),
		    title = CASE WHEN $3::text IS NOT NULL THEN $3 ELSE title END
		WHERE id = $1 AND user_id = $2`,
		conversationID, userID, title,
	); err != nil {
		return nil, fmt.Errorf("touch conversation: %w", err)
	}

	return seqs, nil
}

// inTx runs fn inside a transaction, rolling back on error.
func (s *Store) inTx(ctx context.Context, fn func(tx pgx.Tx) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback(ctx)
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}
	return nil
}

// scanConversation scans one conversation row.
func scanConversation(row pgx.Row) (*domain.Conversation, error) {
	var c domain.Conversation
	err := row.Scan(&c.ID, &c.UserID, &c.AgentID, &c.Title, &c.RollingSummary, &c.SummaryUntilSeq, &c.CreatedAt, &c.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scan conversation: %w", err)
	}
	return &c, nil
}

// scanMessages collects message rows.
func scanMessages(rows pgx.Rows) ([]domain.Message, error) {
	var out []domain.Message
	for rows.Next() {
		var m domain.Message
		var role string
		var toolCallsJSON []byte
		var toolCallID *string
		if err := rows.Scan(&m.ID, &m.ConversationID, &m.Seq, &role, &m.Content, &toolCallsJSON, &toolCallID, &m.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan message: %w", err)
		}
		m.Role = domain.Role(role)
		if toolCallID != nil {
			m.ToolCallID = *toolCallID
		}
		if len(toolCallsJSON) > 0 {
			if err := json.Unmarshal(toolCallsJSON, &m.ToolCalls); err != nil {
				return nil, fmt.Errorf("unmarshal tool_calls: %w", err)
			}
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
