package ws

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"termcore/session"
)

func upgradeRequest(target string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:7795"+target, nil)
	req.Host = "127.0.0.1:7795"
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Sec-WebSocket-Version", "13")
	req.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
	return req
}

func testHandler(t *testing.T, origin string, allowNoOrigin bool) *Handler {
	t.Helper()
	dir := t.TempDir()
	roots, err := session.ParseRoots(dir)
	if err != nil {
		t.Fatal(err)
	}
	handler := &Handler{
		Roots:         roots,
		Manager:       session.New(dir, 4),
		Home:          dir,
		PeroURL:       "/pero/",
		AllowNoOrigin: allowNoOrigin,
	}
	if origin != "" {
		handler.OriginAllowed = func(r *http.Request) bool {
			return r.Header.Get("Origin") == origin
		}
	}
	return handler
}

func TestForeignOriginRejected(t *testing.T) {
	handler := testHandler(t, "https://host.example:8443", false)
	req := upgradeRequest("/api/ws?session=demo")
	req.Header.Set("Origin", "http://evil.example")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", recorder.Code)
	}
}

func TestMissingOriginRejected(t *testing.T) {
	handler := testHandler(t, "https://host.example:8443", false)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, upgradeRequest("/api/ws?session=demo"))

	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", recorder.Code)
	}
}

func TestWrongSchemeRejected(t *testing.T) {
	handler := testHandler(t, "https://host.example:8443", false)
	req := upgradeRequest("/api/ws?session=demo")
	req.Header.Set("Origin", "http://host.example:8443")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", recorder.Code)
	}
}

func TestWrongPortRejected(t *testing.T) {
	handler := testHandler(t, "https://host.example:8443", false)
	req := upgradeRequest("/api/ws?session=demo")
	req.Header.Set("Origin", "https://host.example:443")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", recorder.Code)
	}
}

func TestInvalidTargetRejected(t *testing.T) {
	handler := testHandler(t, "https://host.example:8443", false)
	req := upgradeRequest("/api/ws?dir=relative")
	req.Header.Set("Origin", "https://host.example:8443")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", recorder.Code)
	}
}

func TestMissingOriginAllowedForCLI(t *testing.T) {
	handler := testHandler(t, "https://host.example:8443", true)
	dir := t.TempDir()
	target := "/api/ws?dir=" + url.QueryEscape(dir)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, upgradeRequest(target))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (target reached)", recorder.Code)
	}
}
