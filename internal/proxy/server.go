package proxy

import (
	"errors"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"time"

	"github.com/cp0x-org/xpaywall/xgateway/internal/config"
	"github.com/cp0x-org/xpaywall/xgateway/internal/logger"
	"github.com/cp0x-org/xpaywall/xgateway/internal/rules"
)

// Server wraps an HTTP handler with payment gateway logic.
type Server struct {
	handler http.Handler
}

type serverOptions struct {
	fallback      http.Handler
	logger        *logger.Client
	debug         bool
	x402Timeout   time.Duration
	x402SettleTTL time.Duration
}

// Option configures a Server.
type Option func(*serverOptions)

// WithFallback sets a handler for requests that match no rule.
// Useful for file-based configs with allow_unmatched=true.
func WithFallback(h http.Handler) Option {
	return func(o *serverOptions) { o.fallback = h }
}

// WithLogger enables async request logging to the control-api.
func WithLogger(lg *logger.Client) Option {
	return func(o *serverOptions) { o.logger = lg }
}

// WithDebug disables route caching so every request re-resolves the rule.
func WithDebug(debug bool) Option {
	return func(o *serverOptions) { o.debug = debug }
}

// WithX402Timeouts overrides the x402 verify/request deadline and the (longer)
// on-chain settlement deadline. Non-positive values fall back to the defaults.
func WithX402Timeouts(request, settle time.Duration) Option {
	return func(o *serverOptions) {
		o.x402Timeout = request
		o.x402SettleTTL = settle
	}
}

// New creates a proxy server backed by the given provider.
// Rules are resolved per-request and cached after the first lookup.
func New(provider rules.Provider, opts ...Option) (*Server, error) {
	if provider == nil {
		return nil, errors.New("provider is required")
	}
	o := &serverOptions{}
	for _, opt := range opts {
		opt(o)
	}
	if o.logger == nil {
		o.logger = logger.New("", "") // no-op client
	}
	if o.x402Timeout <= 0 {
		o.x402Timeout = x402DefaultTimeout
	}
	if o.x402SettleTTL <= 0 {
		o.x402SettleTTL = x402DefaultSettleTimeout
	}
	st := newState(provider, o.fallback, o.logger, o.debug, o.x402Timeout, o.x402SettleTTL)
	return &Server{handler: st.buildRouter()}, nil
}

// NewReverseProxy builds a plain reverse proxy with optional auth header injection
// and x402 settlement header processing on responses.
func NewReverseProxy(target string, auth config.AuthHeaderConfig) (http.Handler, error) {
	upURL, err := url.Parse(target)
	if err != nil {
		return nil, err
	}
	rp := httputil.NewSingleHostReverseProxy(upURL)
	origDirector := rp.Director
	authName, authValue := auth.Name, auth.Value
	rp.Director = func(r *http.Request) {
		origDirector(r)
		if auth.Enable && authName != "" && authValue != "" {
			r.Header.Set(authName, authValue)
		}
	}
	rp.ModifyResponse = func(resp *http.Response) error {
		scheme, _ := resp.Request.Context().Value(matchedRuleSchemeContextKey).(string)
		applyUpstreamSettlementHeaders(resp.Header, scheme)
		return nil
	}
	return rp, nil
}

func (s *Server) Handler() http.Handler { return s.handler }

func (s *Server) HTTPServer(addr string) *http.Server {
	return &http.Server{Addr: addr, Handler: s.handler}
}

func (s *Server) Run(addr string) error {
	log.Printf("proxy listening on %s", addr)
	return s.HTTPServer(addr).ListenAndServe()
}
