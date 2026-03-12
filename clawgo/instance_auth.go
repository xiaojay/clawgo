package clawgo

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strings"

	"github.com/golang-jwt/jwt/v5"
)

type contextKey string

const (
	ctxInstanceID contextKey = "instance_id"
	ctxUserID     contextKey = "user_id"
)

func InstanceIDFromContext(ctx context.Context) int64 {
	v, _ := ctx.Value(ctxInstanceID).(int64)
	return v
}

func UserIDFromContext(ctx context.Context) int64 {
	v, _ := ctx.Value(ctxUserID).(int64)
	return v
}

// InstanceAuthMiddleware returns middleware that validates instance JWT tokens.
// If secret is empty, auth is disabled (pass-through).
func InstanceAuthMiddleware(secret string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if secret == "" {
				next.ServeHTTP(w, r)
				return
			}

			header := r.Header.Get("Authorization")
			if header == "" || !strings.HasPrefix(header, "Bearer ") {
				writeAuthError(w, "missing or invalid authorization header")
				return
			}
			tokenStr := strings.TrimPrefix(header, "Bearer ")

			token, err := jwt.Parse(tokenStr, func(token *jwt.Token) (interface{}, error) {
				return []byte(secret), nil
			})
			if err != nil || !token.Valid {
				writeAuthError(w, "invalid or expired token")
				return
			}

			claims, ok := token.Claims.(jwt.MapClaims)
			if !ok {
				writeAuthError(w, "invalid token claims")
				return
			}

			instanceID, _ := claims["instance_id"].(float64)
			userID, _ := claims["user_id"].(float64)

			if instanceID == 0 {
				writeAuthError(w, "missing instance_id in token")
				return
			}

			ctx := r.Context()
			ctx = context.WithValue(ctx, ctxInstanceID, int64(instanceID))
			ctx = context.WithValue(ctx, ctxUserID, int64(userID))

			log.Printf("instance_auth instance_id=%d user_id=%d path=%s", int64(instanceID), int64(userID), r.URL.Path)

			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func writeAuthError(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"error": map[string]string{
			"message": msg,
			"type":    "authentication_error",
		},
	})
}
