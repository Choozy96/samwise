package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"samwise/internal/store"
)

// mcpSecret is the decrypted shape of a registry entry's secret_enc blob: env
// vars for stdio servers, headers for http servers. The web layer encrypts the
// same JSON shape when saving credentials.
type mcpSecret struct {
	Env     map[string]string `json:"env,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
}

// buildMCP assembles the --mcp-config JSON and the matching --allowedTools list
// for a run: the core server (this binary, bound to the run context) plus every
// enabled registry entry for the user. Secrets are decrypted in
// memory here and never written to disk in plaintext.
func (o *Orchestrator) buildMCP(ctx context.Context, userID, runID int64) (string, []string) {
	servers := map[string]any{}
	allowed := append([]string{}, coreTools...)

	if o.exePath != "" {
		servers["core"] = map[string]any{
			"command": o.exePath,
			"args": []string{
				"mcp",
				"--db", o.dbAbs,
				"--user-id", strconv.FormatInt(userID, 10),
				"--run-id", strconv.FormatInt(runID, 10),
			},
		}
	}

	regs, err := o.db.ListEnabledMCPServers(ctx, userID)
	if err != nil {
		o.log.Error("listing mcp servers", "user_id", userID, "err", err)
	}
	for _, m := range regs {
		entry, ok := o.registryEntry(m)
		if !ok {
			o.log.Warn("skipping malformed mcp server", "name", m.Name)
			continue
		}
		servers[m.Name] = entry
		allowed = append(allowed, registryAllowed(m)...)
	}

	b, err := json.Marshal(map[string]any{"mcpServers": servers})
	if err != nil {
		o.log.Error("marshaling mcp config", "err", err)
		return `{"mcpServers":{}}`, coreTools
	}
	// Note: never log the config JSON — it contains decrypted credentials.
	names := make([]string, 0, len(servers))
	for n := range servers {
		names = append(names, n)
	}
	o.log.Debug("composed mcp config", "servers", names)
	return string(b), allowed
}

// registryEntry builds the --mcp-config server object for a registry row.
func (o *Orchestrator) registryEntry(m store.MCPServer) (map[string]any, bool) {
	sec := o.decryptSecret(m.SecretEnc)
	switch m.Transport {
	case "stdio":
		if m.Command == "" {
			return nil, false
		}
		e := map[string]any{"command": m.Command}
		var args []string
		if m.ArgsJSON != "" {
			_ = json.Unmarshal([]byte(m.ArgsJSON), &args)
		}
		if len(args) > 0 {
			e["args"] = args
		}
		if len(sec.Env) > 0 {
			e["env"] = sec.Env
		}
		return e, true
	case "http":
		if m.URL == "" {
			return nil, false
		}
		e := map[string]any{"type": "http", "url": m.URL}
		if len(sec.Headers) > 0 {
			e["headers"] = sec.Headers
		}
		return e, true
	}
	return nil, false
}

// registryAllowed returns the --allowedTools entries for a registry server so
// unattended runs never stall on a permission prompt. If tool names
// were discovered at registration, each is allowed explicitly; otherwise a
// server-scoped wildcard is used as a best-effort fallback.
func registryAllowed(m store.MCPServer) []string {
	var tools []string
	if m.ToolsJSON != "" {
		_ = json.Unmarshal([]byte(m.ToolsJSON), &tools)
	}
	if len(tools) == 0 {
		return []string{fmt.Sprintf("mcp__%s__*", m.Name)}
	}
	out := make([]string, 0, len(tools))
	for _, t := range tools {
		out = append(out, fmt.Sprintf("mcp__%s__%s", m.Name, t))
	}
	return out
}

// decryptSecret decrypts a registry entry's secret blob, returning an empty
// secret on any error (the server then runs without env/headers).
func (o *Orchestrator) decryptSecret(enc string) mcpSecret {
	var s mcpSecret
	if enc == "" || o.box == nil || !o.box.Enabled() {
		return s
	}
	raw, err := o.box.Decrypt(enc)
	if err != nil {
		o.log.Error("decrypting mcp secret", "err", err)
		return s
	}
	_ = json.Unmarshal(raw, &s)
	return s
}

// EncodeSecret marshals env/headers into the JSON shape stored (encrypted) in
// secret_enc. Used by the web layer when saving credentials.
func EncodeSecret(env, headers map[string]string) ([]byte, error) {
	return json.Marshal(mcpSecret{Env: env, Headers: headers})
}
