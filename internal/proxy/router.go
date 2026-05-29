package proxy

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

const (
	ctxEntryKey   = "proxy.entry"
	pendingLogTTL = 10 * time.Minute
)

func (s *state) buildRouter() http.Handler {
	router := gin.New()
	router.Use(gin.Recovery())
	router.Any("/*path", s.loggingMiddleware(), s.resolve(), s.proxyUpstream())

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.healthHandler())
	mux.Handle("/", router)
	return mux
}

func (s *state) healthHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, `{"status":"ok","started_at":%q,"uptime_seconds":%d}`,
			s.startedAt.UTC().Format(time.RFC3339),
			int64(time.Since(s.startedAt).Seconds()),
		)
	}
}

// loggingMiddleware wraps the entire request: sets up logContext, runs handlers,
// then dispatches collected data to the logger in a background goroutine.
//
// HEAD requests are skipped entirely — browsers send them automatically and
// they carry no useful information for payment tracking.
//
// For payment-required routes the middleware maintains a short-lived in-memory
// map keyed by (method+path+clientIP). When a 402 is returned the entry is
// stored so the subsequent paid retry can UPDATE the same DB row instead of
// creating a duplicate.
func (s *state) loggingMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Browser HEAD requests are noise — skip entirely.
		if c.Request.Method == http.MethodHead {
			c.Next()
			return
		}

		if s.debug {
			log.Printf("[debug] → %s %s from=%s", c.Request.Method, c.Request.URL.RequestURI(), c.ClientIP())
			for name, values := range c.Request.Header {
				for _, v := range values {
					log.Printf("[debug]   %s: %s", name, v)
				}
			}
		}

		lc := newLogContext()
		fp := logFingerprint(c.Request.Method, c.Request.URL.Path, c.ClientIP())

		// Reuse an existing log ID if this is a retry of a recent 402.
		if v, ok := s.pendingLogs.Load(fp); ok {
			pe := v.(pendingLogEntry)
			if time.Now().Before(pe.expiresAt) {
				lc.logID = pe.logID
				lc.isRetry = true
			} else {
				s.pendingLogs.Delete(fp)
			}
		}

		c.Set(ctxLogKey, lc)
		c.Next()

		// Update the pending-log cache based on outcome.
		if lc.ruleFound {
			switch lc.paymentEvent {
			case statusPaymentRequired:
				// Store so the next retry can find this log ID.
				s.pendingLogs.Store(fp, pendingLogEntry{
					logID:     lc.logID,
					expiresAt: time.Now().Add(pendingLogTTL),
				})
			default:
				// Request completed (paid or free) — remove pending entry.
				s.pendingLogs.Delete(fp)
			}
		}

		lc.dispatch(s.logger)
	}
}

// resolve looks up (or builds) the per-route entry, attaches it to the gin
// context, runs the payment middleware where applicable, and populates the
// log context with rule + payment information.
func (s *state) resolve() gin.HandlerFunc {
	return func(c *gin.Context) {
		reqPath := c.Request.URL.Path

		var e *entry
		if !s.debug {
			if cached, ok := s.getRoute(reqPath); ok {
				applyEntry(c, cached, reqPath)
				return
			}
		} else {
			log.Printf("[debug] cache bypassed for %s %s", c.Request.Method, reqPath)
		}

		rule, err := s.provider.GetByInboundPath(c.Request.Context(), reqPath)
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": "resolve failed: " + err.Error()})
			c.Abort()
			return
		}
		if rule == nil {
			s.serveNoRule(c, reqPath)
			return
		}

		if s.debug {
			log.Printf("[debug] resolved rule: path=%s target=%s price=%s channels=%d free=%v",
				rule.InboundPath, rule.Target, rule.Price, len(rule.PaymentChannels), rule.Free)
		}

		e, err = s.buildEntry(c.Request.Context(), rule, reqPath)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "payment setup failed: " + err.Error()})
			c.Abort()
			return
		}
		if !s.debug {
			s.putRoute(reqPath, e)
		}

		applyEntry(c, e, reqPath)
	}
}

// serveNoRule handles requests whose path matches no rule: delegates to the
// fallback handler when one is configured, otherwise responds 404.
func (s *state) serveNoRule(c *gin.Context, reqPath string) {
	if s.fallback != nil {
		s.fallback.ServeHTTP(c.Writer, c.Request)
		c.Abort()
		return
	}
	c.JSON(http.StatusNotFound, gin.H{
		"error":  "route not found",
		"path":   reqPath,
		"status": http.StatusNotFound,
	})
	c.Abort()
}

// applyEntry attaches the entry to the request, runs payment middleware (when
// required), and records the resulting rule / payment state on the log context.
func applyEntry(c *gin.Context, e *entry, reqPath string) {
	if lc := getLogContext(c); lc != nil {
		clientIP := c.ClientIP()
		userAgent := c.GetHeader("User-Agent")
		lc.setRule(&e.rule, c.Request.Method, reqPath, strPtr(clientIP), strPtrIfNotEmpty(userAgent))
	}

	c.Request = c.Request.WithContext(
		context.WithValue(c.Request.Context(), matchedRuleSchemeContextKey, e.scheme),
	)
	c.Set(ctxEntryKey, e)

	if e.rule.Free || !e.hasPayment() {
		c.Next()
		return
	}

	usedProtocol := e.runPayment(c)

	// Update log context with payment outcome.
	if lc := getLogContext(c); lc != nil {
		if c.IsAborted() {
			lc.setPaymentRequired()
		} else {
			channelID, assetID, amountUSD := resolvePaymentChannelInfo(e.rule.PaymentChannels, usedProtocol, e.rule.Price)
			lc.setPaymentCompleted(channelID, assetID, amountUSD)
		}
	}
}

func (s *state) proxyUpstream() gin.HandlerFunc {
	return func(c *gin.Context) {
		raw, exists := c.Get(ctxEntryKey)
		if !exists {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "missing route entry"})
			return
		}
		e := raw.(*entry)
		if e.rp == nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": "no upstream configured"})
			return
		}

		lc := getLogContext(c)
		if lc != nil {
			lc.setProxying(e.rule.Target)
		}

		start := time.Now()
		e.rp.ServeHTTP(c.Writer, c.Request)
		elapsedMs := int32(time.Since(start).Milliseconds())
		statusCode := int32(c.Writer.Status())

		if lc != nil {
			finalStatus, errType, errMsg := classifyUpstreamResult(statusCode, nil)
			lc.setUpstreamResult(statusCode, elapsedMs, finalStatus, errType, errMsg)
		}
	}
}
