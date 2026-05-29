package proxy

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/cp0x-org/xpaywall/xgateway/internal/config"
	"github.com/cp0x-org/xpaywall/xgateway/internal/rules"
)

// entry is the per-route resource bundle resolved on first request and cached.
// Exactly one of x402/mpp may be nil when that protocol is not configured.
type entry struct {
	rule   rules.Rule
	rp     *httputil.ReverseProxy
	x402   *x402Protocol
	scheme string
}

func (e *entry) hasPayment() bool {
	return e.x402 != nil
}

// runPayment dispatches to the appropriate payment protocol:
//   - MPP when the client carries an MPP Authorization header and MPP is configured.
//   - x402 otherwise (default).
//
// Returns the protocol name used, for logging.
func (e *entry) runPayment(c *gin.Context) string {
	if e.x402 != nil {
		e.x402.Handle(c)
		return protoX402
	}
	return ""
}

// buildEntry constructs and initialises the per-route entry: upstream proxy plus
// any payment protocols derived from the rule's enabled channels.
func (s *state) buildEntry(ctx context.Context, rule *rules.Rule, reqPath string) (*entry, error) {
	log.Printf("[entry] building entry for %s: free=%v channels=%d", reqPath, rule.Free, len(rule.PaymentChannels))
	e := &entry{rule: *rule}

	if rule.Target != "" {
		rp, err := buildUpstreamProxy(rule, reqPath)
		if err != nil {
			return nil, err
		}
		e.rp = rp
	}

	if rule.Free || len(rule.PaymentChannels) == 0 {
		return e, nil
	}

	// Effective scheme from first enabled channel.
	for _, ch := range rule.PaymentChannels {
		if ch.Enabled {
			e.scheme = config.NormalizeScheme(ch.Scheme)
			break
		}
	}

	x402Channels := filterEnabledChannels(rule.PaymentChannels, protoX402)

	if len(x402Channels) > 0 {
		p, err := buildX402Protocol(ctx, rule, reqPath, x402Channels, s.facilCache)
		if err != nil {
			return nil, fmt.Errorf("build x402: %w", err)
		}
		e.x402 = p
	}

	return e, nil
}

func filterEnabledChannels(channels []*rules.PaymentChannel, proto string) []*rules.PaymentChannel {
	var out []*rules.PaymentChannel
	for _, ch := range channels {
		if ch.Enabled && ch.Protocol == proto {
			out = append(out, ch)
		}
	}
	return out
}

// buildUpstreamProxy creates the reverse proxy for the rule's Target.
func buildUpstreamProxy(rule *rules.Rule, reqPath string) (*httputil.ReverseProxy, error) {
	upURL, err := url.Parse(rule.Target)
	if err != nil {
		return nil, fmt.Errorf("parse target URL: %w", err)
	}
	authName := rule.AuthHeaderName
	authValue := rule.AuthHeaderValue

	cleanPath := rule.InboundPath

	// Compute the project-slug prefix (e.g. "/default") by stripping the
	// inbound path from the request path. Trim trailing slashes before
	// comparing so that "/default/free-multipoint/" still yields "/default".
	prefix := ""
	if cleanPath != "" && !containsGlob(cleanPath) {
		trimmedReq := strings.TrimRight(reqPath, "/")
		trimmedClean := strings.TrimRight(cleanPath, "/")
		if trimmedClean != "" && strings.HasSuffix(trimmedReq, trimmedClean) {
			prefix = trimmedReq[:len(trimmedReq)-len(trimmedClean)]
		}
	}

	return &httputil.ReverseProxy{
		Director: func(r *http.Request) {
			r.URL.Scheme = upURL.Scheme
			r.URL.Host = upURL.Host
			r.Host = upURL.Host
			if !containsGlob(cleanPath) && prefix != "" {
				// Strip the project prefix and forward the rest as-is,
				// preserving any trailing slash so upstream routers (e.g. Gin)
				// don't issue a redirect for paths like /free-multipoint/.
				stripped := strings.TrimPrefix(r.URL.Path, prefix)
				if stripped == "" || stripped[0] != '/' {
					stripped = "/" + stripped
				}
				r.URL.Path = stripped
			} else if cleanPath != "" && cleanPath != r.URL.Path && !containsGlob(cleanPath) {
				r.URL.Path = cleanPath
			}
			if authName != "" && authValue != "" {
				r.Header.Set(authName, authValue)
			}
		},
		ModifyResponse: func(resp *http.Response) error {
			if prefix != "" && resp.StatusCode >= 300 && resp.StatusCode < 400 {
				if loc := resp.Header.Get("Location"); strings.HasPrefix(loc, "/") && !strings.HasPrefix(loc, prefix+"/") {
					resp.Header.Set("Location", prefix+loc)
				}
			}
			scheme, _ := resp.Request.Context().Value(matchedRuleSchemeContextKey).(string)
			applyUpstreamSettlementHeaders(resp.Header, scheme)
			return nil
		},
	}, nil
}

func classifyUpstreamResult(statusCode int32, err error) (proxyStatus, string, string) {
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return statusUpstreamTimeout, "timeout", err.Error()
		}
		return statusFailed, "proxy_error", err.Error()
	}
	switch {
	case statusCode == http.StatusGatewayTimeout:
		return statusUpstreamTimeout, "timeout", ""
	case statusCode == http.StatusBadGateway || statusCode == http.StatusServiceUnavailable:
		return statusUpstreamError, "upstream_unavailable", ""
	case statusCode >= 500:
		return statusUpstreamError, "upstream_error", ""
	default:
		return statusCompleted, "", ""
	}
}

// resolvePaymentChannelInfo finds the first enabled channel matching usedProtocol
// and returns its DB IDs and effective price.
func resolvePaymentChannelInfo(channels []*rules.PaymentChannel, usedProtocol, routePrice string) (*uuid.UUID, *uuid.UUID, *string) {
	zeroUUID := uuid.UUID{}
	for _, ch := range channels {
		if ch.Protocol != usedProtocol || !ch.Enabled {
			continue
		}

		rawPrice := ch.Price
		if rawPrice == "" {
			rawPrice = routePrice
		}
		var pricePtr *string
		if rawPrice != "" && ch.Decimals > 0 {
			if raw, err := strconv.ParseFloat(rawPrice, 64); err == nil && raw > 0 {
				usd := raw / math.Pow10(int(ch.Decimals))
				s := strconv.FormatFloat(usd, 'f', 6, 64)
				pricePtr = &s
			}
		} else if rawPrice != "" {
			pricePtr = &rawPrice
		}

		var channelID, assetID *uuid.UUID
		if ch.ID != zeroUUID {
			id := ch.ID
			channelID = &id
		}
		if ch.AssetID != nil && *ch.AssetID != zeroUUID {
			assetID = ch.AssetID
		}

		return channelID, assetID, pricePtr
	}
	return nil, nil, nil
}

func containsGlob(s string) bool {
	return strings.ContainsAny(s, "*?[")
}

func strPtr(s string) *string { return &s }
func strPtrIfNotEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
