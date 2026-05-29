package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

const EnvPrefix = "X402PROXY"

const (
	SchemeExact   = "exact"
	SchemeUpto    = "upto"
	SchemeCharge  = "charge"
	SchemeSession = "session"

	MPPMethodTempo = "tempo"
)

const (
	defaultX402Timeout  = 30
	defaultMPPTimeout   = 30
	defaultMPPSecretKey = "xgateway-dev-secret"
	defaultAuthHeader   = "Authorization"
)

type Config struct {
	X402 []X402Method `json:"x402" yaml:"x402" mapstructure:"x402"`
	//MPP      []MPPMethod    `json:"mpp" yaml:"mpp" mapstructure:"mpp"`
	Outbound OutboundConfig `json:"outbound" yaml:"outbound" mapstructure:"outbound"`
}

type OutboundConfig struct {
	Target         string           `json:"target" yaml:"target" mapstructure:"target"`
	AuthHeader     AuthHeaderConfig `json:"auth_header" yaml:"auth_header" mapstructure:"auth_header"`
	AllowUnmatched bool             `json:"allow_unmatched" yaml:"allow_unmatched" mapstructure:"allow_unmatched"`
	Rules          []Rule           `json:"rules" yaml:"rules" mapstructure:"rules"`
}

type AuthHeaderConfig struct {
	Enable bool   `json:"enable" yaml:"enable" mapstructure:"enable"`
	Name   string `json:"name" yaml:"name" mapstructure:"name"`
	Value  string `json:"value" yaml:"value" mapstructure:"value"`
}

type X402Method struct {
	Name                   string `json:"name" yaml:"name" mapstructure:"name"`
	FacilitatorURL         string `json:"facilitator_url" yaml:"facilitator_url" mapstructure:"facilitator_url"`
	Network                string `json:"network" yaml:"network" mapstructure:"network"`
	Scheme                 string `json:"scheme" yaml:"scheme" mapstructure:"scheme"`
	SyncFacilitatorOnStart bool   `json:"sync_facilitator_on_start" yaml:"sync_facilitator_on_start" mapstructure:"sync_facilitator_on_start"`
	TimeoutSeconds         int    `json:"timeout_seconds" yaml:"timeout_seconds" mapstructure:"timeout_seconds"`
	Merchant               string `json:"merchant" yaml:"merchant" mapstructure:"merchant"`
	Asset                  string `json:"asset" yaml:"asset" mapstructure:"asset"`
	Decimals               int32  `json:"decimals" yaml:"decimals" mapstructure:"decimals"`
}

//type MPPMethod struct {
//	Name           string `json:"name" yaml:"name" mapstructure:"name"`
//	Method         string `json:"method" yaml:"method" mapstructure:"method"`
//	RPCURL         string `json:"rpc_url" yaml:"rpc_url" mapstructure:"rpc_url"`
//	Scheme         string `json:"scheme" yaml:"scheme" mapstructure:"scheme"`
//	TimeoutSeconds int    `json:"timeout_seconds" yaml:"timeout_seconds" mapstructure:"timeout_seconds"`
//	Merchant       string `json:"merchant" yaml:"merchant" mapstructure:"merchant"`
//	Asset          string `json:"asset" yaml:"asset" mapstructure:"asset"`
//	SecretKey      string `json:"secret_key" yaml:"secret_key" mapstructure:"secret_key"`
//}

type Rule struct {
	Name           string        `json:"name" yaml:"name" mapstructure:"name"`
	Path           string        `json:"path" yaml:"path" mapstructure:"path"`
	PathGlob       string        `json:"path_glob" yaml:"path_glob" mapstructure:"path_glob"`
	Price          string        `json:"price" yaml:"price" mapstructure:"price"`
	Description    string        `json:"description" yaml:"description" mapstructure:"description"`
	Free           bool          `json:"free" yaml:"free" mapstructure:"free"`
	PaymentMethods []string      `json:"payment_methods" yaml:"payment_methods" mapstructure:"payment_methods"`
	Bazaar         *BazaarConfig `json:"bazaar" yaml:"bazaar" mapstructure:"bazaar"`
}

// BazaarConfig describes how a route should be advertised via the x402 Bazaar
// discovery extension. When nil on a paid rule, a minimal declaration is
// auto-generated (method=GET, empty schema) so facilitators can still catalog
// the endpoint. Set fields here to give discovery clients richer metadata.
type BazaarConfig struct {
	Disabled      bool           `json:"disabled" yaml:"disabled" mapstructure:"disabled"`
	Method        string         `json:"method" yaml:"method" mapstructure:"method"`
	BodyType      string         `json:"body_type" yaml:"body_type" mapstructure:"body_type"`
	InputExample  map[string]any `json:"input_example" yaml:"input_example" mapstructure:"input_example"`
	InputSchema   map[string]any `json:"input_schema" yaml:"input_schema" mapstructure:"input_schema"`
	OutputExample map[string]any `json:"output_example" yaml:"output_example" mapstructure:"output_example"`
	OutputSchema  map[string]any `json:"output_schema" yaml:"output_schema" mapstructure:"output_schema"`
}

