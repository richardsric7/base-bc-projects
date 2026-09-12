package notify

import "log"

// SMSProvider sends OTP/notification text messages.
type SMSProvider interface {
	Send(to, message string) error
}

// ConsoleSMSProvider logs messages instead of sending them - the default so
// the service boots without any SMS gateway credentials. Implement this
// interface for a real provider (Twilio, Infobip, Termii, ...) and select it
// via config; see the "SMS_PROVIDER" example in .env.example.
type ConsoleSMSProvider struct{}

func NewConsoleSMSProvider() SMSProvider { return ConsoleSMSProvider{} }

func (ConsoleSMSProvider) Send(to, message string) error {
	log.Printf("[sms:console] to=%s message=%q", to, message)
	return nil
}
