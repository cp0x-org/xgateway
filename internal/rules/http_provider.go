package rules

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
)

const internalAPIKeyHeader = "X-Api-Key"

var _ Provider = (*HttpProvider)(nil)

type HttpProvider struct {
	baseURL string
	apiKey  string
	client  *http.Client
}

func NewHttpProvider(baseURL, apiKey string) *HttpProvider {
	return &HttpProvider{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		client:  &http.Client{Timeout: 10 * time.Second},
	}
}

type resolveRouteResponse struct {
	ProjectID       uuid.UUID       `json:"project_id"`
	OutboundRouteID uuid.UUID       `json:"outbound_route_id"`
	Name            string          `json:"name"`
	InboundPath     string          `json:"inbound_path"`
	Target          string          `json:"target"`
	AuthHeaderName  string          `json:"auth_header_name"`
	AuthHeaderValue string          `json:"auth_header_value"`
	AllowUnmatched  bool            `json:"allow_unmatched"`
	Price           string          `json:"price"`
	Free            bool            `json:"free"`
	MimeType        string          `json:"mime_type"`
	Description     string          `json:"description"`
	Bazaar          json.RawMessage `json:"bazaar,omitempty"`
	PaymentChannels []channelDTO    `json:"payment_channels"`
}

// bazaarDTO mirrors the YAML BazaarConfig shape so HTTP-delivered bazaar payloads
// follow the same field names as the file provider.
type bazaarDTO struct {
	Disabled      bool           `json:"disabled,omitempty"`
	Method        string         `json:"method,omitempty"`
	BodyType      string         `json:"body_type,omitempty"`
	InputExample  map[string]any `json:"input_example,omitempty"`
	InputSchema   map[string]any `json:"input_schema,omitempty"`
	OutputExample map[string]any `json:"output_example,omitempty"`
	OutputSchema  map[string]any `json:"output_schema,omitempty"`
}

type channelDTO struct {
	Protocol        string            `json:"protocol"`
	Method          string            `json:"method"`
	Code            string            `json:"code"`
	Scheme          string            `json:"scheme"`
	Price           string            `json:"price"`
	Enabled         bool              `json:"enabled"`
	ChannelConfig   map[string]string `json:"channel_config"`
	ChannelID       uuid.UUID         `json:"channel_id"`
	PaymentMethodID uuid.UUID         `json:"payment_method_id"`
	AssetID         *uuid.UUID        `json:"asset_id,omitempty"`
	// flat fields sent by control-api (new schema)
	FacilitatorURL  string `json:"facilitator_url"`
	CaIP2ChainID    string `json:"caip2_chain_id"`
	PayoutAddress   string `json:"payout_address"`
	ContractAddress string `json:"contract_address"`
	Amount          string `json:"amount"`
	Decimals        int32  `json:"decimals"`
}

// GetByInboundPath resolves a full inbound path (/{slug}/{path}) via the control-api.
func (p *HttpProvider) GetByInboundPath(ctx context.Context, inboundPath string) (*Rule, error) {
	if !strings.HasPrefix(inboundPath, "/") {
		inboundPath = "/" + inboundPath
	}

	url := p.baseURL + "/proxy/resolve" + inboundPath
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	if p.apiKey != "" {
		req.Header.Set(internalAPIKeyHeader, p.apiKey)
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("control-api status %d", resp.StatusCode)
	}

	var dto resolveRouteResponse
	if err := json.NewDecoder(resp.Body).Decode(&dto); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	return fromResolveResponse(dto), nil
}

func fromResolveResponse(dto resolveRouteResponse) *Rule {
	channels := make([]*PaymentChannel, 0, len(dto.PaymentChannels))
	for _, ch := range dto.PaymentChannels {
		if !ch.Enabled {
			continue
		}

		cfg := ch.ChannelConfig
		if cfg == nil {
			cfg = map[string]string{
				"facilitator_url": ch.FacilitatorURL,
				"network":         ch.CaIP2ChainID,
				"merchant":        ch.PayoutAddress,
				"asset":           ch.ContractAddress,
			}
		}

		method := ch.Method
		if method == "" {
			method = ch.Code
		}

		id := ch.ChannelID
		if id == (uuid.UUID{}) {
			id = ch.PaymentMethodID
		}

		price := ch.Amount
		if price == "" {
			price = ch.Price
		}
		channels = append(channels, &PaymentChannel{
			ID:            id,
			AssetID:       ch.AssetID,
			Protocol:      ch.Protocol,
			Method:        method,
			Scheme:        ch.Scheme,
			Price:         price,
			Decimals:      ch.Decimals,
			Enabled:       ch.Enabled,
			ChannelConfig: cfg,
		})
	}

	return &Rule{
		ProjectID:       dto.ProjectID,
		OutboundRouteID: dto.OutboundRouteID,
		Name:            dto.Name,
		InboundPath:     dto.InboundPath,
		OutboundPath:    dto.Target,
		Target:          dto.Target,
		AuthHeaderName:  dto.AuthHeaderName,
		AuthHeaderValue: dto.AuthHeaderValue,
		AllowUnmatched:  dto.AllowUnmatched,
		Price:           dto.Price,
		Free:            dto.Free,
		MimeType:        dto.MimeType,
		Description:     dto.Description,
		Bazaar:          decodeBazaar(dto.Bazaar),
		PaymentChannels: channels,
	}
}

// decodeBazaar parses the JSON payload from control-api into BazaarConfig.
// Returns nil when the payload is absent or malformed — auto-mode kicks in upstream.
func decodeBazaar(raw json.RawMessage) *BazaarConfig {
	if len(raw) == 0 {
		return nil
	}
	var dto bazaarDTO
	if err := json.Unmarshal(raw, &dto); err != nil {
		return nil
	}
	return &BazaarConfig{
		Disabled:      dto.Disabled,
		Method:        dto.Method,
		BodyType:      dto.BodyType,
		InputExample:  dto.InputExample,
		InputSchema:   dto.InputSchema,
		OutputExample: dto.OutputExample,
		OutputSchema:  dto.OutputSchema,
	}
}
