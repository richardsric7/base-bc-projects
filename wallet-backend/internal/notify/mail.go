// Package notify abstracts outbound email and SMS behind small interfaces so
// a provider can be swapped (or added) by writing one file, never by
// touching a component's business logic.
//
// The upstream project this template is derived from hard-wired Mailgun and
// Infobip/Termii SDKs directly into the mail/sms packages. This base version
// deliberately does not vendor any paid provider's SDK - ship with a
// zero-dependency SMTP mailer and a console SMS logger, and add a real SMS
// gateway (Twilio, Infobip, Termii, ...) by implementing SMSProvider below.
package notify

import (
	"fmt"
	"log"
	"net/smtp"
)

// Mailer sends transactional email (OTP codes, notices, receipts).
type Mailer interface {
	Send(to, subject, body string) error
}

// ConsoleMailer logs emails instead of sending them - the default for local
// development so no SMTP credentials are required to boot the service.
type ConsoleMailer struct{}

func NewConsoleMailer() Mailer { return ConsoleMailer{} }

func (ConsoleMailer) Send(to, subject, body string) error {
	log.Printf("[mail:console] to=%s subject=%q body=%q", to, subject, body)
	return nil
}

// SMTPMailer sends mail through any standard SMTP server (Mailtrap for
// testing, Gmail/SES/Postmark/Mailgun's SMTP endpoint, etc. in production).
type SMTPMailer struct {
	host, port, username, password, from string
}

func NewSMTPMailer(host, port, username, password, from string) Mailer {
	return &SMTPMailer{host: host, port: port, username: username, password: password, from: from}
}

func (m *SMTPMailer) Send(to, subject, body string) error {
	msg := fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\n\r\n%s", m.from, to, subject, body)
	auth := smtp.PlainAuth("", m.username, m.password, m.host)
	addr := fmt.Sprintf("%s:%s", m.host, m.port)
	return smtp.SendMail(addr, auth, m.from, []string{to}, []byte(msg))
}
