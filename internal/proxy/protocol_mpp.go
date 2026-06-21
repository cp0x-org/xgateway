package proxy

import (
	"fmt"
	"log"
	"math"
	"strconv"

	"github.com/gin-gonic/gin"
	mppserver "github.com/tempoxyz/mpp-go/pkg/server"
	chargeserver "github.com/tempoxyz/mpp-go/pkg/tempo/server"

	"github.com/cp0x-org/xpaywall/xgateway/internal/rules"
)

const (
	protoMPP = "mpp"
	// mppRealm is the WWW-Authenticate realm advertised in MPP 402 challenges.
	mppRealm = "xgateway"
)

// mppProtocol verifies one route's payment using the MPP Tempo "charge" scheme.
//
// A one-time charge is verified up front from the request's Authorization
// header: no payment proof means a 402 challenge is returned; a valid credential
// is verified before the request reaches the upstream and a Payment-Receipt
// header is attached to the response. There is no post-response settlement step
// for charges.
type mppProtocol struct {
	srv         *mppserver.Mpp
	amount      string
	description string
}

func (p *mppProtocol) Handle(c *gin.Context) {
	if p.srv == nil {
		c.Next()
		return
	}

	result, err := p.srv.Charge(c.Request.Context(), mppserver.ChargeParams{
		Authorization: c.GetHeader("Authorization"),
		Amount:        p.amount,
		Description:   p.description,
	})
	if err != nil {
		log.Printf("[mpp] charge error: %v", err)
		mppserver.WritePaymentError(c.Writer, err)
		c.Abort()
		return
	}
	if result.Challenge != nil {
		log.Printf("[mpp] payment required, returning 402 challenge")
		mppserver.WriteChallenge(c.Writer, result.Challenge, p.srv.Realm())
		c.Abort()
		return
	}

	log.Printf("[mpp] payment verified, proceeding to upstream")
	c.Request = c.Request.WithContext(
		mppserver.ContextWithPayment(c.Request.Context(), result.Credential, result.Receipt),
	)
	c.Header("Payment-Receipt", result.Receipt.ToPaymentReceipt())
	c.Next()
}

// buildMPPProtocol constructs an mppProtocol for a route from its MPP channels.
// Only the Tempo "charge" scheme is supported; a single charge method backs the
// route, so when multiple MPP channels are present the first is used.
func buildMPPProtocol(rule *rules.Rule, reqPath string, channels []*rules.PaymentChannel) (*mppProtocol, error) {
	if len(channels) == 0 {
		return nil, nil
	}
	ch := channels[0]
	if len(channels) > 1 {
		log.Printf("[entry] mpp routeKey=%s has %d channels; using first (method=%s)", reqPath, len(channels), ch.Method)
	}

	price := ch.Price
	if price == "" {
		price = rule.Price
	}
	amount := humanAmount(price, ch.Decimals)

	log.Printf("[entry] mpp channel method=%s scheme=%s rpc_url=%q merchant=%q amount=%s",
		ch.Method, ch.Scheme, ch.ChannelConfig["rpc_url"], ch.ChannelConfig["merchant"], amount)

	method, err := chargeserver.MethodFromConfig(chargeserver.Config{
		RPCURL:    ch.ChannelConfig["rpc_url"],
		Recipient: ch.ChannelConfig["merchant"],
		Currency:  ch.ChannelConfig["asset"],
		Decimals:  int(ch.Decimals),
	})
	if err != nil {
		return nil, fmt.Errorf("build mpp tempo method: %w", err)
	}

	return &mppProtocol{
		srv:         mppserver.New(method, mppRealm, ch.ChannelConfig["secret_key"]),
		amount:      amount,
		description: rule.Description,
	}, nil
}

// humanAmount converts a raw base-unit price (e.g. "1000") into the
// human-readable decimal string the MPP charge API expects (e.g. "0.001000"),
// which the Tempo method re-scales by the same `decimals`.
func humanAmount(rawPrice string, decimals int32) string {
	if rawPrice == "" {
		return "0"
	}
	if decimals <= 0 {
		return rawPrice
	}
	raw, err := strconv.ParseFloat(rawPrice, 64)
	if err != nil {
		return rawPrice
	}
	return strconv.FormatFloat(raw/math.Pow10(int(decimals)), 'f', int(decimals), 64)
}
