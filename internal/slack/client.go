package slack

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	slackapi "github.com/slack-go/slack"
)

// maxDownloadBytes caps inbound file downloads (mirror of the Telegram intake).
const maxDownloadBytes = 45 << 20

// Client wraps one Slack app's Web API client. The app-level (Socket Mode)
// token is handled by the Manager; this is the xoxb- side.
type Client struct {
	api *slackapi.Client
}

// NewClient builds a Web API client. opts is for tests (OptionAPIURL).
func NewClient(botToken string, opts ...slackapi.Option) *Client {
	return &Client{api: slackapi.New(botToken, opts...)}
}

// AuthTest validates the bot token and returns the app's bot user id (U…) and
// workspace name — cached for mention detection and display.
func (c *Client) AuthTest(ctx context.Context) (botUserID, teamName string, err error) {
	resp, err := c.api.AuthTestContext(ctx)
	if err != nil {
		return "", "", err
	}
	return resp.UserID, resp.Team, nil
}

// PostMessage sends mrkdwn text to a channel/DM; threadTS non-empty replies in
// that thread.
func (c *Client) PostMessage(ctx context.Context, channelID, threadTS, text string) error {
	opts := []slackapi.MsgOption{slackapi.MsgOptionText(text, false)}
	if threadTS != "" {
		opts = append(opts, slackapi.MsgOptionTS(threadTS))
	}
	_, _, err := c.api.PostMessageContext(ctx, channelID, opts...)
	return err
}

// UploadFile uploads a document to a channel (the external-upload flow behind
// slack-go's UploadFileContext), with an optional comment; threadTS non-empty
// attaches it to that thread.
func (c *Client) UploadFile(ctx context.Context, channelID, threadTS, filename string, data []byte, caption string) error {
	params := slackapi.UploadFileParameters{
		Channel:         channelID,
		Filename:        filename,
		FileSize:        len(data),
		Reader:          bytes.NewReader(data),
		InitialComment:  caption,
		ThreadTimestamp: threadTS,
	}
	_, err := c.api.UploadFileContext(ctx, params)
	return err
}

// DownloadFile fetches a file's url_private (bot-token-authorized), size-capped.
func (c *Client) DownloadFile(ctx context.Context, url string) ([]byte, error) {
	var buf limitedBuffer
	if err := c.api.GetFileContext(ctx, url, &buf); err != nil {
		return nil, err
	}
	if buf.overflowed {
		return nil, fmt.Errorf("slack: file exceeds %d bytes", maxDownloadBytes)
	}
	return buf.b, nil
}

// deliver formats + chunks + sends with the same at-most-once policy as the
// Telegram sender: retry ONLY on a definitive rate-limit rejection (Slack tells
// us it was not delivered, with a wait); an ambiguous transport error returns
// as-is rather than re-sending a message that may already be in the channel.
func deliver(ctx context.Context, c *Client, channelID, threadTS, rawText string, log *slog.Logger) error {
	for _, part := range chunk(markdownToMrkdwn(rawText), slackMaxLen) {
		if err := postRetry(ctx, c, channelID, threadTS, part); err != nil {
			return err
		}
	}
	return nil
}

func postRetry(ctx context.Context, c *Client, channelID, threadTS, text string) error {
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		if err = c.PostMessage(ctx, channelID, threadTS, text); err == nil {
			return nil
		}
		var rl *slackapi.RateLimitedError
		if !errors.As(err, &rl) {
			return err // definitive non-retryable, or ambiguous transport: no re-send
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(rl.RetryAfter):
		}
	}
	return err
}

// ── small helpers ────────────────────────────────────────────────────────────

type limitedBuffer struct {
	b          []byte
	overflowed bool
}

func (l *limitedBuffer) Write(p []byte) (int, error) {
	if len(l.b)+len(p) > maxDownloadBytes {
		l.overflowed = true
		return 0, fmt.Errorf("slack: download exceeds cap")
	}
	l.b = append(l.b, p...)
	return len(p), nil
}

// ThreadParent fetches a thread's ROOT message (text + attached files) so a
// reply that mentions the bot can see the media it's pointing at — e.g. someone
// posts a PDF, another user replies in-thread "@bot summarize this".
func (c *Client) ThreadParent(ctx context.Context, channelID, threadTS string) (text string, files []fileRef, err error) {
	msgs, _, _, err := c.api.GetConversationRepliesContext(ctx, &slackapi.GetConversationRepliesParameters{
		ChannelID: channelID,
		Timestamp: threadTS,
		Limit:     1,
		Inclusive: true,
	})
	if err != nil || len(msgs) == 0 {
		return "", nil, err
	}
	parent := msgs[0]
	for _, f := range parent.Files {
		url := f.URLPrivateDownload
		if url == "" {
			url = f.URLPrivate
		}
		if url == "" {
			continue
		}
		name := f.Name
		if name == "" {
			name = "file"
		}
		files = append(files, fileRef{Name: name, URL: url})
	}
	return parent.Text, files, nil
}
