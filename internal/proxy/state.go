package proxy

import (
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/cp0x-org/xpaywall/xgateway/internal/logger"
	"github.com/cp0x-org/xpaywall/xgateway/internal/rules"
)

// state holds shared per-Server resources used across the request lifecycle.
type state struct {
	provider   rules.Provider
	fallback   http.Handler
	logger     *logger.Client
	facilCache *facilitatorCache
	startedAt  time.Time
	debug      bool

	mu         sync.RWMutex
	routeCache map[string]*entry

	pendingLogs sync.Map // logFingerprint → pendingLogEntry
}

func newState(provider rules.Provider, fallback http.Handler, lg *logger.Client, debug bool) *state {
	return &state{
		provider:   provider,
		fallback:   fallback,
		logger:     lg,
		facilCache: newFacilitatorCache(),
		startedAt:  time.Now(),
		debug:      debug,
		routeCache: make(map[string]*entry),
	}
}

func (s *state) getRoute(path string) (*entry, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	e, ok := s.routeCache[path]
	return e, ok
}

func (s *state) putRoute(path string, e *entry) {
	s.mu.Lock()
	s.routeCache[path] = e
	s.mu.Unlock()
}

// pendingLogEntry tracks a 402-response log entry waiting for its payment retry.
type pendingLogEntry struct {
	logID     uuid.UUID
	expiresAt time.Time
}

// logFingerprint returns a key correlating the initial 402 request with its retry.
func logFingerprint(method, path, clientIP string) string {
	return method + "|" + path + "|" + clientIP
}
