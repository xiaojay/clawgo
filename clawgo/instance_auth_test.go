package clawgo

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
)

func generateTestInstanceToken(instanceID, userID int64, secret string, ttl time.Duration) string {
	now := time.Now()
	claims := jwt.MapClaims{
		"instance_id": instanceID,
		"user_id":     userID,
		"scope":       "instance_internal",
		"iat":         now.Unix(),
		"exp":         now.Add(ttl).Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, _ := token.SignedString([]byte(secret))
	return signed
}

func TestInstanceAuth_ValidToken(t *testing.T) {
	secret := "test-secret"
	token := generateTestInstanceToken(42, 7, secret, time.Hour)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		assert.Equal(t, int64(42), InstanceIDFromContext(ctx))
		assert.Equal(t, int64(7), UserIDFromContext(ctx))
		w.WriteHeader(http.StatusOK)
	})

	mw := InstanceAuthMiddleware(secret)
	req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	mw(handler).ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestInstanceAuth_MissingToken(t *testing.T) {
	mw := InstanceAuthMiddleware("secret")
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("should not reach handler")
	})

	req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	w := httptest.NewRecorder()
	mw(handler).ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestInstanceAuth_InvalidToken(t *testing.T) {
	mw := InstanceAuthMiddleware("secret")
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("should not reach handler")
	})

	req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	req.Header.Set("Authorization", "Bearer invalid-token")
	w := httptest.NewRecorder()
	mw(handler).ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestInstanceAuth_ExpiredToken(t *testing.T) {
	secret := "test-secret"
	token := generateTestInstanceToken(42, 7, secret, -time.Hour)

	mw := InstanceAuthMiddleware(secret)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("should not reach handler")
	})

	req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	mw(handler).ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestInstanceAuth_NoSecretDisabled(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	mw := InstanceAuthMiddleware("")
	req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	w := httptest.NewRecorder()
	mw(handler).ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}