type ResolvedPaymentMethod struct {
	Name string
	X402 *X402Method
	//MPP  *MPPMethod
}

func (m ResolvedPaymentMethod) IsX402() bool { return m.X402 != nil }

//func (m ResolvedPaymentMethod) IsMPP() bool  { return m.MPP != nil }

func Load(configPath string) (*Config, error) {
	if configPath == "" {
		return nil, errors.New("service config file is required")
	}
	cfg := &Config{}

	if strings.TrimSpace(configPath) != "" {
		if err := loadFile(configPath, cfg); err != nil {
			return nil, err
		}
	}

	ApplyDefaults(cfg)
	if err := ValidateForReverseProxy(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

func DefaultConfig() *Config {
	return &Config{}
}

func ApplyDefaults(cfg *Config) {
	if cfg == nil {
		return
	}

	if cfg.Outbound.AuthHeader.Enable && strings.TrimSpace(cfg.Outbound.AuthHeader.Name) == "" {
		cfg.Outbound.AuthHeader.Name = defaultAuthHeader
	}

	for i := range cfg.X402 {
		if cfg.X402[i].Name == "" {
			cfg.X402[i].Name = fmt.Sprintf("x402-%d", i+1)
		}
		if NormalizeScheme(cfg.X402[i].Scheme) == "" {
			cfg.X402[i].Scheme = SchemeExact
		}
		if cfg.X402[i].TimeoutSeconds <= 0 {
			cfg.X402[i].TimeoutSeconds = defaultX402Timeout
		}
	}

	//for i := range cfg.MPP {
	//	if cfg.MPP[i].Name == "" {
	//		cfg.MPP[i].Name = fmt.Sprintf("mpp-%d", i+1)
	//	}
	//	if NormalizeMethod(cfg.MPP[i].Method) == "" {
	//		cfg.MPP[i].Method = MPPMethodTempo
	//	}
	//	if NormalizeScheme(cfg.MPP[i].Scheme) == "" {
	//		cfg.MPP[i].Scheme = SchemeCharge
	//	}
	//	if cfg.MPP[i].TimeoutSeconds <= 0 {
	//		cfg.MPP[i].TimeoutSeconds = defaultMPPTimeout
	//	}
	//	if strings.TrimSpace(cfg.MPP[i].SecretKey) == "" {
	//		cfg.MPP[i].SecretKey = defaultMPPSecretKey
	//	}
	//}
}

func Validate(cfg *Config) error {
	return validate(cfg, false)
}

func ValidateForReverseProxy(cfg *Config) error {
	return validate(cfg, true)
}

func loadFile(path string, cfg *Config) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".yaml", ".yml":
		if err := yaml.Unmarshal(b, cfg); err != nil {
			return err
		}
	case ".json":
		if err := json.Unmarshal(b, cfg); err != nil {
			return err
		}
	default:
		return errors.New("config must be .yaml/.yml or .json")
	}
	return nil
}

func validate(cfg *Config, requireUpstream bool) error {
	if cfg == nil {
		return errors.New("config is required")
	}
	if requireUpstream && strings.TrimSpace(cfg.Outbound.Target) == "" {
		return errors.New("outbound.target is required")
	}

	for i, method := range cfg.X402 {
		field := fmt.Sprintf("x402[%d]", i)
		if strings.TrimSpace(method.Name) == "" {
			return fmt.Errorf("%s.name is required", field)
		}
		if strings.TrimSpace(method.FacilitatorURL) == "" {
			return fmt.Errorf("%s.facilitator_url is required", field)
		}
		if strings.TrimSpace(method.Network) == "" {
			return fmt.Errorf("%s.network is required", field)
		}
		if strings.TrimSpace(method.Merchant) == "" {
			return fmt.Errorf("%s.merchant is required", field)
		}
		if err := validateX402SchemeValue(method.Scheme, field+".scheme", false); err != nil {
			return err
		}
	}

	//for i, method := range cfg.MPP {
	//	field := fmt.Sprintf("mpp[%d]", i)
	//	if strings.TrimSpace(method.Name) == "" {
	//		return fmt.Errorf("%s.name is required", field)
	//	}
	//	if strings.TrimSpace(method.Merchant) == "" {
	//		return fmt.Errorf("%s.merchant is required", field)
	//	}
	//	if err := validateMPPSchemeValue(method.Scheme, field+".scheme", false); err != nil {
	//		return err
	//	}
	//}

	for i, rule := range cfg.Outbound.Rules {
		field := fmt.Sprintf("outbound.rules[%d]", i)
		if strings.TrimSpace(rule.PathValue()) == "" {
			return fmt.Errorf("%s.path is required", field)
		}
		if rule.Free {
			continue
		}
		if len(rule.PaymentMethods) == 0 {
			if len(cfg.DefaultPaymentMethodNames()) == 0 {
				return fmt.Errorf("%s.payment_methods is required when no supported payment methods are configured", field)
			}
			continue
		}
		for _, name := range rule.PaymentMethods {
			if _, err := cfg.ResolvePaymentMethod(name); err != nil {
				return fmt.Errorf("%s.payment_methods: %w", field, err)
			}
		}
	}

	return nil
}

