package service

import (
	"fmt"
	"net/smtp"
	"os"
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

// NewEmailService creates a new email service
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

// SendVerificationEmail sends an email verification link
func (s *smtpEmailService) SendVerificationEmail(toEmail, toName, token string) error {
	subject := "Verify Your Email - loci"
	verificationLink := fmt.Sprintf("%s/verify-email?token=%s", s.frontendURL, token)

	body := fmt.Sprintf(`
<!DOCTYPE html>
<html>
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
</head>
<body style="font-family: Arial, sans-serif; line-height: 1.6; color: #333; max-width: 600px; margin: 0 auto; padding: 20px;">
    <div style="background-color: #f8f9fa; border-radius: 10px; padding: 30px;">
        <h1 style="color: #4a5568; margin-bottom: 20px;">Welcome to loci!</h1>
        <p>Hi %s,</p>
        <p>Thank you for registering with loci. Please verify your email address by clicking the button below:</p>
        <div style="text-align: center; margin: 30px 0;">
            <a href="%s" style="background-color: #4f46e5; color: white; padding: 12px 30px; text-decoration: none; border-radius: 5px; display: inline-block;">Verify Email</a>
        </div>
        <p style="color: #6b7280; font-size: 14px;">Or copy and paste this link into your browser:</p>
        <p style="word-break: break-all; color: #6b7280; font-size: 12px;">%s</p>
        <p style="margin-top: 30px; color: #6b7280; font-size: 14px;">This link will expire in 24 hours.</p>
        <p style="margin-top: 30px; color: #6b7280; font-size: 12px;">If you didn't create an account, please ignore this email.</p>
    </div>
</body>
</html>
	`, toName, verificationLink, verificationLink)

	return s.sendEmail(toEmail, subject, body)
}

// SendPasswordResetEmail sends a password reset link
func (s *smtpEmailService) SendPasswordResetEmail(toEmail, toName, token string) error {
	subject := "Reset Your Password - loci"
	resetLink := fmt.Sprintf("%s/reset-password?token=%s", s.frontendURL, token)

	body := fmt.Sprintf(`
<!DOCTYPE html>
<html>
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
</head>
<body style="font-family: Arial, sans-serif; line-height: 1.6; color: #333; max-width: 600px; margin: 0 auto; padding: 20px;">
    <div style="background-color: #f8f9fa; border-radius: 10px; padding: 30px;">
        <h1 style="color: #4a5568; margin-bottom: 20px;">Password Reset Request</h1>
        <p>Hi %s,</p>
        <p>We received a request to reset your password. Click the button below to create a new password:</p>
        <div style="text-align: center; margin: 30px 0;">
            <a href="%s" style="background-color: #dc2626; color: white; padding: 12px 30px; text-decoration: none; border-radius: 5px; display: inline-block;">Reset Password</a>
        </div>
        <p style="color: #6b7280; font-size: 14px;">Or copy and paste this link into your browser:</p>
        <p style="word-break: break-all; color: #6b7280; font-size: 12px;">%s</p>
        <p style="margin-top: 30px; color: #6b7280; font-size: 14px;">This link will expire in 1 hour.</p>
        <p style="margin-top: 30px; color: #dc2626; font-size: 12px; font-weight: bold;">If you didn't request a password reset, please ignore this email or contact support if you have concerns.</p>
    </div>
</body>
</html>
	`, toName, resetLink, resetLink)

	return s.sendEmail(toEmail, subject, body)
}

// SendWelcomeEmail sends a welcome email after successful registration
func (s *smtpEmailService) SendWelcomeEmail(toEmail, toName string) error {
	subject := "Welcome to loci!"

	body := fmt.Sprintf(`
<!DOCTYPE html>
<html>
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
</head>
<body style="font-family: Arial, sans-serif; line-height: 1.6; color: #333; max-width: 600px; margin: 0 auto; padding: 20px;">
    <div style="background-color: #f8f9fa; border-radius: 10px; padding: 30px;">
        <h1 style="color: #4a5568; margin-bottom: 20px;">🎉 Welcome to loci!</h1>
        <p>Hi %s,</p>
        <p>Your email has been verified successfully! You can now:</p>
        <ul style="margin: 20px 0;">
            <li>Create your skill profile</li>
            <li>Match with other users</li>
            <li>Schedule skill exchange sessions</li>
            <li>Start learning and teaching</li>
        </ul>
        <div style="text-align: center; margin: 30px 0;">
            <a href="%s/dashboard" style="background-color: #10b981; color: white; padding: 12px 30px; text-decoration: none; border-radius: 5px; display: inline-block;">Get Started</a>
        </div>
        <p style="margin-top: 30px;">Happy learning!</p>
        <p>The loci Team</p>
    </div>
</body>
</html>
	`, toName, s.frontendURL)

	return s.sendEmail(toEmail, subject, body)
}

// sendEmail is a helper function to send emails via SMTP
func (s *smtpEmailService) sendEmail(to, subject, body string) error {
	// If SMTP is not configured, log and skip (for development)
	if s.smtpHost == "" || s.smtpPort == "" {
		fmt.Printf("Email would be sent to %s with subject: %s\n", to, subject)
		return nil
	}

	auth := smtp.PlainAuth("", s.smtpUsername, s.smtpPassword, s.smtpHost)

	from := fmt.Sprintf("%s <%s>", s.fromName, s.fromEmail)

	message := []byte(fmt.Sprintf("From: %s\r\n"+
		"To: %s\r\n"+
		"Subject: %s\r\n"+
		"MIME-Version: 1.0\r\n"+
		"Content-Type: text/html; charset=UTF-8\r\n"+
		"\r\n"+
		"%s\r\n", from, to, subject, body))

	addr := fmt.Sprintf("%s:%s", s.smtpHost, s.smtpPort)
	return smtp.SendMail(addr, auth, s.fromEmail, []string{to}, message)
}
