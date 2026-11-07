package pages

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// Without an addons manager every address is the panel's bare 404, whatever it names;
// only GET is answered at all.
func TestPublicWithoutAddons(t *testing.T) {
	p := NewPublic(Deps{Log: discardLogger(), Now: time.Now})
	for _, path := range []string{
		"/addons-public/status-page/",
		"/addons-public/status-page/index.html",
		"/addons-public/UPPER!/index.html",
		"/addons-public/",
		"/other/index.html",
	} {
		rec := httptest.NewRecorder()
		p.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("GET %s: %d, want 404", path, rec.Code)
		}
	}
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/addons-public/status-page/index.html", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST: %d, want 405", rec.Code)
	}
}

// The public pages run no scripts: the policy the panel puts on them allows styles and
// images only, and nothing may frame them.
func TestPublicHeaders(t *testing.T) {
	h := http.Header{}
	publicHeaders(h)
	csp := h.Get("Content-Security-Policy")
	if !strings.Contains(csp, "default-src 'none'") || strings.Contains(csp, "script-src") && !strings.Contains(csp, "script-src 'none'") {
		t.Fatalf("csp: %s", csp)
	}
	if h.Get("X-Frame-Options") != "DENY" || h.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("headers: %v", h)
	}
}
