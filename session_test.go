package main

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestValidSessionToken(t *testing.T) {
	sessionSecret = []byte("test-secret")
	expired := strconv.FormatInt(time.Now().Add(-time.Minute).Unix(), 10)

	tests := []struct {
		name  string
		token string
		want  bool
	}{
		{"fresh token", newSessionToken(), true},
		{"legacy forged cookie", "authenticated", false},
		{"empty", "", false},
		{"tampered expiry", "9999999999." + signSession("1"), false},
		{"bad signature", "9999999999.AAAA", false},
		{"expired", expired + "." + signSession(expired), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := validSessionToken(tt.token); got != tt.want {
				t.Errorf("validSessionToken(%q) = %v, want %v", tt.token, got, tt.want)
			}
		})
	}

	sessionSecret = []byte("other-secret")
	if validSessionToken(newSessionTokenWith("test-secret")) {
		t.Error("token signed with a different secret was accepted")
	}
}

func newSessionTokenWith(secret string) string {
	prev := sessionSecret
	sessionSecret = []byte(secret)
	defer func() { sessionSecret = prev }()
	return newSessionToken()
}

func TestSessionCookie(t *testing.T) {
	previousSecret, previousPath := sessionSecret, basePath
	t.Cleanup(func() { sessionSecret, basePath = previousSecret, previousPath })
	sessionSecret = []byte("test-secret")
	basePath = "/downloads"
	for _, tt := range []struct {
		name    string
		setting string
		secure  bool
	}{
		{"secure default", "", true},
		{"explicit HTTP development", "false", false},
		{"invalid setting stays secure", "invalid", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("SESSION_COOKIE_SECURE", tt.setting)
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			setSessionCookie(c)
			cookies := w.Result().Cookies()
			if len(cookies) != 1 {
				t.Fatalf("got %d cookies, want 1", len(cookies))
			}
			cookie := cookies[0]
			if cookie.Secure != tt.secure {
				t.Errorf("Secure = %v, want %v", cookie.Secure, tt.secure)
			}
			if !cookie.HttpOnly || cookie.SameSite != http.SameSiteLaxMode || cookie.Path != "/downloads/" {
				t.Errorf("unexpected cookie attributes: %+v", cookie)
			}
			if !validSessionToken(cookie.Value) {
				t.Error("session cookie has an invalid token")
			}
		})
	}
}
