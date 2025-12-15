package service

import (
	"fmt"
	"os"

	"github.com/twilio/twilio-go"
	verify "github.com/twilio/twilio-go/rest/verify/v2"
)

// TwilioConfig holds Twilio configuration
type TwilioConfig struct {
	AccountSID string
	AuthToken  string
	VerifySID  string // Twilio Verify Service SID
}

// PhoneService handles phone verification via Twilio Verify
type PhoneService struct {
	client    *twilio.RestClient
	verifySID string
	enabled   bool
}

// NewPhoneService creates a new phone verification service
func NewPhoneService(config TwilioConfig) *PhoneService {
	if config.AccountSID == "" || config.AuthToken == "" || config.VerifySID == "" {
		// Return disabled service if not configured
		return &PhoneService{enabled: false}
	}

	client := twilio.NewRestClientWithParams(twilio.ClientParams{
		Username: config.AccountSID,
		Password: config.AuthToken,
	})

	return &PhoneService{
		client:    client,
		verifySID: config.VerifySID,
		enabled:   true,
	}
}

// LoadTwilioConfigFromEnv loads Twilio configuration from environment variables
func LoadTwilioConfigFromEnv() TwilioConfig {
	return TwilioConfig{
		AccountSID: os.Getenv("TWILIO_ACCOUNT_SID"),
		AuthToken:  os.Getenv("TWILIO_AUTH_TOKEN"),
		VerifySID:  os.Getenv("TWILIO_VERIFY_SID"),
	}
}

// IsEnabled returns whether phone verification is configured
func (s *PhoneService) IsEnabled() bool {
	return s.enabled
}

// SendVerification sends an SMS verification code to the phone number
func (s *PhoneService) SendVerification(phoneNumber string) error {
	if !s.enabled {
		return fmt.Errorf("phone verification is not configured")
	}

	params := &verify.CreateVerificationParams{}
	params.SetTo(phoneNumber)
	params.SetChannel("sms")

	_, err := s.client.VerifyV2.CreateVerification(s.verifySID, params)
	if err != nil {
		return fmt.Errorf("failed to send verification: %w", err)
	}

	return nil
}

// CheckVerification verifies the code entered by the user
// Returns true if the code is valid, false otherwise
func (s *PhoneService) CheckVerification(phoneNumber, code string) (bool, error) {
	if !s.enabled {
		return false, fmt.Errorf("phone verification is not configured")
	}

	params := &verify.CreateVerificationCheckParams{}
	params.SetTo(phoneNumber)
	params.SetCode(code)

	resp, err := s.client.VerifyV2.CreateVerificationCheck(s.verifySID, params)
	if err != nil {
		return false, fmt.Errorf("failed to verify code: %w", err)
	}

	return resp.Status != nil && *resp.Status == "approved", nil
}
