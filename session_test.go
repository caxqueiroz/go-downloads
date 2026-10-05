package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestValidSessionToken(t *testing.T) {
	sessionSecret = []byte("test-secret")
	expired := strconv.FormatInt(time.Now().Add(-time.Minute).Unix(), 10)
	b := &bucket{name: "photos", password: "pw"}
	other := &bucket{name: "work", password: "pw"}
	changed := &bucket{name: "photos", password: "new-pw"}

	tests := []struct {
		name  string
		token string
		want  bool
	}{
		{"fresh token", b.newSessionToken(), true},
		{"legacy forged cookie", "authenticated", false},
		{"empty", "", false},
		{"tampered expiry", "9999999999." + b.signSession("1"), false},
		{"bad signature", "9999999999.AAAA", false},
		{"expired", expired + "." + b.signSession(expired), false},
		{"token for another bucket", other.newSessionToken(), false},
		{"token for the default bucket", (&bucket{password: "pw"}).newSessionToken(), false},
		{"token from before a password change", changed.newSessionToken(), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := b.validSessionToken(tt.token); got != tt.want {
				t.Errorf("validSessionToken(%q) = %v, want %v", tt.token, got, tt.want)
			}
		})
	}

	sessionSecret = []byte("other-secret")
	if b.validSessionToken(newSessionTokenWith(b, "test-secret")) {
		t.Error("token signed with a different secret was accepted")
	}
}

func newSessionTokenWith(b *bucket, secret string) string {
	prev := sessionSecret
	sessionSecret = []byte(secret)
	defer func() { sessionSecret = prev }()
	return b.newSessionToken()
}

func TestSessionCookie(t *testing.T) {
	previousSecret, previousPath := sessionSecret, basePath
	t.Cleanup(func() { sessionSecret, basePath = previousSecret, previousPath })
	sessionSecret = []byte("test-secret")
	basePath = "/downloads"
	for _, tt := range []struct {
		name       string
		bucket     *bucket
		setting    string
		secure     bool
		cookieName string
		path       string
	}{
		{"secure default", &bucket{}, "", true, "session", "/downloads/"},
		{"explicit HTTP development", &bucket{}, "false", false, "session", "/downloads/"},
		{"invalid setting stays secure", &bucket{}, "invalid", true, "session", "/downloads/"},
		{"named bucket", &bucket{name: "photos"}, "", true, "session_photos", "/downloads/photos/"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("SESSION_COOKIE_SECURE", tt.setting)
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			tt.bucket.setSessionCookie(c)
			cookies := w.Result().Cookies()
			if len(cookies) != 1 {
				t.Fatalf("got %d cookies, want 1", len(cookies))
			}
			cookie := cookies[0]
			if cookie.Secure != tt.secure {
				t.Errorf("Secure = %v, want %v", cookie.Secure, tt.secure)
			}
			if cookie.Name != tt.cookieName || cookie.Path != tt.path {
				t.Errorf("cookie %q at %q, want %q at %q", cookie.Name, cookie.Path, tt.cookieName, tt.path)
			}
			if !cookie.HttpOnly || cookie.SameSite != http.SameSiteLaxMode {
				t.Errorf("unexpected cookie attributes: %+v", cookie)
			}
			if !tt.bucket.validSessionToken(cookie.Value) {
				t.Error("session cookie has an invalid token")
			}
		})
	}
}

func TestParseBuckets(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    map[string]bucket
		wantErr bool
	}{
		{"unset", "", map[string]bucket{}, false},
		{
			"two buckets",
			`{"photos":{"bucket":"family-photos","password":"pw1"},"work-2":{"bucket":"work-files"}}`,
			map[string]bucket{
				"photos": {name: "photos", s3Name: "family-photos", password: "pw1"},
				"work-2": {name: "work-2", s3Name: "work-files"},
			},
			false,
		},
		{"invalid JSON", `{"photos":`, nil, true},
		{"reserved name", `{"login":{"bucket":"x"}}`, nil, true},
		{"uppercase name", `{"Photos":{"bucket":"x"}}`, nil, true},
		{"name with slash", `{"a/b":{"bucket":"x"}}`, nil, true},
		{"empty name", `{"":{"bucket":"x"}}`, nil, true},
		{"missing S3 bucket", `{"photos":{"password":"pw"}}`, nil, true},
		{"misspelled field", `{"photos":{"bucket":"x","pasword":"pw"}}`, nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseBuckets(tt.raw)
			if (err != nil) != tt.wantErr {
				t.Fatalf("parseBuckets() error = %v, wantErr %v", err, tt.wantErr)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("got %d buckets, want %d", len(got), len(tt.want))
			}
			for name, want := range tt.want {
				if got[name] == nil || *got[name] != want {
					t.Errorf("bucket %q = %+v, want %+v", name, got[name], want)
				}
			}
		})
	}
}

