package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"

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
// for a run: the core server (hosted in-process, reached over a token-scoped
// loopback HTTP endpoint) plus every enabled registry entry for the user.
// Secrets are decrypted in memory here and travel in the config; the caller
// writes it to a 0600 file, never argv. The returned revoke func tears down the
// run's core-MCP token and must be called when the run ends.
func (o *Orchestrator) buildMCP(ctx context.Context, scope runScope) (string, []string, func()) {
	servers := map[string]any{}
	allowed := append([]string{}, coreTools...)
	revoke := func() {}

	if o.mcp.ready() {
		token, rev, err := o.mcp.register(scope)
		if err != nil {
			o.log.Error("registering core mcp token", "err", err)
		} else {
			revoke = rev
			servers["core"] = map[string]any{
				"type": "http",
				"url":  o.mcp.endpoint(),
				"headers": map[string]any{
					"Authorization": "Bearer " + token,
				},
			}
		}
	} else {
		o.log.Error("core mcp host not started; core tools unavailable for run", "run_id", scope.runID)
	}

	regs, err := o.db.ListEnabledMCPServers(ctx, scope.userID)
	if err != nil {
		o.log.Error("listing mcp servers", "user_id", scope.userID, "err", err)
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
		revoke()
		return `{"mcpServers":{}}`, coreTools, func() {}
	}
	// Note: never log the config JSON — it contains decrypted credentials.
	names := make([]string, 0, len(servers))
	for n := range servers {
		names = append(names, n)
	}
	o.log.Debug("composed mcp config", "servers", names)
	return string(b), allowed, revoke
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
