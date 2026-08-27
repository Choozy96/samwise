package web

import (
	"io"
	"testing"

	"samwise/internal/orchestrator"
	"samwise/internal/store"
)

// TestAgentsTemplateRenders executes the agents page with both channel panels
// populated (Telegram + Slack), catching runtime template errors that a parse
// alone would miss.
func TestAgentsTemplateRenders(t *testing.T) {
	data := pageData{
		"Title": "Agents", "User": nil,
		"Agents":       []store.Agent{{ID: 1, Name: "Assistant", IsDefault: true, Enabled: true}},
		"ActiveID":     int64(1),
		"ModelOptions": []orchestrator.ModelOption{{Alias: "fable5", ID: "claude-fable-5", Label: "Claude Fable 5"}},
		"CustomModels": map[int64]string{},
		"Runtimes":     []orchestrator.RuntimeChoice{},
		"BoxEnabled":   true,
		"TgBots": []botView{{
			TelegramBot: store.TelegramBot{ID: 1, Label: "Main", Username: "mybot", Enabled: true},
			AgentName:   "Assistant",
			Pairings:    []store.ChannelIdentity{{BotID: 1, ExternalID: "555", ChatID: "555"}},
		}},
		"DiscordBots": []discordBotView{{
			DiscordBot: store.DiscordBot{ID: 3, Label: "Server", Username: "samwise", BotUserID: "999", Enabled: true},
			AgentName:  "Assistant",
			Pairings:   []store.ChannelIdentity{{BotID: 3, ExternalID: "888", ChatID: "888"}},
		}},
		"SlackBots": []slackBotView{{
			SlackBot:  store.SlackBot{ID: 2, Label: "Work", TeamName: "Acme", BotUserID: "U0B", Enabled: true},
			AgentName: "Assistant",
			Pairings:  []store.ChannelIdentity{{BotID: 2, ExternalID: "C0123", ChatID: "C0123"}},
		}},
	}
	if err := tmpl.ExecuteTemplate(io.Discard, "agents", data); err != nil {
		t.Fatalf("agents template render: %v", err)
	}
}
