package proxy

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	x402 "github.com/x402-foundation/x402/go"
	"github.com/x402-foundation/x402/go/extensions/bazaar"
	exttypes "github.com/x402-foundation/x402/go/extensions/types"
	x402http "github.com/x402-foundation/x402/go/http"
	evmexact "github.com/x402-foundation/x402/go/mechanisms/evm/exact/server"
	evmupto "github.com/x402-foundation/x402/go/mechanisms/evm/upto/server"

	"github.com/cp0x-org/xpaywall/xgateway/internal/config"
	"github.com/cp0x-org/xpaywall/xgateway/internal/rules"
)

const (
	protoX402                   = "x402"
	upstreamSettlementAmountKey = "X-X402-Settlement-Amount"
	x402DefaultTimeout          = 30 * time.Second
)

type requestContextKey string

const matchedRuleSchemeContextKey requestContextKey = "proxy.rule_scheme"

// ---------- Facilitator HTTP client ------------------------------------------

type facilitatorLoggingTransport struct{}

func (t *facilitatorLoggingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	var reqBody []byte
	if req.Body != nil {
		reqBody, _ = io.ReadAll(req.Body)
		req.Body = io.NopCloser(bytes.NewReader(reqBody))
	}
	log.Printf("[facilitator] → %s %s body=%s", req.Method, req.URL, truncateIfNotJSON(reqBody))

	resp, err := http.DefaultTransport.RoundTrip(req)
	if err != nil {
		log.Printf("[facilitator] ← error: %v", err)
		return nil, err
	}

	respBody, _ := io.ReadAll(resp.Body)
	resp.Body = io.NopCloser(bytes.NewReader(respBody))
	log.Printf("[facilitator] ← %d %s body=%s", resp.StatusCode, req.URL, truncateIfNotJSON(respBody))
	return resp, nil
}

func truncateIfNotJSON(data []byte) string {
	s := strings.TrimSpace(string(data))
	if s != "" && (s[0] == '{' || s[0] == '[') {
		return s
	}
	if len(s) > 120 {
		return s[:120] + "...[truncated]"
	}
	return s
}

func facilitatorHTTPClient() *http.Client {
	return &http.Client{
		Timeout:   x402DefaultTimeout,
		Transport: &facilitatorLoggingTransport{},
	}
}

// facilitatorCache memoises x402 facilitator clients keyed by URL.
type facilitatorCache struct {
	mu sync.RWMutex
	m  map[string]x402.FacilitatorClient
}

func newFacilitatorCache() *facilitatorCache {
	return &facilitatorCache{m: make(map[string]x402.FacilitatorClient)}
}

func (f *facilitatorCache) get(url string) x402.FacilitatorClient {
	f.mu.RLock()
	fac, ok := f.m[url]
	f.mu.RUnlock()
	if ok {
		return fac
	}
	fac = x402http.NewHTTPFacilitatorClient(&x402http.FacilitatorConfig{
		URL:        url,
		HTTPClient: facilitatorHTTPClient(),
	})
	f.mu.Lock()
	f.m[url] = fac
	f.mu.Unlock()
	return fac
}

// ---------- x402 protocol ----------------------------------------------------

// x402Protocol handles payment verification for one route using the x402 protocol.
type x402Protocol struct {
	srv *x402http.HTTPServer
}

func (p *x402Protocol) Handle(c *gin.Context) {
	if p.srv == nil {
		c.Next()
		return
	}

	adapter := &ginAdapter{ctx: c}
	reqCtx := x402http.HTTPRequestContext{
		Adapter: adapter,
		Path:    c.Request.URL.Path,
		Method:  c.Request.Method,
	}

	if !p.srv.RequiresPayment(reqCtx) {
		c.Next()
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), x402DefaultTimeout)
	defer cancel()

	result := p.srv.ProcessHTTPRequest(ctx, reqCtx, nil)
	log.Printf("[x402] ProcessHTTPRequest path=%s result=%v", reqCtx.Path, result.Type)
	switch result.Type {
	case x402http.ResultNoPaymentRequired:
		c.Next()
	case x402http.ResultPaymentError:
		if result.Response != nil {
			log.Printf("[x402] payment error: status=%d", result.Response.Status)
		}
		writeX402Error(c, result.Response)
	case x402http.ResultPaymentVerified:
		log.Printf("[x402] payment verified, proceeding to upstream")
		handleX402Verified(c, p.srv, ctx, reqCtx, result)
	}
}

