package mcpserver

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// FileSendFunc delivers a file from the user's workspace to the run's origin
// chat (or the user's default channel / web). Injected by the orchestrator,
// which owns the workspace path resolution and the channel senders.
type FileSendFunc func(ctx context.Context, userID, botID, chatID int64, relPath, caption string) error

var fileSender FileSendFunc

// SetFileSender wires the workspace-file delivery sink (send_file tool). Until
// set, send_file reports that file delivery isn't available.
func SetFileSender(fn FileSendFunc) { fileSender = fn }

type sendFileIn struct {
	Path    string `json:"path" jsonschema:"path to the file in your workspace to send, e.g. 'report.xlsx' or 'out/summary.csv'. Create the file first with your file tools."`
	Caption string `json:"caption,omitempty" jsonschema:"optional short caption/message to accompany the file"`
}

func (h *handlers) registerFiles(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name: "send_file",
		Description: "Send a file from your workspace to the user (as a Telegram document, or a download on the web). " +
			"Use this to hand over a spreadsheet (.xlsx), CSV, Markdown, text, or other file you created — write the file " +
			"first (with your file tools, or a script), then call send_file with its path. Delivered to the chat you're in.",
	}, h.sendFile)
}

func (h *handlers) sendFile(ctx context.Context, _ *mcp.CallToolRequest, in sendFileIn) (*mcp.CallToolResult, any, error) {
	if h.readOnly {
		return h.denyWrite("send_file"), nil, nil
	}
	path := strings.TrimSpace(in.Path)
	if path == "" {
		return h.fail("send_file", "", "path is required"), nil, nil
	}
	if fileSender == nil {
		return h.fail("send_file", path, "file delivery isn't available on this deployment"), nil, nil
	}
	if err := fileSender(ctx, h.userID, h.originBotID, h.originChatID, path, strings.TrimSpace(in.Caption)); err != nil {
		return h.fail("send_file", path, err.Error()), nil, nil
	}
	h.audit("send_file", "path="+path, "ok")
	return textResult(fmt.Sprintf("Sent %q to the user.", path)), nil, nil
}
