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
// Either or both of x402/mpp may be nil when that protocol is not configured.
type entry struct {
	rule   rules.Rule
	rp     *httputil.ReverseProxy
	x402   *x402Protocol
	mpp    *mppProtocol
	scheme string
}

func (e *entry) hasPayment() bool {
	return e.x402 != nil || e.mpp != nil
}

// runPayment dispatches to the appropriate payment protocol:
//   - MPP when the client carries an MPP Authorization header, or when MPP is the
//     only protocol configured for the route.
//   - x402 otherwise (default).
//
// Returns the protocol name used, for logging.
func (e *entry) runPayment(c *gin.Context) string {
	if e.mpp != nil && (e.x402 == nil || c.GetHeader("Authorization") != "") {
		e.mpp.Handle(c)
		return protoMPP
	}
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
		p, err := buildX402Protocol(ctx, rule, reqPath, x402Channels, s.facilCache, s.x402Timeout, s.settleTTL)
		if err != nil {
			return nil, fmt.Errorf("build x402: %w", err)
		}
		e.x402 = p
	}

	mppChannels := filterEnabledChannels(rule.PaymentChannels, protoMPP)
	if len(mppChannels) > 0 {
		p, err := buildMPPProtocol(rule, reqPath, mppChannels)
		if err != nil {
			return nil, fmt.Errorf("build mpp: %w", err)
		}
		e.mpp = p
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
			*r = *r.WithContext(withPublicOrigin(r.Context(), publicScheme(r), r.Host))
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
			// x402 settlement header processing (applies to all responses).
			scheme, _ := resp.Request.Context().Value(matchedRuleSchemeContextKey).(string)
			applyUpstreamSettlementHeaders(resp.Header, scheme)

			// Redirect rewriting (3xx only): preserve the gateway's project
			// prefix and rewrite absolute redirects back to the upstream's own
			// host into the gateway's public origin, so MPP clients -- which
			// refuse to follow cross-origin redirects carrying payment
			// credentials -- can still follow them.
			if resp.StatusCode < 300 || resp.StatusCode >= 400 {
				return nil
			}
			loc := resp.Header.Get("Location")
			if loc == "" {
				return nil
			}
			locURL, err := url.Parse(loc)
			if err != nil {
				return nil
			}
			if locURL.Host != "" && locURL.Host != upURL.Host {
				return nil // redirect to an unrelated third-party host: leave untouched
			}

			changed := false
			if locURL.Host == upURL.Host {
				if pubScheme, pubHost, ok := publicOriginFromContext(resp.Request.Context()); ok {
					locURL.Scheme = pubScheme
					locURL.Host = pubHost
					changed = true
				}
			}
			if prefix != "" && !strings.HasPrefix(locURL.Path, prefix+"/") {
				locURL.Path = prefix + locURL.Path
				changed = true
			}
			if changed {
				resp.Header.Set("Location", locURL.String())
			}
			return nil
		},
	}, nil
}

type publicOriginKey struct{}

type publicOrigin struct {
	scheme, host string
}

func withPublicOrigin(ctx context.Context, scheme, host string) context.Context {
	return context.WithValue(ctx, publicOriginKey{}, publicOrigin{scheme: scheme, host: host})
}

func publicOriginFromContext(ctx context.Context) (scheme, host string, ok bool) {
	o, ok := ctx.Value(publicOriginKey{}).(publicOrigin)
	return o.scheme, o.host, ok
}

// publicScheme reports the scheme the client used to reach the gateway,
// honouring X-Forwarded-Proto when the gateway sits behind a TLS-terminating
// proxy (r.TLS is nil in that case).
func publicScheme(r *http.Request) string {
	if proto := r.Header.Get("X-Forwarded-Proto"); proto != "" {
		return proto
	}
	if r.TLS != nil {
		return "https"
	}
	return "http"
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