// buildX402Protocol constructs and initialises an x402Protocol for a route.
//
// Price is passed as map[string]interface{}{"asset": ..., "amount": ...} when an
// explicit asset is configured (v2 mode). If no asset is specified in ChannelConfig
// the library falls back to its default USD→USDC conversion for the network.
func buildX402Protocol(
	ctx context.Context,
	rule *rules.Rule,
	reqPath string,
	channels []*rules.PaymentChannel,
	cache *facilitatorCache,
) (*x402Protocol, error) {
	options := make(x402http.PaymentOptions, 0, len(channels))
	for _, ch := range channels {
		price := ch.Price
		if price == "" {
			price = rule.Price
		}

		priceVal := map[string]any{
			"asset":  ch.ChannelConfig["asset"],
			"amount": price,
		}
		fmt.Printf("priceVal: %+v\n", priceVal)

		options = append(options, x402http.PaymentOption{
			Scheme:  config.NormalizeScheme(ch.Scheme),
			PayTo:   ch.ChannelConfig["merchant"],
			Price:   priceVal,
			Network: x402.Network(ch.ChannelConfig["network"]),
		})
	}
	if len(options) == 0 {
		return nil, nil
	}

	log.Printf("[entry] x402 routeKey=%s options=%d", reqPath, len(options))

	routes := x402http.RoutesConfig{
		reqPath: x402http.RouteConfig{
			Accepts:     options,
			Description: rule.Description,
			MimeType:    rule.MimeType,
			Extensions:  buildBazaarExtensions(rule),
		},
	}

	var serverOpts []x402.ResourceServerOption
	for _, ch := range channels {
		facURL := ch.ChannelConfig["facilitator_url"]
		log.Printf("[entry] x402 channel scheme=%s network=%s facilitator_url=%q merchant=%q",
			ch.Scheme, ch.ChannelConfig["network"], facURL, ch.ChannelConfig["merchant"])
		if facURL == "" {
			log.Printf("[entry] WARNING: x402 channel missing facilitator_url — payment verification will fail")
			continue
		}
		serverOpts = append(serverOpts, x402.WithFacilitatorClient(cache.get(facURL)))
	}

	srv := x402http.Newx402HTTPResourceServer(routes, serverOpts...)
	srv.RegisterExtension(bazaar.BazaarResourceServerExtension)
	for _, ch := range channels {
		network := x402.Network(ch.ChannelConfig["network"])
		switch config.NormalizeScheme(ch.Scheme) {
		case config.SchemeExact:
			srv.Register(network, evmexact.NewExactEvmScheme())
		case config.SchemeUpto:
			srv.Register(network, evmupto.NewUptoEvmScheme())
		}
	}

	initCtx, cancel := context.WithTimeout(ctx, x402DefaultTimeout)
	defer cancel()
	if err := srv.Initialize(initCtx); err != nil {
		return nil, fmt.Errorf("initialize x402 server: %w", err)
	}
	return &x402Protocol{srv: srv}, nil
}

// ---------- x402 runtime helpers ---------------------------------------------

func writeX402Error(c *gin.Context, response *x402http.HTTPResponseInstructions) {
	if response == nil {
		c.AbortWithStatus(http.StatusPaymentRequired)
		return
	}
	for key, value := range response.Headers {
		c.Header(key, value)
	}
	c.Status(response.Status)
	if response.IsHTML {
		c.Data(response.Status, "text/html; charset=utf-8", []byte(fmt.Sprint(response.Body)))
	} else {
		c.JSON(response.Status, response.Body)
	}
	c.Abort()
}

