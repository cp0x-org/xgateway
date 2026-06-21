package config

import (
	"strings"
	"testing"
)

func TestValidateRejectsUnsupportedX402MethodScheme(t *testing.T) {
	t.Parallel()

	cfg := DefaultConfig()
	cfg.Outbound.Target = "http://localhost:4021"
	cfg.X402 = []X402Method{{
		Name:           "base",
		FacilitatorURL: "https://x402.org/facilitator",
		Network:        "eip155:84532",
		Scheme:         "invalid",
		Merchant:       "0xabc",
	}}

	err := Validate(cfg)
	if err == nil {
		t.Fatal("expected validation error for unsupported x402 scheme")
	}
	if !strings.Contains(err.Error(), "x402[0].scheme") {
		t.Fatalf("expected x402[0].scheme in error, got %v", err)
	}
}

func TestValidateRejectsUnknownPaymentMethod(t *testing.T) {
	t.Parallel()

	cfg := DefaultConfig()
	cfg.Outbound.Target = "http://localhost:4021"
	cfg.X402 = []X402Method{{
		Name:           "base",
		FacilitatorURL: "https://x402.org/facilitator",
		Network:        "eip155:84532",
		Scheme:         "exact",
		Merchant:       "0xabc",
	}}
	cfg.Outbound.Rules = []Rule{
		{Path: "/metered", PaymentMethods: []string{"missing"}},
	}

	err := Validate(cfg)
	if err == nil {
		t.Fatal("expected validation error for unknown payment method")
	}
	if !strings.Contains(err.Error(), "outbound.rules[0].payment_methods") {
		t.Fatalf("expected outbound.rules[0].payment_methods in error, got %v", err)
	}
}

func TestDefaultPaymentMethodNamesSkipsUnsupportedDefinitions(t *testing.T) {
	t.Parallel()

	cfg := DefaultConfig()
	cfg.X402 = []X402Method{{
		Name:           "base-exact",
		FacilitatorURL: "https://x402.org/facilitator",
		Network:        "eip155:84532",
		Scheme:         "exact",
		Merchant:       "0xabc",
	}}
	cfg.MPP = []MPPMethod{
		{Name: "tempo-charge", Method: "tempo", Scheme: "charge", Merchant: "0xabc"},
		{Name: "tempo-session", Method: "tempo", Scheme: "session", Merchant: "0xabc"},
		{Name: "stripe-charge", Method: "stripe", Scheme: "charge", Merchant: "acct_123"},
	}
	ApplyDefaults(cfg)

	got := cfg.DefaultPaymentMethodNames()
	if len(got) != 2 {
		t.Fatalf("len(DefaultPaymentMethodNames()) = %d, want 2", len(got))
	}
	if got[0] != "base-exact" || got[1] != "tempo-charge" {
		t.Fatalf("DefaultPaymentMethodNames() = %v", got)
	}
}
