package middleware

import (
	"log"
	"net/http"
	"time"
)

// responseWriter wraps http.ResponseWriter to capture status code
type responseWriter struct {
	http.ResponseWriter
	statusCode  int
	wroteHeader bool
}

func (rw *responseWriter) WriteHeader(code int) {
	if rw.wroteHeader {
		return
	}
	rw.statusCode = code
	rw.wroteHeader = true
	rw.ResponseWriter.WriteHeader(code)
}

func (rw *responseWriter) Write(b []byte) (int, error) {
	if !rw.wroteHeader {
		rw.WriteHeader(http.StatusOK)
	}
	return rw.ResponseWriter.Write(b)
}

// Flush implements http.Flusher to support streaming responses
func (rw *responseWriter) Flush() {
	if f, ok := rw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// RequestLogging middleware logs every request and response status code
func RequestLogging(verbose bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !verbose {
				next.ServeHTTP(w, r)
				return
			}

			startTime := time.Now()

			// Wrap the response writer to capture status code
			wrapped := &responseWriter{
				ResponseWriter: w,
				statusCode:     http.StatusOK,
				wroteHeader:    false,
			}

			// Log incoming request
			log.Printf("[VERBOSE] Request: %s %s", r.Method, r.URL.Path)

			// Call the next handler
			next.ServeHTTP(wrapped, r)

			// Log response with status code and latency
			latency := time.Since(startTime)
			log.Printf("[VERBOSE] Response: %s %s - Status: %d - Latency: %v",
				r.Method, r.URL.Path, wrapped.statusCode, latency)
		})
	}
}
