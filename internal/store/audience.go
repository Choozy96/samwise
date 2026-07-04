package store

import "encoding/json"

// ParseToolAudience decodes a settings.ToolAudience JSON map (tool name ->
// 'everyone'). Only overrides are stored; tools absent from the map use their
// code default. A blank or malformed value yields an empty map.
func ParseToolAudience(j string) map[string]string {
	m := map[string]string{}
	if j == "" {
		return m
	}
	_ = json.Unmarshal([]byte(j), &m)
	return m
}

// MarshalToolAudience encodes a tool-audience map for storage, dropping entries
// set back to the default so the JSON only ever holds real overrides. An empty
// map serializes to "" (the column default).
func MarshalToolAudience(m map[string]string) string {
	for k, v := range m {
		if v != AudienceEveryone {
			delete(m, k)
		}
	}
	if len(m) == 0 {
		return ""
	}
	b, _ := json.Marshal(m)
	return string(b)
}
