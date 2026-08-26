package mcpserver

import (
	"context"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// userGuide is the embedded product guide (the same markdown the web portal
// renders). Set once at startup from main via SetUserGuide; read-only reference
// the agent can pull on demand with the read_guide tool.
var userGuide string

// SetUserGuide installs the embedded user guide for the read_guide tool. Called
// once at startup (mirrors web.SetUserGuide).
func SetUserGuide(s string) { userGuide = s }

type readGuideIn struct {
	Section string `json:"section,omitempty" jsonschema:"optional: a section name (or part of one) to read, e.g. 'cron', 'memory', 'telegram', 'skills'. Omit to list the available sections first."`
}

func (h *handlers) registerGuide(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "read_guide",
		Description: "Read your own user guide — the authoritative reference for what you (this assistant) can do and how each feature works: memory, scheduled jobs, reminders, skills, Telegram and group chats, settings, secrets. Call with no argument to list the sections, then call again with a section name to read it. Use this to answer 'how do I…' / 'can you…' questions and to operate features correctly instead of guessing.",
	}, h.readGuide)
}

func (h *handlers) readGuide(_ context.Context, _ *mcp.CallToolRequest, in readGuideIn) (*mcp.CallToolResult, any, error) {
	sections := splitGuideSections(userGuide)
	if len(sections) == 0 {
		return textResult("The guide isn't available."), nil, nil
	}
	q := strings.TrimSpace(strings.ToLower(in.Section))
	if q == "" {
		h.audit("read_guide", "toc", "ok")
		return textResult("Your guide has these sections — call read_guide again with one of them:\n" + guideTOC(sections)), nil, nil
	}
	for _, sec := range sections {
		if strings.Contains(strings.ToLower(sec.title), q) {
			h.audit("read_guide", "section="+sec.title, "ok")
			return textResult("## " + sec.title + "\n" + sec.body), nil, nil
		}
	}
	h.audit("read_guide", "section="+in.Section, "no match")
	return textResult("No section matched \"" + in.Section + "\". Available sections:\n" + guideTOC(sections)), nil, nil
}

type guideSection struct{ title, body string }

func guideTOC(sections []guideSection) string {
	var b strings.Builder
	for _, s := range sections {
		b.WriteString("- ")
		b.WriteString(s.title)
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// splitGuideSections breaks the guide markdown into its "## " sections. Content
// before the first "## " (the title + intro) is ignored — every useful topic is
// under a section heading.
func splitGuideSections(g string) []guideSection {
	if strings.TrimSpace(g) == "" {
		return nil
	}
	var out []guideSection
	var cur *guideSection
	var body strings.Builder
	flush := func() {
		if cur != nil {
			cur.body = strings.TrimRight(body.String(), "\n")
			out = append(out, *cur)
			body.Reset()
		}
	}
	for _, ln := range strings.Split(g, "\n") {
		if strings.HasPrefix(ln, "## ") {
			flush()
			cur = &guideSection{title: strings.TrimSpace(strings.TrimPrefix(ln, "## "))}
			continue
		}
		if cur != nil {
			body.WriteString(ln)
			body.WriteString("\n")
		}
	}
	flush()
	return out
}
