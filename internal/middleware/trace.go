package middleware

import (
	"net/http"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

// TraceMiddleware creates trace spans for HTTP requests
type TraceMiddleware struct {
	tracer trace.Tracer
	logger *zap.Logger
}

// NewTraceMiddleware creates a new trace middleware
func NewTraceMiddleware(logger *zap.Logger) *TraceMiddleware {
	return &TraceMiddleware{
		tracer: otel.Tracer("deployment-service/api"),
		logger: logger,
	}
}

// Middleware creates a trace span for each request
func (m *TraceMiddleware) Middleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Extract trace context from headers
		ctx := otel.GetTextMapPropagator().Extract(r.Context(), propagation.HeaderCarrier(r.Header))
		
		// Start span
		ctx, span := m.tracer.Start(ctx, r.Method+" "+r.URL.Path)
		defer span.End()

		// Add request attributes
		span.SetAttributes(
			attribute.String("http.method", r.Method),
			attribute.String("http.path", r.URL.Path),
			attribute.String("http.url", r.URL.String()),
		)

		// Create response writer wrapper to capture status code
		rw := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}

		// Call next handler with trace context
		next(rw, r.WithContext(ctx))

		// Set status code attribute
		span.SetAttributes(attribute.Int("http.status_code", rw.statusCode))

		if rw.statusCode >= 400 {
			span.RecordError(nil)
		}
	}
}

// responseWriter wraps http.ResponseWriter to capture status code
type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}
