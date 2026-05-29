package rules

import (
	"context"

	"github.com/google/uuid"
)

type Provider interface {
	GetByInboundPath(ctx context.Context, inboundPath string) (*Rule, error)
}

type Rule struct {
	ProjectID       uuid.UUID
	OutboundRouteID uuid.UUID

	Name            string
	InboundPath     string
	OutboundPath    string
	Price           string
	MimeType        string
	Description     string
	Free            bool
	PaymentChannels []*PaymentChannel
	Bazaar          *BazaarConfig

	Target          string
	AuthHeaderName  string
	AuthHeaderValue string
	AllowUnmatched  bool
}

// BazaarConfig holds runtime Bazaar discovery extension settings for a route.
// Mirrors config.BazaarConfig in the file provider; HTTP provider may populate
// it from control-api later.
type BazaarConfig struct {
	Disabled      bool
	Method        string
	BodyType      string
	InputExample  map[string]any
	InputSchema   map[string]any
	OutputExample map[string]any
	OutputSchema  map[string]any
}

type PaymentChannel struct {
	ID            uuid.UUID
	AssetID       *uuid.UUID
	Protocol      string
	Method        string
	Scheme        string
	Price         string
	Decimals      int32
	Enabled       bool
	ChannelConfig map[string]string
}

// ChannelConfig
// for x402 charge: asset address, asset symbol, amount, timeout, payto
// for tempo: asset, amount, timeout, deadline
