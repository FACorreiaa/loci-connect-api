package service

import "testing"

func TestPhoneService_DisabledWhenUnconfigured(t *testing.T) {
	svc := NewPhoneService(TwilioConfig{})

	if svc.IsEnabled() {
		t.Fatal("phone service should be disabled with empty config")
	}

	if err := svc.SendVerification("+15555550123"); err == nil {
		t.Error("SendVerification on disabled service should error")
	}

	ok, err := svc.CheckVerification("+15555550123", "123456")
	if err == nil {
		t.Error("CheckVerification on disabled service should error")
	}
	if ok {
		t.Error("CheckVerification on disabled service should return false")
	}
}

func TestPhoneService_PartialConfigStaysDisabled(t *testing.T) {
	// Missing VerifySID -> disabled.
	svc := NewPhoneService(TwilioConfig{AccountSID: "AC123", AuthToken: "tok"})
	if svc.IsEnabled() {
		t.Fatal("phone service should be disabled when VerifySID is missing")
	}
}

func TestPhoneService_EnabledWhenFullyConfigured(t *testing.T) {
	// Constructor must not make network calls; only the send/check methods do.
	svc := NewPhoneService(TwilioConfig{
		AccountSID: "AC123",
		AuthToken:  "tok",
		VerifySID:  "VA123",
	})
	if !svc.IsEnabled() {
		t.Fatal("phone service should be enabled when fully configured")
	}
}
