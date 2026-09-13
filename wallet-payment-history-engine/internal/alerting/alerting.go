// Package alerting sends operational alerts (stream errors, panics
// recovered, stalled progress) to an external channel - the same
// Notifier-interface-plus-Discord-default pattern wallet-backend's own
// internal/alerting package uses, kept as an independent copy here since
// this is a separate deployable with its own go.mod.
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