func TestBucketRouting(t *testing.T) {
	previousSecret, previousPath := sessionSecret, basePath
	t.Cleanup(func() { sessionSecret, basePath = previousSecret, previousPath })
	sessionSecret = []byte("test-secret")
	basePath = "/downloads"
	gin.SetMode(gin.TestMode)

	def := &bucket{s3Name: "default", password: "default-pw"}
	photos := &bucket{name: "photos", s3Name: "family-photos", password: "photos-pw"}
	router := newRouter(nil, def, map[string]*bucket{"photos": photos})

	form := func(password string) string { return url.Values{"password": {password}}.Encode() }
	tests := []struct {
		name         string
		method       string
		path         string
		body         string
		cookie       *http.Cookie
		wantStatus   int
		wantLocation string
		wantCookie   string
	}{
		{"default listing needs login", http.MethodGet, "/", "", nil, http.StatusFound, "/downloads/login", ""},
		{"default download needs login", http.MethodGet, "/download/a.txt", "", nil, http.StatusFound, "/downloads/login", ""},
		{"default login page", http.MethodGet, "/login", "", nil, http.StatusOK, "", ""},
		{"default login", http.MethodPost, "/login", form("default-pw"), nil, http.StatusFound, "/downloads/", "session"},
		{"default rejects bucket password", http.MethodPost, "/login", form("photos-pw"), nil, http.StatusUnauthorized, "", ""},
		{"bucket without trailing slash", http.MethodGet, "/photos", "", nil, http.StatusFound, "/downloads/photos/", ""},
		{"bucket listing needs login", http.MethodGet, "/photos/", "", nil, http.StatusFound, "/downloads/photos/login", ""},
		{"bucket download needs login", http.MethodGet, "/photos/download/a.txt", "", nil, http.StatusFound, "/downloads/photos/login", ""},
		{"bucket API needs login", http.MethodGet, "/photos/api/files", "", nil, http.StatusFound, "/downloads/photos/login", ""},
		{"bucket login page", http.MethodGet, "/photos/login", "", nil, http.StatusOK, "", ""},
		{"bucket login", http.MethodPost, "/photos/login", form("photos-pw"), nil, http.StatusFound, "/downloads/photos/", "session_photos"},
		{"bucket rejects default password", http.MethodPost, "/photos/login", form("default-pw"), nil, http.StatusUnauthorized, "", ""},
		{
			"bucket rejects default session", http.MethodGet, "/photos/api/files", "",
			&http.Cookie{Name: "session_photos", Value: def.newSessionToken()},
			http.StatusFound, "/downloads/photos/login", "",
		},
		{"unknown bucket", http.MethodGet, "/nope/", "", nil, http.StatusNotFound, "", ""},
		{"unknown bucket without trailing slash", http.MethodGet, "/nope", "", nil, http.StatusNotFound, "", ""},
		{"unknown bucket login page", http.MethodGet, "/nope/login", "", nil, http.StatusNotFound, "", ""},
		{"unknown bucket login", http.MethodPost, "/nope/login", form("photos-pw"), nil, http.StatusNotFound, "", ""},
		{"unknown bucket download", http.MethodGet, "/nope/download/a.txt", "", nil, http.StatusNotFound, "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, strings.NewReader(tt.body))
			if tt.method == http.MethodPost {
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			}
			if tt.cookie != nil {
				req.AddCookie(tt.cookie)
			}
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			if w.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", w.Code, tt.wantStatus)
			}
			if got := w.Header().Get("Location"); got != tt.wantLocation {
				t.Errorf("Location = %q, want %q", got, tt.wantLocation)
			}
			var gotCookie string
			if cookies := w.Result().Cookies(); len(cookies) > 0 {
				gotCookie = cookies[0].Name
			}
			if gotCookie != tt.wantCookie {
				t.Errorf("cookie = %q, want %q", gotCookie, tt.wantCookie)
			}
		})
	}

	t.Run("login form posts to its own bucket", func(t *testing.T) {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/photos/login", nil))
		if !strings.Contains(w.Body.String(), `action="/downloads/photos/login"`) {
			t.Errorf("login form has the wrong action: %s", w.Body.String())
		}
	})
}
