package rules

import (
	"context"
	"path"
	"strings"

	"github.com/cp0x-org/xpaywall/xgateway/internal/config"
)

var _ Provider = (*FileProvider)(nil)

type FileProvider struct {
	rules []Rule
}

func NewFileProvider(cfg *config.Config) *FileProvider {
	if cfg == nil {
		return &FileProvider{}
	}

	configRules := cfg.PathRules()
	loadedRules := make([]Rule, 0, len(configRules))
	for _, rule := range configRules {
		loadedRules = append(loadedRules, fromConfigRule(cfg, rule))
	}

	return &FileProvider{
		rules: loadedRules,
	}
}

func (r *FileProvider) GetByInboundPath(_ context.Context, inboundPath string) (*Rule, error) {
	normalized := normalizePath(inboundPath)
	for i, rule := range r.rules {
		if matchPath(rule.InboundPath, normalized) {
			return &r.rules[i], nil
		}
	}
	return nil, nil
}

func fromConfigRule(cfg *config.Config, rule config.Rule) Rule {
	r := Rule{
		Name:            rule.Name,
		InboundPath:     rule.PathValue(),
		Price:           rule.Price,
		Description:     rule.Description,
		Free:            rule.Free,
		PaymentChannels: resolveChannels(cfg, rule),
		Bazaar:          fromConfigBazaar(rule.Bazaar),
		Target:          cfg.Outbound.Target,
		AllowUnmatched:  cfg.Outbound.AllowUnmatched,
	}
	if cfg.Outbound.AuthHeader.Enable && cfg.Outbound.AuthHeader.Name != "" {
		r.AuthHeaderName = cfg.Outbound.AuthHeader.Name
		r.AuthHeaderValue = cfg.Outbound.AuthHeader.Value
	}
	return r
}

func fromConfigBazaar(b *config.BazaarConfig) *BazaarConfig {
	if b == nil {
		return nil
	}
	return &BazaarConfig{
		Disabled:      b.Disabled,
		Method:        b.Method,
		BodyType:      b.BodyType,
		InputExample:  b.InputExample,
		InputSchema:   b.InputSchema,
		OutputExample: b.OutputExample,
		OutputSchema:  b.OutputSchema,
	}
}

func resolveChannels(cfg *config.Config, rule config.Rule) []*PaymentChannel {
	methods, err := cfg.ResolveRulePaymentMethods(rule)
	if err != nil || len(methods) == 0 {
		return nil
	}

	channels := make([]*PaymentChannel, 0, len(methods))
	for _, m := range methods {
		switch {
		case m.IsX402():
			channels = append(channels, &PaymentChannel{
				Protocol: "x402",
				Scheme:   m.X402.Scheme,
				Decimals: m.X402.Decimals,
				Enabled:  true,
				ChannelConfig: map[string]string{
					"facilitator_url": m.X402.FacilitatorURL,
					"network":         m.X402.Network,
					"merchant":        m.X402.Merchant,
					"asset":           m.X402.Asset,
				},
			})
			//case m.IsMPP():
			//	channels = append(channels, &PaymentChannel{
			//		Protocol: "mpp",
			//		Method:   m.MPP.Method,
			//		Scheme:   m.MPP.Scheme,
			//		Enabled:  true,
			//		ChannelConfig: map[string]string{
			//			"rpc_url":    m.MPP.RPCURL,
			//			"merchant":   m.MPP.Merchant,
			//			"asset":      m.MPP.Asset,
			//			"secret_key": m.MPP.SecretKey,
			//		},
			//	})
		}
	}
	return channels
}

func matchPath(pattern, p string) bool {
	pattern = normalizePath(pattern)
	ok, _ := path.Match(pattern, p)
	return ok
}

func normalizePath(p string) string {
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	if p != "/" {
		p = strings.TrimRight(p, "/")
	}
	return p
}
