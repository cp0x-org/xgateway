package proxy

import (
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/cp0x-org/xpaywall/xgateway/internal/logger"
	"github.com/cp0x-org/xpaywall/xgateway/internal/rules"
)

const ctxLogKey = "proxy.lc"

type proxyStatus string

const (
	statusStarted          proxyStatus = "started"
	statusPaymentRequired  proxyStatus = "payment_required"
	statusPaymentCompleted proxyStatus = "payment_completed"
	statusProxying         proxyStatus = "proxying"
	statusCompleted        proxyStatus = "completed"
	statusFailed           proxyStatus = "failed"
	statusUpstreamTimeout  proxyStatus = "upstream_timeout"
	statusUpstreamError    proxyStatus = "upstream_error"
)

// logContext accumulates data during a single proxy request and dispatches
// it to the logger as a single sequential goroutine after the request completes.
type logContext struct {
	logID     uuid.UUID
	requestID string

	// isRetry is true when this request is a payment retry for a previously-logged
	// 402 response. The log entry already exists in the DB — only updates are sent.
	isRetry bool

	// populated after rule resolution
	ruleFound       bool
	projectID       uuid.UUID
	outboundRouteID uuid.UUID
	method          string
	path            string
	clientIP        *string
	userAgent       *string
	paymentRequired bool

	// payment outcome (empty = not applicable / free route)
	paymentEvent          proxyStatus
	paymentAt             time.Time
	paymentChannelID      *uuid.UUID
	paymentChannelAssetID *uuid.UUID
	amountUSD             *string

	// upstream outcome
	proxied              bool
	upstreamURL          string
	upstreamStatusCode   int32
	upstreamResponseTime int32
	finalStatus          proxyStatus
	errorType            string
	errorMessage         string
	proxyingAt           time.Time
}

func newLogContext() *logContext {
	return &logContext{
		logID:     uuid.New(),
		requestID: uuid.New().String(),
	}
}

func getLogContext(c *gin.Context) *logContext {
	v, _ := c.Get(ctxLogKey)
	lc, _ := v.(*logContext)
	return lc
}

func (lc *logContext) setRule(rule *rules.Rule, method, path string, clientIP, userAgent *string) {
	lc.ruleFound = true
	lc.projectID = rule.ProjectID
	lc.outboundRouteID = rule.OutboundRouteID
	lc.paymentRequired = !rule.Free && len(rule.PaymentChannels) > 0
	lc.method = method
	lc.path = path
	lc.clientIP = clientIP
	lc.userAgent = userAgent
}

func (lc *logContext) setPaymentRequired() {
	lc.paymentEvent = statusPaymentRequired
	lc.paymentAt = time.Now()
}

func (lc *logContext) setPaymentCompleted(channelID *uuid.UUID, assetID *uuid.UUID, amountUSD *string) {
	lc.paymentEvent = statusPaymentCompleted
	lc.paymentAt = time.Now()
	lc.paymentChannelID = channelID
	lc.paymentChannelAssetID = assetID
	lc.amountUSD = amountUSD
}

func (lc *logContext) setProxying(upstreamURL string) {
	lc.proxied = true
	lc.upstreamURL = upstreamURL
	lc.proxyingAt = time.Now()
}

func (lc *logContext) setUpstreamResult(statusCode int32, elapsedMs int32, finalStatus proxyStatus, errType, errMsg string) {
	lc.upstreamStatusCode = statusCode
	lc.upstreamResponseTime = elapsedMs
	lc.finalStatus = finalStatus
	lc.errorType = errType
	lc.errorMessage = errMsg
}

// dispatch sends all collected log data to the logger client as a single
// background goroutine. Must be called once after the request completes.
//
// Lifecycle:
//   - 402 response (new entry): CREATE with status=payment_required.
//   - 402 response (retry=true): nothing — this shouldn't happen.
//   - Proxied request (new entry): CREATE + UPDATE chain.
//   - Proxied request (retry=true): UPDATE chain only — entry already exists from 402.
func (lc *logContext) dispatch(lg *logger.Client) {
	if !lg.Enabled() || !lc.ruleFound {
		return
	}

	paymentCompleted := lc.paymentEvent == statusPaymentCompleted

	if lc.isRetry {
		// Entry already exists from the initial 402 — only send updates.
		if !lc.proxied {
			return
		}
		lg.DispatchUpdates(lc.logID, lc.buildUpdates(paymentCompleted))
		return
	}

	outboundRouteID := lc.outboundRouteID
	create := logger.CreateLogReq{
		ID:              lc.logID,
		ProjectID:       lc.projectID,
		OutboundRouteID: &outboundRouteID,
		RequestID:       lc.requestID,
		Method:          lc.method,
		Path:            lc.path,
		ClientIP:        lc.clientIP,
		UserAgent:       lc.userAgent,
		Status:          string(statusStarted),
		PaymentRequired: lc.paymentRequired,
	}

	if !lc.proxied {
		// 402 response: create entry with payment_requested_at timestamp.
		if lc.paymentEvent == statusPaymentRequired {
			create.Status = string(statusPaymentRequired)
			at := lc.paymentAt
			create.PaymentRequestedAt = &at
		}
		lg.Dispatch(create, nil)
		return
	}

	// Proxied (new entry): create + full update chain.
	lg.Dispatch(create, lc.buildUpdates(paymentCompleted))
}

// buildUpdates constructs the ordered UPDATE sequence for a proxied request.
func (lc *logContext) buildUpdates(paymentCompleted bool) []logger.UpdateLogReq {
	var updates []logger.UpdateLogReq

	if lc.paymentEvent == statusPaymentCompleted {
		at := lc.paymentAt
		updates = append(updates, logger.UpdateLogReq{
			Status:                string(statusPaymentCompleted),
			PaymentRequired:       lc.paymentRequired,
			PaymentCompleted:      true,
			PaymentCompletedAt:    &at,
			PaymentChannelID:      lc.paymentChannelID,
			PaymentChannelAssetID: lc.paymentChannelAssetID,
			AmountUSD:             lc.amountUSD,
		})
	}

	upstreamURL := lc.upstreamURL
	updates = append(updates, logger.UpdateLogReq{
		Status:           string(statusProxying),
		PaymentRequired:  lc.paymentRequired,
		PaymentCompleted: paymentCompleted,
		UpstreamURL:      &upstreamURL,
	})

	if lc.finalStatus != "" {
		upURL := lc.upstreamURL
		u := logger.UpdateLogReq{
			Status:                 string(lc.finalStatus),
			PaymentRequired:        lc.paymentRequired,
			PaymentCompleted:       paymentCompleted,
			UpstreamURL:            &upURL,
			UpstreamStatusCode:     &lc.upstreamStatusCode,
			UpstreamResponseTimeMs: &lc.upstreamResponseTime,
			FinalStatusCode:        &lc.upstreamStatusCode,
		}
		if lc.errorType != "" {
			u.ErrorType = &lc.errorType
		}
		if lc.errorMessage != "" {
			u.ErrorMessage = &lc.errorMessage
		}
		updates = append(updates, u)
	}

	return updates
}
