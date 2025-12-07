package notify

import (
	"log"
	"time"

	gomail "gopkg.in/mail.v2"
)

// EmailConfig holds SMTP configuration for sending emails.
type EmailConfig struct {
	SMTPServer string
	SMTPPort   int
	SMTPUser   string
	SMTPPass   string
	FromEmail  string
	ToEmail    string
	Enabled    bool
}

// EmailSender delivers messages via SMTP.
type EmailSender struct {
	cfg EmailConfig
}

// NewEmailSender creates a sender with the given SMTP configuration.
func NewEmailSender(cfg EmailConfig) *EmailSender {
	return &EmailSender{cfg: cfg}
}

// Send delivers an email with HTML body and plain text fallback.
func (s *EmailSender) Send(msg *RenderedMessage) error {
	if !s.cfg.Enabled {
		return nil
	}

	m := gomail.NewMessage()
	m.SetHeader("From", s.cfg.FromEmail)
	m.SetHeader("To", s.cfg.ToEmail)
	m.SetHeader("Subject", msg.Subject)

	if msg.HTML != "" && msg.Text != "" {
		m.SetBody("text/plain", msg.Text)
		m.AddAlternative("text/html", msg.HTML)
	} else if msg.HTML != "" {
		m.SetBody("text/html", msg.HTML)
	} else {
		m.SetBody("text/plain", msg.Text)
	}

	dialer := gomail.NewDialer(s.cfg.SMTPServer, s.cfg.SMTPPort, s.cfg.SMTPUser, s.cfg.SMTPPass)
	dialer.Timeout = 10 * time.Second

	if err := dialer.DialAndSend(m); err != nil {
		log.Printf("Email error: failed to send to %s (Subject: %s): %v", s.cfg.ToEmail, msg.Subject, err)
		return err
	}

	log.Printf("Email sent: %s", msg.Subject)
	return nil
}
