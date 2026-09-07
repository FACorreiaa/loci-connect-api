package service

import (
	"fmt"
	"net"
	"net/smtp"
	"os"
	"sort"
	"strings"

	"github.com/FACorreiaa/loci-connect-api/pkg/config"
)

// Brand values, sampled from the approved art. Kept as literals rather than
// imported because an email is rendered by whatever mail client opens it: no
// CSS variables, no stylesheet, no dark mode. See
// loci-client/public/images/brand/README.md.
const (
	brandCream = "#FDF5EA"
	brandInk   = "#323B42"
	brandMuted = "#5F6469"
	// Not the brand coral (#FA7862): that measures 2.4:1 against white and
	// fails as a ground for a button label. This is the deeper coral the app
	// uses for the same reason — 4.8:1 under white text.
	brandCoral = "#C63C24"
)

// EmailSender defines the behavior required for sending auth emails.
type EmailSender interface {
	SendVerificationEmail(toEmail, toName, token string) error
	SendPasswordResetEmail(toEmail, toName, token string) error
	SendWelcomeEmail(toEmail, toName string) error
}

type smtpEmailService struct {
	smtpHost     string
	smtpPort     string
	smtpUsername string
	smtpPassword string
	fromEmail    string
	fromName     string
	frontendURL  string
}

// NewEmailService creates a new email service.
func NewEmailService() EmailSender {
	return &smtpEmailService{
		smtpHost:     os.Getenv("SMTP_HOST"),
		smtpPort:     os.Getenv("SMTP_PORT"),
		smtpUsername: os.Getenv("SMTP_USERNAME"),
		smtpPassword: os.Getenv("SMTP_PASSWORD"),
		fromEmail:    os.Getenv("FROM_EMAIL"),
		fromName:     os.Getenv("FROM_NAME"),
		frontendURL:  os.Getenv("FRONTEND_URL"),
	}
}

// layout wraps body content in the shell every one of these mails shares.
//
// Table-based and inline-styled on purpose: Outlook and several webmail clients
// strip <style> blocks and ignore flexbox, so anything structural has to be an
// attribute.
func layout(heading, body string) string {
	return fmt.Sprintf(`<!DOCTYPE html>
<html>
<head><meta charset="UTF-8"><meta name="viewport" content="width=device-width, initial-scale=1.0"></head>
<body style="margin:0;padding:0;background-color:%[1]s;">
  <table role="presentation" width="100%%" cellpadding="0" cellspacing="0" style="background-color:%[1]s;padding:24px 12px;">
    <tr><td align="center">
      <table role="presentation" width="100%%" cellpadding="0" cellspacing="0" style="max-width:560px;background-color:#FFFDFA;border-radius:16px;padding:32px;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Helvetica,Arial,sans-serif;color:%[2]s;line-height:1.6;">
        <tr><td>
          <p style="margin:0 0 24px;font-size:20px;font-weight:700;letter-spacing:-0.01em;color:%[2]s;">Loci</p>
          <h1 style="margin:0 0 16px;font-size:22px;font-weight:700;color:%[2]s;">%[3]s</h1>
          %[4]s
        </td></tr>
      </table>
    </td></tr>
  </table>
</body>
</html>`, brandCream, brandInk, heading, body)
}

// button renders the one call to action a mail should have.
func button(href, label string) string {
	return fmt.Sprintf(
		`<table role="presentation" cellpadding="0" cellspacing="0" style="margin:28px 0;">`+
			`<tr><td align="center" style="border-radius:10px;background-color:%s;">`+
			`<a href="%s" style="display:inline-block;padding:12px 28px;color:%s;text-decoration:none;font-weight:600;">%s</a>`+
			`</td></tr></table>`,
		brandCoral, href, brandCream, label,
	)
}

// fallbackLink repeats the URL as text. Some clients strip or rewrite anchors,
// and a person who cannot click still needs the link.
func fallbackLink(href string) string {
	return fmt.Sprintf(
		`<p style="margin:0;font-size:13px;color:%s;">Or paste this into your browser:</p>`+
			`<p style="margin:4px 0 0;font-size:12px;word-break:break-all;color:%s;">%s</p>`,
		brandMuted, brandMuted, href,
	)
}

// SendVerificationEmail sends an email verification link.
//
// Currently unreachable by design — see sendEmailVerification in auth_service.go
// for why nothing calls it. Kept whole so that turning verification on is a
// matter of exposing the RPC and adding the route, not rewriting this.
func (s *smtpEmailService) SendVerificationEmail(toEmail, toName, token string) error {
	return s.sendEmail(toEmail, "Confirm your email - Loci",
		layout("Confirm your email", s.verifyBody(toName, token)))
}

// verifyBody is split out so the template can be asserted on without sending.
func (s *smtpEmailService) verifyBody(toName, token string) string {
	link := fmt.Sprintf("%s/auth/verify-email?token=%s", s.frontendURL, token)
	return fmt.Sprintf(
		`<p style="margin:0 0 8px;">Hi %s,</p>`+
			`<p style="margin:0;">Confirm this address to finish setting up your Loci account.</p>`+
			`%s%s`+
			`<p style="margin:24px 0 0;font-size:13px;color:%s;">This link expires in 24 hours. `+
			`If you didn't create a Loci account, you can ignore this.</p>`,
		toName, button(link, "Confirm email"), fallbackLink(link), brandMuted,
	)
}

