package proxy

import (
	"bytes"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
)

type ginAdapter struct {
	ctx *gin.Context
}

func (a *ginAdapter) GetHeader(name string) string { return a.ctx.GetHeader(name) }
func (a *ginAdapter) GetMethod() string            { return a.ctx.Request.Method }
func (a *ginAdapter) GetPath() string              { return a.ctx.Request.URL.Path }
func (a *ginAdapter) GetAcceptHeader() string      { return a.ctx.GetHeader("Accept") }
func (a *ginAdapter) GetUserAgent() string         { return a.ctx.GetHeader("User-Agent") }
func (a *ginAdapter) GetURL() string {
	if pub := strings.TrimRight(os.Getenv("PUBLIC_URL"), "/"); pub != "" {
		return pub + a.ctx.Request.URL.RequestURI()
	}
	scheme := "http"
	if a.ctx.Request.TLS != nil {
		scheme = "https"
	}
	if fwdProto := a.ctx.GetHeader("X-Forwarded-Proto"); fwdProto != "" {
		scheme = fwdProto
	}
	host := a.ctx.GetHeader("X-Forwarded-Host")
	if host == "" {
		host = a.ctx.Request.Host
	}
	if host == "" {
		host = a.ctx.GetHeader("Host")
	}
	return fmt.Sprintf("%s://%s%s", scheme, host, a.ctx.Request.URL.RequestURI())
}

type responseCapture struct {
	gin.ResponseWriter
	body       *bytes.Buffer
	statusCode int
	written    bool
	mu         sync.Mutex
}

func (w *responseCapture) WriteHeader(code int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.written {
		w.statusCode = code
		w.written = true
	}
}

func (w *responseCapture) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.written {
		w.statusCode = http.StatusOK
		w.written = true
	}
	return w.body.Write(data)
}

func (w *responseCapture) WriteString(s string) (int, error) { return w.Write([]byte(s)) }
func (w *responseCapture) Flush()                            {}
func (w *responseCapture) WriteHeaderNow()                   {}

func (w *responseCapture) Status() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.statusCode
}

func (w *responseCapture) Size() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.body.Len()
}

func (w *responseCapture) Written() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.written
}
