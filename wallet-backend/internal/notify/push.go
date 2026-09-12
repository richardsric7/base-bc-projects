package notify

import "log"

// PushProvider sends mobile push notifications. The upstream project this
// template is derived from wired Firebase Cloud Messaging directly into
// main.go's boot sequence, with a hard log.Fatalln if it failed to
// initialize - meaning the whole API refused to start without a Firebase
// project configured. Here, push is optional: NoopPushProvider is the
// default so the service boots with nothing configured, and a real
// implementation (Firebase, APNs/FCM directly, OneSignal, ...) is added by
// implementing this interface and wiring it in internal/sharedconfig.
type PushProvider interface {
	// Send delivers a single push notification to one device token.
	Send(deviceToken, title, body string) error
}

// NoopPushProvider silently drops every notification. Useful for local dev
// and for deployments that don't need push yet.
type NoopPushProvider struct{}

func NewNoopPushProvider() PushProvider { return NoopPushProvider{} }

func (NoopPushProvider) Send(string, string, string) error { return nil }

// ConsolePushProvider logs notifications instead of sending them - handy for
// local development when you want to see that the call site fires correctly.
type ConsolePushProvider struct{}

func NewConsolePushProvider() PushProvider { return ConsolePushProvider{} }

func (ConsolePushProvider) Send(deviceToken, title, body string) error {
	log.Printf("[push:console] to=%s title=%q body=%q", deviceToken, title, body)
	return nil
}
