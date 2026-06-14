package web

import (
	"fmt"
	"io"
	"net/http"

	"samwise/internal/orchestrator"
)

const (
	maxAttachFiles      = 5
	maxAttachFileBytes  = orchestrator.MaxAttachmentBytes // 10 MiB per file
	maxAttachTotalBytes = 25 << 20                        // 25 MiB per message
)

// saveAttachments reads uploaded files (with size/count guards) and hands each to
// the shared orchestrator saver, which writes it to the user's workspace and
// inlines small text files. Returns them as orchestrator attachments.
func (s *Server) saveAttachments(r *http.Request) ([]orchestrator.Attachment, error) {
	headers := r.MultipartForm.File["attachments"]
	if len(headers) > maxAttachFiles {
		return nil, fmt.Errorf("too many files (max %d)", maxAttachFiles)
	}
	u := currentUser(r.Context())

	var out []orchestrator.Attachment
	var total int64
	for _, h := range headers {
		if h.Size > maxAttachFileBytes {
			return nil, fmt.Errorf("%q is too large (max 10MB)", h.Filename)
		}
		total += h.Size
		if total > maxAttachTotalBytes {
			return nil, fmt.Errorf("attachments exceed 25MB total")
		}

		f, err := h.Open()
		if err != nil {
			return nil, err
		}
		data, err := io.ReadAll(io.LimitReader(f, maxAttachFileBytes+1))
		_ = f.Close()
		if err != nil {
			return nil, err
		}
		att, err := s.orch.SaveAttachment(u.ID, h.Filename, data)
		if err != nil {
			return nil, err
		}
		out = append(out, att)
		_ = s.db.AddAuditEvent(r.Context(), u.ID, 0, "message", "attachment", att.Name, "ok")
	}
	return out, nil
}
