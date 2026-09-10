// Package sms delivers one-time codes and alerts to phones.
//
// The Sender interface exists so the auth service never knows which provider is
// configured. Swapping Twilio for a local Bangladeshi gateway is a change in
// this package and one environment variable — nothing in the auth flow moves.
package sms

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/johirdev/Hisabji-Server/internal/config"
	"github.com/johirdev/Hisabji-Server/internal/core/apperr"
	"github.com/johirdev/Hisabji-Server/internal/core/logger"
)

// Sender delivers a text message.
type Sender interface {
	// Send delivers body to phone. It returns an *apperr.Error on failure so
	// the caller can decide whether to surface it or swallow it.
	Send(ctx context.Context, phone, body string) error
	// Name identifies the provider in logs and health output.
	Name() string
}

// New builds the configured sender.
func New(cfg config.SMS, appCfg config.App) Sender {
	switch strings.ToLower(cfg.Provider) {
	case "log", "":
		return &LogSender{production: appCfg.IsProduction()}
	default:
		// A provider name we do not implement yet must not silently degrade to
		// logging in production — that would print OTP codes into the log and
		// send nothing to the user.
		if appCfg.IsProduction() {
			return &UnavailableSender{provider: cfg.Provider}
		}
		logger.Named("sms").Warn(
			"unknown SMS provider, falling back to log output",
			"provider", cfg.Provider)
		return &LogSender{production: false}
	}
}

// LogSender writes the message to the application log instead of sending it.
// It is the correct default for local development: no gateway account needed,
// and the OTP is right there in the terminal.
type LogSender struct{ production bool }

// Name identifies the provider.
func (s *LogSender) Name() string { return "log" }

// Send logs the message. In production it refuses, because logging an OTP is
// both a security problem and a silently broken signup flow.
func (s *LogSender) Send(ctx context.Context, phone, body string) error {
	if s.production {
		return apperr.Upstream("SMS delivery",
			fmt.Errorf("SMS_PROVIDER is 'log', which cannot deliver messages in production"))
	}
	logger.FromContext(ctx).Info("SMS (development only)",
		"to", maskPhone(phone),
		"body", body,
		"sent_at", time.Now().Format(time.RFC3339))
	return nil
}

// UnavailableSender fails every send with a clear operator-facing message.
type UnavailableSender struct{ provider string }

// Name identifies the provider.
func (s *UnavailableSender) Name() string { return s.provider + " (not implemented)" }

// Send always fails.
func (s *UnavailableSender) Send(ctx context.Context, phone, body string) error {
	return apperr.Upstream("SMS delivery",
		fmt.Errorf("SMS provider %q is configured but not implemented", s.provider))
}

// maskPhone hides the middle digits, so support can match a number in a log
// without the log itself becoming a list of customer phone numbers.
func maskPhone(phone string) string {
	if len(phone) < 7 {
		return "***"
	}
	return phone[:3] + strings.Repeat("*", len(phone)-6) + phone[len(phone)-3:]
}

// OTPMessage renders the verification text in the user's language.
func OTPMessage(code, appName, locale string, ttl time.Duration) string {
	minutes := int(ttl.Minutes())
	if locale == "bn" {
		return fmt.Sprintf(
			"%s যাচাই কোড: %s। কোডটি %d মিনিট পর্যন্ত বৈধ। কোডটি কারো সাথে শেয়ার করবেন না।",
			appName, code, minutes)
	}
	return fmt.Sprintf(
		"%s verification code: %s. It expires in %d minutes. Never share this code with anyone.",
		appName, code, minutes)
}
