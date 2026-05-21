package middleware

import (
	"net/http"

	"go.uber.org/zap"
)

// AuthMiddleware validates the deploy token
type AuthMiddleware struct {
	deployToken string
	logger      *zap.Logger
}

// NewAuthMiddleware creates a new auth middleware
func NewAuthMiddleware(deployToken string, logger *zap.Logger) *AuthMiddleware {
	return &AuthMiddleware{
		deployToken: deployToken,
		logger:      logger,
	}
}

// Middleware validates the x-deploy-token header
func (m *AuthMiddleware) Middleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := r.Header.Get("x-deploy-token")
		if token == "" {
			m.logger.Warn("Missing deploy token")
			http.Error(w, "Unauthorized: missing deploy token", http.StatusUnauthorized)
			return
		}

		if token != m.deployToken {
			m.logger.Warn("Invalid deploy token")
			http.Error(w, "Unauthorized: invalid deploy token", http.StatusUnauthorized)
			return
		}

		next(w, r)
	}
}
