package service

import (
	"strings"
	"testing"
)

// The bug this file exists for: with SMTP unset, sendEmail printed a line and
// returned nil, so every password reset was answered with "check your email"
// while nothing left the process and nothing said so.
func TestUnconfiguredMailerFailsInProduction(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	svc := &smtpEmailService{fromName: "Loci"}

	err := svc.SendPasswordResetEmail("someone@example.com", "Someone", "tok")
	if err == nil {
		t.Fatal("an unconfigured mailer reported success in production")
	}
	for _, want := range []string{"SMTP_HOST", "SMTP_PORT", "FROM_EMAIL", "FRONTEND_URL"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should name %s, got %q", want, err)
		}
	}
}

// Outside production it stays a no-op, so running locally needs no SMTP server.
func TestUnconfiguredMailerIsANoOpOutsideProduction(t *testing.T) {
	t.Setenv("APP_ENV", "development")
	svc := &smtpEmailService{}

	if err := svc.SendPasswordResetEmail("someone@example.com", "Someone", "tok"); err != nil {
		t.Fatalf("local runs should not require SMTP: %v", err)
	}
}

// FRONTEND_URL counts as configuration: unset, every link renders with an empty
// host, so the mail sends and goes nowhere.
func TestAMissingFrontendURLCountsAsUnconfigured(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	svc := &smtpEmailService{
		smtpHost: "smtp.resend.com", smtpPort: "587", fromEmail: "noreply@lociai.fyi",
	}

	err := svc.SendPasswordResetEmail("someone@example.com", "Someone", "tok")
	if err == nil || !strings.Contains(err.Error(), "FRONTEND_URL") {
		t.Fatalf("want a FRONTEND_URL complaint, got %v", err)
	}
}

// The reset link used to point at /reset-password. The route is
// /auth/reset-password, so every reset mail ever sent would have 404'd.
func TestLinksPointAtRoutesThatExist(t *testing.T) {
	svc := &smtpEmailService{frontendURL: "https://lociai.fyi"}

	for name, got := range map[string]string{
		"reset":  svc.resetBody("Someone", "tok"),
		"verify": svc.verifyBody("Someone", "tok"),
	} {
		if !strings.Contains(got, "https://lociai.fyi/auth/") {
			t.Errorf("%s link is not under /auth/: %s", name, firstLink(got))
		}
	}
}

// The welcome mail described a skill-exchange product, not this one.
func TestWelcomeEmailIsAboutThisProduct(t *testing.T) {
	svc := &smtpEmailService{frontendURL: "https://lociai.fyi"}
	got := svc.welcomeBody("Someone")

	for _, gone := range []string{"skill", "Skill", "match with other users", "teaching"} {
		if strings.Contains(got, gone) {
			t.Errorf("welcome email still mentions %q", gone)
		}
	}
	if !strings.Contains(got, "https://lociai.fyi/discover") {
		t.Error("welcome email should lead somewhere that exists")
	}
}

func firstLink(body string) string {
	i := strings.Index(body, "https://")
	if i < 0 {
		return "(no link)"
	}
	j := strings.IndexAny(body[i:], `"< `)
	return body[i : i+j]
}
