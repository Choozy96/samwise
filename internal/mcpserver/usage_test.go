package mcpserver

import (
	"strings"
	"testing"
)

// TestUsageReportAdminGate: the tool refuses non-admin users and answers admins.
func TestUsageReportAdminGate(t *testing.T) {
	h, ctx := newJobHandlers(t) // its user is non-admin unless the helper says otherwise
	u, err := h.db.GetUserByID(ctx, h.userID)
	if err != nil {
		t.Fatal(err)
	}

	r, _, _ := h.usageReport(ctx, nil, usageReportIn{})
	got := resultText(r)
	if u.IsAdmin {
		if strings.Contains(got, "admin users only") {
			t.Fatalf("admin should not be refused: %q", got)
		}
	} else {
		if !strings.Contains(got, "admin users only") {
			t.Fatalf("non-admin should be refused: %q", got)
		}
	}
}
