package store

import (
	"context"
	"database/sql"
	"errors"
)

// Message is one transcript entry. Role is user|assistant|tool. Channel records
// which surface (web|telegram) the message arrived on.
type Message struct {
	ID        int64
	Role      string
	Content   string
	Channel   string
	CreatedAt string
}

// Conversation is a per-user, per-agent dialogue with its rolling summary and
// optional native-continuity session id. A single thread spans all channels
// (web + Telegram) so the assistant keeps one continuous transcript regardless of
// where a message arrives; Channel only records where the thread was first opened.
type Conversation struct {
	ID               int64
	UserID           int64
	Channel          string
	AgentID          int64
	HarnessSessionID string
	Summary          string
	SummaryMsgCount  int
}

// GetOrCreateConversation returns the user's current conversation for a given
// agent, creating one if none exists. The thread is shared across channels —
// channel is recorded only as where the thread originated. Switching agent
// switches conversation.
func (db *DB) GetOrCreateConversation(ctx context.Context, userID int64, channel string, agentID int64) (*Conversation, error) {
	c, err := db.latestConversation(ctx, userID, agentID)
	if err == nil {
		return c, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	res, err := db.ExecContext(ctx,
		`INSERT INTO conversations(user_id, channel, agent_id) VALUES(?,?,?)`, userID, channel, agentID)
	if err != nil {
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	return &Conversation{ID: id, UserID: userID, Channel: channel, AgentID: agentID}, nil
}

func (db *DB) latestConversation(ctx context.Context, userID, agentID int64) (*Conversation, error) {
	var c Conversation
	var sess sql.NullString
	var aid sql.NullInt64
	err := db.QueryRowContext(ctx,
		`SELECT id, user_id, channel, COALESCE(agent_id,0), harness_session_id, summary, summary_msg_count
		   FROM conversations WHERE user_id = ? AND COALESCE(agent_id,0) = ?
		  ORDER BY id DESC LIMIT 1`, userID, agentID).
		Scan(&c.ID, &c.UserID, &c.Channel, &aid, &sess, &c.Summary, &c.SummaryMsgCount)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	c.AgentID = aid.Int64
	c.HarnessSessionID = sess.String
	return &c, nil
}

// GetConversation loads a conversation by id, scoped to the owning user.
func (db *DB) GetConversation(ctx context.Context, userID, convID int64) (*Conversation, error) {
	var c Conversation
	var sess sql.NullString
	var aid sql.NullInt64
	err := db.QueryRowContext(ctx,
		`SELECT id, user_id, channel, COALESCE(agent_id,0), harness_session_id, summary, summary_msg_count
		   FROM conversations WHERE id = ? AND user_id = ?`, convID, userID).
		Scan(&c.ID, &c.UserID, &c.Channel, &aid, &sess, &c.Summary, &c.SummaryMsgCount)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	c.AgentID = aid.Int64
	c.HarnessSessionID = sess.String
	return &c, nil
}

// AddMessage appends a transcript entry (tagged with the source channel) and
// bumps the conversation's updated_at.
func (db *DB) AddMessage(ctx context.Context, conversationID, userID int64, channel, role, content string) (int64, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx,
		`INSERT INTO messages(conversation_id, user_id, channel, role, content) VALUES(?,?,?,?,?)`,
		conversationID, userID, channel, role, content)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE conversations SET updated_at = datetime('now') WHERE id = ?`, conversationID); err != nil {
		return 0, err
	}
	return id, tx.Commit()
}

// RecentMessages returns the last n messages of a conversation in chronological
// order (oldest first), for context assembly.
func (db *DB) RecentMessages(ctx context.Context, conversationID int64, n int) ([]Message, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, role, content, channel, created_at FROM (
		    SELECT id, role, content, channel, created_at FROM messages
		     WHERE conversation_id = ? ORDER BY id DESC LIMIT ?
		 ) ORDER BY id ASC`, conversationID, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Message
	for rows.Next() {
		var m Message
		if err := rows.Scan(&m.ID, &m.Role, &m.Content, &m.Channel, &m.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// NewConversation forces a fresh conversation thread for (user, agent) by
// inserting a new row, so the next turn starts with empty context. The old
// thread (messages + summary) stays in the DB.
func (db *DB) NewConversation(ctx context.Context, userID int64, channel string, agentID int64) error {
	_, err := db.ExecContext(ctx,
		`INSERT INTO conversations(user_id, channel, agent_id) VALUES(?,?,?)`,
		userID, channel, agentID)
	return err
}

// LatestMessageID returns the id of the user's most recent message across all
// conversations (0 if none) — a cheap activity marker for the distiller.
func (db *DB) LatestMessageID(ctx context.Context, userID int64) (int64, error) {
	var id sql.NullInt64
	err := db.QueryRowContext(ctx, `SELECT MAX(id) FROM messages WHERE user_id = ?`, userID).Scan(&id)
	return id.Int64, err
}

// MessagesForUserInRange returns the user's messages across all conversations
// with created_at in [startUTC, endUTC) (UTC 'YYYY-MM-DD HH:MM:SS'), chronological
// — used by the overnight full-day distillation.
func (db *DB) MessagesForUserInRange(ctx context.Context, userID int64, startUTC, endUTC string) ([]Message, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, role, content, channel, created_at FROM messages
		   WHERE user_id = ? AND created_at >= ? AND created_at < ? ORDER BY id`,
		userID, startUTC, endUTC)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Message
	for rows.Next() {
		var m Message
		if err := rows.Scan(&m.ID, &m.Role, &m.Content, &m.Channel, &m.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// SetConversationSession records the harness session id for native-continuity
// reuse.
func (db *DB) SetConversationSession(ctx context.Context, conversationID int64, sessionID string) error {
	_, err := db.ExecContext(ctx,
		`UPDATE conversations SET harness_session_id = ? WHERE id = ?`, sessionID, conversationID)
	return err
}