// SendPasswordResetEmail sends a password reset link.
func (s *smtpEmailService) SendPasswordResetEmail(toEmail, toName, token string) error {
	return s.sendEmail(toEmail, "Reset your password - Loci",
		layout("Reset your password", s.resetBody(toName, token)))
}

func (s *smtpEmailService) resetBody(toName, token string) string {
	// /auth/reset-password, not /reset-password: the route is under /auth, and
	// the old path 404'd on every reset anyone ever requested.
	link := fmt.Sprintf("%s/auth/reset-password?token=%s", s.frontendURL, token)
	return fmt.Sprintf(
		`<p style="margin:0 0 8px;">Hi %s,</p>`+
			`<p style="margin:0;">Use the button below to choose a new password.</p>`+
			`%s%s`+
			`<p style="margin:24px 0 0;font-size:13px;color:%s;">This link expires in one hour. `+
			`If you didn't ask to reset your password, ignore this email — your password has not changed.</p>`,
		toName, button(link, "Choose a new password"), fallbackLink(link), brandMuted,
	)
}

// SendWelcomeEmail sends a welcome email after successful verification.
func (s *smtpEmailService) SendWelcomeEmail(toEmail, toName string) error {
	return s.sendEmail(toEmail, "Welcome to Loci", layout("Welcome to Loci", s.welcomeBody(toName)))
}

func (s *smtpEmailService) welcomeBody(toName string) string {
	// This used to offer to help you "match with other users" and "schedule
	// skill exchange sessions", which is a different product entirely.
	link := s.frontendURL + "/discover"
	return fmt.Sprintf(
		`<p style="margin:0 0 8px;">Hi %s,</p>`+
			`<p style="margin:0;">Your account is ready. Tell Loci a city and a mood, and it plots a `+
			`route through real places — ordered, mapped, and yours to reshape.</p>`+
			`%s`+
			`<p style="margin:24px 0 0;font-size:13px;color:%s;">Search is free. Chat, saved trips and `+
			`sync come with an account, which you now have.</p>`,
		toName, button(link, "Plan something"), brandMuted,
	)
}

// configured reports whether this service can actually deliver, and says what
// is missing when it cannot.
func (s *smtpEmailService) configured() error {
	var missing []string
	for name, value := range map[string]string{
		"SMTP_HOST":    s.smtpHost,
		"SMTP_PORT":    s.smtpPort,
		"FROM_EMAIL":   s.fromEmail,
		"FRONTEND_URL": s.frontendURL,
	} {
		if strings.TrimSpace(value) == "" {
			missing = append(missing, name)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	sort.Strings(missing)
	return fmt.Errorf("email is not configured: %s unset", strings.Join(missing, ", "))
}

// sendEmail delivers one message over SMTP.
//
// An unconfigured mailer is an ERROR in production, not a no-op. It used to
// print "Email would be sent to ..." and return nil, so with SMTP_HOST unset
// every password reset ever requested was answered with "check your email"
// while nothing left the process — and nothing anywhere said so. The caller
// logs what this returns; a silent success cannot be logged at all.
//
// FRONTEND_URL counts as configuration for the same reason. Unset, the links
// above render with an empty host, so the mail sends and goes nowhere.
//
// Outside production it stays a no-op, deliberately: running the app locally
// should not require an SMTP server, and the log line carries the link so a
// developer can follow the flow by copying it out of the terminal.
func (s *smtpEmailService) sendEmail(to, subject, body string) error {
	if err := s.configured(); err != nil {
		if config.IsProduction() {
			return err
		}
		fmt.Printf("[email] would send to %s: %s (%v)\n", to, subject, err)
		return nil
	}

	from := fmt.Sprintf("%s <%s>", s.fromName, s.fromEmail)
	message := fmt.Appendf(nil, "From: %s\r\n"+
		"To: %s\r\n"+
		"Subject: %s\r\n"+
		"MIME-Version: 1.0\r\n"+
		"Content-Type: text/html; charset=UTF-8\r\n"+
		"\r\n"+
		"%s\r\n", from, to, subject, body)

	addr := net.JoinHostPort(s.smtpHost, s.smtpPort)

	// Resend, and most hosted relays, require authentication. A blank username
	// would otherwise be sent as an anonymous PLAIN attempt and rejected with a
	// message about credentials rather than about configuration.
	var auth smtp.Auth
	if s.smtpUsername != "" {
		auth = smtp.PlainAuth("", s.smtpUsername, s.smtpPassword, s.smtpHost)
	}

	if err := smtp.SendMail(addr, auth, s.fromEmail, []string{to}, message); err != nil {
		return fmt.Errorf("send to %s via %s: %w", to, addr, err)
	}
	return nil
}
