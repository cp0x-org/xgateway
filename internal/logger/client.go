package logger

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
)

const apiKeyHeader = "X-Api-Key"

type Client struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

func New(baseURL, apiKey string) *Client {
	return &Client{
		baseURL:    strings.TrimRight(baseURL, "/"),
		apiKey:     apiKey,
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

func (c *Client) Enabled() bool {
	return c != nil && c.baseURL != ""
}

// CreateLogReq is sent to POST /api/v1/request-logs.
type CreateLogReq struct {
	ID                 uuid.UUID  `json:"id"`
	ProjectID          uuid.UUID  `json:"project_id"`
	OutboundRouteID    *uuid.UUID `json:"outbound_route_id,omitempty"`
	RequestID          string     `json:"request_id"`
	Method             string     `json:"method"`
	Path               string     `json:"path"`
	ClientIP           *string    `json:"client_ip,omitempty"`
	UserAgent          *string    `json:"user_agent,omitempty"`
	Status             string     `json:"status"`
	PaymentRequired    bool       `json:"payment_required"`
	PaymentRequestedAt *time.Time `json:"payment_requested_at,omitempty"`
}

// UpdateLogReq is sent to PUT /api/v1/request-logs/:id.
type UpdateLogReq struct {
	Status                 string     `json:"status"`
	PaymentRequired        bool       `json:"payment_required"`
	PaymentRequestedAt     *time.Time `json:"payment_requested_at,omitempty"`
	PaymentCompleted       bool       `json:"payment_completed"`
	PaymentCompletedAt     *time.Time `json:"payment_completed_at,omitempty"`
	PaymentChannelID       *uuid.UUID `json:"payment_channel_id,omitempty"`
	PaymentChannelAssetID  *uuid.UUID `json:"payment_channel_asset_id,omitempty"`
	AmountUSD              *string    `json:"amount_usd,omitempty"`
	UpstreamURL            *string    `json:"upstream_url,omitempty"`
	UpstreamStatusCode     *int32     `json:"upstream_status_code,omitempty"`
	UpstreamResponseTimeMs *int32     `json:"upstream_response_time_ms,omitempty"`
	FinalStatusCode        *int32     `json:"final_status_code,omitempty"`
	ErrorType              *string    `json:"error_type,omitempty"`
	ErrorMessage           *string    `json:"error_message,omitempty"`
}

// Dispatch runs all log operations sequentially in a single goroutine.
// It fires-and-forgets: the caller is not blocked.
func (c *Client) Dispatch(create CreateLogReq, updates []UpdateLogReq) {
	if !c.Enabled() {
		return
	}
	go func() {
		if err := c.doCreate(create); err != nil {
			log.Printf("logger: create log %s: %v", create.ID, err)
			return
		}
		for _, u := range updates {
			if err := c.doUpdate(create.ID, u); err != nil {
				log.Printf("logger: update log %s to %q: %v", create.ID, u.Status, err)
			}
		}
	}()
}

// DispatchUpdates sends only UPDATE calls for an existing log entry.
// Used when the entry was already created by a previous 402 response.
func (c *Client) DispatchUpdates(id uuid.UUID, updates []UpdateLogReq) {
	if !c.Enabled() || len(updates) == 0 {
		return
	}
	go func() {
		for _, u := range updates {
			if err := c.doUpdate(id, u); err != nil {
				log.Printf("logger: update log %s to %q: %v", id, u.Status, err)
			}
		}
	}()
}

func (c *Client) doCreate(req CreateLogReq) error {
	body, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
		c.baseURL+"/api/v1/request-logs", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("new request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set(apiKeyHeader, c.apiKey)
	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("do: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	return nil
}

func (c *Client) doUpdate(id uuid.UUID, req UpdateLogReq) error {
	body, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	url := fmt.Sprintf("%s/api/v1/request-logs/%s", c.baseURL, id)
	httpReq, err := http.NewRequestWithContext(context.Background(), http.MethodPut,
		url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("new request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set(apiKeyHeader, c.apiKey)
	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("do: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	return nil
}
