// Package alerting sends operational alerts (submission failures, low
// faucet balances, DB pool exhaustion) to an external channel. The upstream
// project hard-coded Discord webhook URLs directly in several files with an
// env override; this template makes the destination fully env-driven and
// keeps Discord as the default because it's free and simple to set up -
// swap in Slack/PagerDuty/OpsGenie by implementing Notifier.
package alerting

import (
	"bytes"
	"encoding/json"
	"log"
	"net/http"
)

// Notifier sends a short operational alert message somewhere a human will see it.
type Notifier interface {
	Notify(message string) error
}

// NoopNotifier drops every alert - the default when no webhook is configured.
type NoopNotifier struct{}

func NewNoopNotifier() Notifier { return NoopNotifier{} }

func (NoopNotifier) Notify(string) error { return nil }

// DiscordWebhookNotifier posts to a Discord incoming webhook URL supplied
// entirely via configuration - never hardcode a webhook URL in source.
type DiscordWebhookNotifier struct {
	webhookURL string
}

func NewDiscordWebhookNotifier(webhookURL string) Notifier {
	return &DiscordWebhookNotifier{webhookURL: webhookURL}
}

func (d *DiscordWebhookNotifier) Notify(message string) error {
	body, err := json.Marshal(map[string]string{"content": message})
	if err != nil {
		return err
	}
	resp, err := http.Post(d.webhookURL, "application/json", bytes.NewReader(body))
	if err != nil {
		log.Printf("[alerting] failed to send Discord alert: %v", err)
		return err
	}
	defer resp.Body.Close()
	return nil
}