func handleX402Verified(
	c *gin.Context,
	server *x402http.HTTPServer,
	ctx context.Context,
	reqCtx x402http.HTTPRequestContext,
	result x402http.HTTPProcessResult,
) {
	writer := &responseCapture{
		ResponseWriter: c.Writer,
		body:           &bytes.Buffer{},
		statusCode:     http.StatusOK,
	}
	c.Writer = writer

	c.Next()
	if c.IsAborted() {
		return
	}

	c.Writer = writer.ResponseWriter
	if writer.statusCode >= 400 {
		c.Writer.WriteHeader(writer.statusCode)
		_, _ = c.Writer.Write(writer.body.Bytes())
		return
	}

	log.Printf("[x402] running settlement, upstream status=%d", writer.statusCode)
	settleResult := server.ProcessSettlement(
		ctx,
		*result.PaymentPayload,
		*result.PaymentRequirements,
		nil,
		&x402http.HTTPTransportContext{
			Request:         &reqCtx,
			ResponseBody:    writer.body.Bytes(),
			ResponseHeaders: writer.Header(),
		},
	)

	if !settleResult.Success {
		decodedHeaders := make(map[string]string, len(settleResult.Headers))
		for k, v := range settleResult.Headers {
			if decoded, err := base64.StdEncoding.DecodeString(v); err == nil {
				decodedHeaders[k] = string(decoded)
			} else {
				decodedHeaders[k] = v
			}
		}
		log.Printf("[x402] settlement FAILED: headers=%v response=%v", decodedHeaders, settleResult.Response)
		for key, value := range settleResult.Headers {
			c.Header(key, value)
		}
		if settleResult.Response != nil {
			writeX402Error(c, settleResult.Response)
			return
		}
		c.AbortWithStatus(http.StatusPaymentRequired)
		return
	}
	log.Printf("[x402] settlement SUCCESS")

	for key, value := range settleResult.Headers {
		c.Header(key, value)
	}
	c.Writer.WriteHeader(writer.statusCode)
	_, _ = c.Writer.Write(writer.body.Bytes())
}

// buildBazaarExtensions returns the Extensions map for an x402 RouteConfig.
// When the rule has no Bazaar block, a minimal GET declaration is generated so
// the endpoint still appears in facilitator catalogs. The server-side
// BazaarResourceServerExtension rewrites method and path params per-request.
// Returns nil when discovery is explicitly disabled.
func buildBazaarExtensions(rule *rules.Rule) map[string]any {
	if rule == nil {
		return nil
	}
	cfg := rule.Bazaar
	if cfg != nil && cfg.Disabled {
		return nil
	}

	method := exttypes.MethodGET
	if cfg != nil && strings.TrimSpace(cfg.Method) != "" {
		method = exttypes.QueryParamMethods(strings.ToUpper(strings.TrimSpace(cfg.Method)))
	}

	var (
		inputExample any
		inputSchema  exttypes.JSONSchema
		bodyType     exttypes.BodyType
		outputConfig *exttypes.OutputConfig
	)
	if cfg != nil {
		if len(cfg.InputExample) > 0 {
			inputExample = cfg.InputExample
		}
		if len(cfg.InputSchema) > 0 {
			inputSchema = exttypes.JSONSchema(cfg.InputSchema)
		}
		if strings.TrimSpace(cfg.BodyType) != "" {
			bodyType = exttypes.BodyType(strings.ToLower(strings.TrimSpace(cfg.BodyType)))
		}
		if len(cfg.OutputExample) > 0 || len(cfg.OutputSchema) > 0 {
			outputConfig = &exttypes.OutputConfig{
				Example: cfg.OutputExample,
				Schema:  exttypes.JSONSchema(cfg.OutputSchema),
			}
		}
	}

	ext, err := bazaar.DeclareDiscoveryExtension(method, inputExample, inputSchema, bodyType, outputConfig)
	if err != nil {
		log.Printf("[x402] bazaar: failed to declare extension for %q: %v", rule.InboundPath, err)
		return nil
	}
	return map[string]any{bazaar.BAZAAR.Key(): ext}
}

// applyUpstreamSettlementHeaders translates the upstream's X-X402-Settlement-Amount
// header into the x402 SettlementOverrides header for the "upto" scheme.
// For all other schemes it simply strips both headers.
func applyUpstreamSettlementHeaders(headers http.Header, scheme string) {
	if headers == nil {
		return
	}
	if config.NormalizeScheme(scheme) != config.SchemeUpto {
		headers.Del(x402http.SettlementOverridesHeader)
		headers.Del(upstreamSettlementAmountKey)
		return
	}
	amount := strings.TrimSpace(headers.Get(upstreamSettlementAmountKey))
	if amount != "" && headers.Get(x402http.SettlementOverridesHeader) == "" {
		headers.Set(
			x402http.SettlementOverridesHeader,
			x402http.MarshalSettlementOverrides(&x402.SettlementOverrides{Amount: amount}),
		)
	}
	headers.Del(upstreamSettlementAmountKey)
}
