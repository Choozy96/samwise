// Package telegram implements the Telegram channel: a long-polling bot for
// inbound messages, a pairing flow, and an outbound sender (spec §4.1, §4.3).
// The orchestrator is the only component that touches Telegram; agents never do.
package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Client is a minimal Telegram Bot API client.
type Client struct {
	token   string
	baseURL string // override for tests; empty => the real Bot API
	http    *http.Client
}

// NewClient builds a client for the given bot token.
func NewClient(token string) *Client {
	return &Client{
		token: token,
		// Long-poll friendly: longer than the getUpdates timeout below.
		http: &http.Client{Timeout: 50 * time.Second},
	}
}

// Update is a Telegram update (subset). MyChatMember fires when the bot's own
// membership changes — e.g. it's added to a group.
type Update struct {
	UpdateID     int64             `json:"update_id"`
	Message      *Message          `json:"message"`
	MyChatMember *ChatMemberUpdate `json:"my_chat_member"`
}

// ChatMemberUpdate reports a change to the bot's membership in a chat.
type ChatMemberUpdate struct {
	Chat          *Chat       `json:"chat"`
	From          *User       `json:"from"`
	NewChatMember *ChatMember `json:"new_chat_member"`
}

// ChatMember is the bot's membership state in a chat (subset). Status is
// "member"/"administrator" when present, "left"/"kicked" when not.
type ChatMember struct {
	Status string `json:"status"`
}

// Message is a Telegram message (subset). A message carries either Text, or a
// media item (Document/Photo/Video/Voice/Audio/Animation/VideoNote) with an
// optional Caption (Telegram puts a file's text in Caption, not Text).
type Message struct {
	MessageID int64       `json:"message_id"`
	From      *User       `json:"from"`
	Chat      *Chat       `json:"chat"`
	Text      string      `json:"text"`
	Caption   string      `json:"caption"`
	Document  *Document   `json:"document"`
	Photo     []PhotoSize `json:"photo"`
	Video     *Document   `json:"video"`
	Animation *Document   `json:"animation"`
	Voice     *Document   `json:"voice"`
	Audio     *Document   `json:"audio"`
	VideoNote *Document   `json:"video_note"`
	Sticker   *Sticker    `json:"sticker"`
	// ReplyToMessage is set when this message replies to another — used to detect
	// a reply directed at the bot in a group.
	ReplyToMessage *Message `json:"reply_to_message"`
}

// Sticker is a Telegram sticker. Static stickers are WEBP (viewable as an image);
// animated (.tgs) and video (.webm) ones aren't directly viewable.
type Sticker struct {
	FileID     string `json:"file_id"`
	FileSize   int64  `json:"file_size"`
	IsAnimated bool   `json:"is_animated"`
	IsVideo    bool   `json:"is_video"`
	Emoji      string `json:"emoji"`
}

// Document is an uploaded file (subset). Telegram's video/voice/audio/animation
// objects share the fields we need (file_id, mime_type, file_size), so they reuse
// this type; file_name is absent for some (we synthesize one).
type Document struct {
	FileID   string `json:"file_id"`
	FileName string `json:"file_name"`
	MimeType string `json:"mime_type"`
	FileSize int64  `json:"file_size"`
}

// PhotoSize is one rendition of a photo; Telegram sends several sizes per photo.
type PhotoSize struct {
	FileID   string `json:"file_id"`
	FileSize int64  `json:"file_size"`
	Width    int    `json:"width"`
	Height   int    `json:"height"`
}

// User is a Telegram user (subset).
type User struct {
	ID        int64  `json:"id"`
	FirstName string `json:"first_name"`
	Username  string `json:"username"`
}

// Chat is a Telegram chat (subset). Type is "private" for a 1:1 DM or
// "group"/"supergroup" for a group chat.
type Chat struct {
	ID   int64  `json:"id"`
	Type string `json:"type"`
}

// GetMe calls getMe to verify the token and return the bot's id and @username —
// used to validate a registered bot, cache its handle, and detect @mentions /
// replies addressed to it in groups.
func (c *Client) GetMe(ctx context.Context) (id int64, username string, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base()+"/getMe", nil)
	if err != nil {
		return 0, "", err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	var out struct {
		OK          bool   `json:"ok"`
		Description string `json:"description"`
		Result      struct {
			ID       int64  `json:"id"`
			Username string `json:"username"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return 0, "", err
	}
	if !out.OK {
		return 0, "", fmt.Errorf("telegram getMe: %s", out.Description)
	}
	return out.Result.ID, out.Result.Username, nil
}

// GetUpdates long-polls for updates after offset.
func (c *Client) GetUpdates(ctx context.Context, offset int64, timeoutSec int) ([]Update, error) {
	url := fmt.Sprintf("%s/getUpdates?offset=%d&timeout=%d", c.base(), offset, timeoutSec)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out struct {
		OK          bool     `json:"ok"`
		Description string   `json:"description"`
		Result      []Update `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	if !out.OK {
		return nil, fmt.Errorf("telegram getUpdates: %s", out.Description)
	}
	return out.Result, nil
}

// SendMessage sends text to a chat. parseMode is "" (plain), "HTML", or
// "MarkdownV2". Returns an error for non-OK responses so the caller can retry or
// fall back to plain text.
func (c *Client) SendMessage(ctx context.Context, chatID int64, text, parseMode string) error {
	m := map[string]any{"chat_id": chatID, "text": text}
	if parseMode != "" {
		m["parse_mode"] = parseMode
	}
	body, _ := json.Marshal(m)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base()+"/sendMessage", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("telegram sendMessage: status %d: %s", resp.StatusCode, string(b))
	}
	return nil
}

// SendChatAction sends a chat action (e.g. "typing"), which Telegram shows for
// about 5 seconds. Best-effort: errors are returned but typically ignored.
func (c *Client) SendChatAction(ctx context.Context, chatID int64, action string) error {
	body, _ := json.Marshal(map[string]any{"chat_id": chatID, "action": action})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base()+"/sendChatAction", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	return nil
}

// GetFile resolves a file_id to a downloadable file_path (Bot API getFile).
func (c *Client) GetFile(ctx context.Context, fileID string) (string, error) {
	url := fmt.Sprintf("%s/getFile?file_id=%s", c.base(), fileID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var out struct {
		OK          bool   `json:"ok"`
		Description string `json:"description"`
		Result      struct {
			FilePath string `json:"file_path"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	if !out.OK || out.Result.FilePath == "" {
		return "", fmt.Errorf("telegram getFile: %s", out.Description)
	}
	return out.Result.FilePath, nil
}

// DownloadFile fetches a file by its file_path (from GetFile), reading at most
// maxBytes. The download endpoint lives under /file/bot<token>/, not the API base.
func (c *Client) DownloadFile(ctx context.Context, filePath string, maxBytes int64) ([]byte, error) {
	url := "https://api.telegram.org/file/bot" + c.token + "/" + filePath
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("telegram file download: status %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("telegram file exceeds %d bytes", maxBytes)
	}
	return data, nil
}

func (c *Client) base() string {
	if c.baseURL != "" {
		return c.baseURL
	}
	return "https://api.telegram.org/bot" + c.token
}