func (cfg *Config) PathRules() []Rule {
	if cfg == nil {
		return nil
	}
	return append([]Rule(nil), cfg.Outbound.Rules...)
}

func (r Rule) PathValue() string {
	if strings.TrimSpace(r.Path) != "" {
		return r.Path
	}
	return r.PathGlob
}

func (cfg *Config) DefaultPaymentMethodNames() []string {
	if cfg == nil {
		return nil
	}

	//names := make([]string, 0, len(cfg.X402)+len(cfg.MPP))
	names := make([]string, 0, len(cfg.X402))
	for _, method := range cfg.X402 {
		if isSupportedX402Method(method) {
			names = append(names, method.Name)
		}
	}
	//for _, method := range cfg.MPP {
	//	if isSupportedMPPMethod(method) {
	//		names = append(names, method.Name)
	//	}
	//}
	return names
}

func (cfg *Config) ResolveRulePaymentMethods(rule Rule) ([]ResolvedPaymentMethod, error) {
	if cfg == nil {
		return nil, errors.New("config is required")
	}

	names := append([]string(nil), rule.PaymentMethods...)
	if len(names) == 0 {
		names = cfg.DefaultPaymentMethodNames()
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("rule %q has no resolvable payment methods", rule.Name)
	}

	out := make([]ResolvedPaymentMethod, 0, len(names))
	for _, name := range names {
		method, err := cfg.ResolvePaymentMethod(name)
		if err != nil {
			return nil, err
		}
		out = append(out, method)
	}
	return out, nil
}

func (cfg *Config) ResolvePaymentMethod(name string) (ResolvedPaymentMethod, error) {
	normalized := strings.TrimSpace(name)
	if normalized == "" {
		return ResolvedPaymentMethod{}, errors.New("payment method name is required")
	}

	for i := range cfg.X402 {
		if cfg.X402[i].Name == normalized {
			if !isSupportedX402Method(cfg.X402[i]) {
				return ResolvedPaymentMethod{}, fmt.Errorf("payment method %q is not supported", normalized)
			}
			return ResolvedPaymentMethod{Name: normalized, X402: &cfg.X402[i]}, nil
		}
	}

	//for i := range cfg.MPP {
	//	if cfg.MPP[i].Name == normalized {
	//		if !isSupportedMPPMethod(cfg.MPP[i]) {
	//			return ResolvedPaymentMethod{}, fmt.Errorf("payment method %q is not supported", normalized)
	//		}
	//		return ResolvedPaymentMethod{Name: normalized, MPP: &cfg.MPP[i]}, nil
	//	}
	//}

	return ResolvedPaymentMethod{}, fmt.Errorf("payment method %q is not defined", normalized)
}

func isSupportedX402Method(method X402Method) bool {
	switch NormalizeScheme(method.Scheme) {
	case SchemeExact, SchemeUpto:
		return true
	default:
		return false
	}
}

//func isSupportedMPPMethod(method MPPMethod) bool {
//	return NormalizeMethod(method.Method) == MPPMethodTempo && NormalizeScheme(method.Scheme) == SchemeCharge
//}

func validateX402SchemeValue(value, field string, allowEmpty bool) error {
	scheme := NormalizeScheme(value)
	if scheme == "" {
		if allowEmpty {
			return nil
		}
		scheme = SchemeExact
	}
	switch scheme {
	case SchemeExact, SchemeUpto:
		return nil
	default:
		return fmt.Errorf("%s must be one of %q or %q, got %q", field, SchemeExact, SchemeUpto, value)
	}
}

func validateMPPSchemeValue(value, field string, allowEmpty bool) error {
	scheme := NormalizeScheme(value)
	if scheme == "" {
		if allowEmpty {
			return nil
		}
		scheme = SchemeCharge
	}
	switch scheme {
	case SchemeCharge, SchemeSession:
		return nil
	default:
		return fmt.Errorf("%s must be one of %q or %q, got %q", field, SchemeCharge, SchemeSession, value)
	}
}

func NormalizeScheme(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func NormalizeMethod(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}
