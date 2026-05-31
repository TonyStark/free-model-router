package api

import (
	"context"
	"fmt"
	"net/http"
	"sync/atomic"
	"time"

	"free-model-router/internal/logger"
	met "free-model-router/internal/metrics"
)

var requestCounter atomic.Uint64

func newRequestID() string {
	return fmt.Sprintf("req-%04d", requestCounter.Add(1))
}

type ctxKeyReqID struct{}

func reqIDFromCtx(ctx context.Context) string {
	v, _ := ctx.Value(ctxKeyReqID{}).(string)
	return v
}

type statusWriter struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (sw *statusWriter) WriteHeader(code int) {
	sw.status = code
	sw.wrote = true
	sw.ResponseWriter.WriteHeader(code)
}

func (sw *statusWriter) Write(b []byte) (int, error) {
	if !sw.wrote {
		sw.status = 200
		sw.wrote = true
	}
	return sw.ResponseWriter.Write(b)
}

func (sw *statusWriter) Flush() {
	if f, ok := sw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqID := newRequestID()
		ctx := context.WithValue(r.Context(), ctxKeyReqID{}, reqID)
		r = r.WithContext(ctx)

		sw := &statusWriter{ResponseWriter: w, status: 200}
		start := time.Now()
		met.IncTotalRequests()
		met.IncActiveRequests()
		defer met.DecActiveRequests()

		logger.ReqDebug(reqID, "→ %s %s  from %s", r.Method, r.URL.Path, r.RemoteAddr)
		next.ServeHTTP(sw, r)

		dur := time.Since(start)
		statusColor := logger.ColorGreen
		if sw.status >= 500 {
			statusColor = logger.ColorRed
		} else if sw.status >= 400 {
			statusColor = logger.ColorYellow
		}
		durStr := dur.Round(time.Millisecond).String()
		if dur < time.Millisecond {
			durStr = dur.Round(time.Microsecond).String()
		}
		logger.Emit("REQ", logger.ColorBlue, reqID,
			"%s%-4s%s %s  %s%d%s  %s%s%s",
			logger.ColorBold, r.Method, logger.ColorReset,
			r.URL.Path,
			statusColor, sw.status, logger.ColorReset,
			logger.ColorGray, durStr, logger.ColorReset,
		)
	})
}

func rateLimitMiddleware(sem chan struct{}) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
				next.ServeHTTP(w, r)
			default:
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusTooManyRequests)
				w.Write([]byte(`{"error":"too many concurrent requests, try again"}`))
			}
		})
	}
}
