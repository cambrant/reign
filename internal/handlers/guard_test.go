package handlers

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/reign/internal/access"
)

func newTestGuard(uid int, uidKnown bool, admins *access.Admins) (*Guard, *bool) {
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	g := NewGuard(next, GuardOptions{Admins: admins, ListenAddr: "127.0.0.1:7890", AllowedHosts: []string{"reign.example"}})
	g.peerUID = func(*http.Request) (int, bool) { return uid, uidKnown }
	return g, &called
}

func TestGuardRejectsBrowserRequests(t *testing.T) {
	tests := []struct {
		name    string
		method  string
		host    string
		headers map[string]string
		body    string
		want    int
	}{
		{"cli get", "GET", "127.0.0.1:7890", nil, "", http.StatusOK},
		{"cli post without body", "POST", "127.0.0.1:7890", nil, "", http.StatusOK},
		{"cli post json", "POST", "127.0.0.1:7890", map[string]string{"Content-Type": "application/json"}, `{}`, http.StatusOK},
		{"json with charset", "PUT", "localhost:7890", map[string]string{"Content-Type": "application/json; charset=utf-8"}, `{}`, http.StatusOK},
		{"ipv6 loopback host", "GET", "[::1]:7890", nil, "", http.StatusOK},
		{"configured host", "GET", "reign.example:7890", nil, "", http.StatusOK},
		{"origin header", "POST", "127.0.0.1:7890", map[string]string{"Origin": "https://evil.example"}, "", http.StatusForbidden},
		{"null origin", "POST", "127.0.0.1:7890", map[string]string{"Origin": "null"}, "", http.StatusForbidden},
		{"cross-site fetch get", "GET", "127.0.0.1:7890", map[string]string{"Sec-Fetch-Site": "cross-site"}, "", http.StatusForbidden},
		{"typed url in browser", "GET", "127.0.0.1:7890", map[string]string{"Sec-Fetch-Site": "none"}, "", http.StatusOK},
		{"dns rebinding host", "GET", "evil.example:7890", nil, "", http.StatusForbidden},
		{"empty host", "GET", "", nil, "", http.StatusForbidden},
		{"text/plain body", "POST", "127.0.0.1:7890", map[string]string{"Content-Type": "text/plain"}, `{"id":"x"}`, http.StatusForbidden},
		{"body without content type", "POST", "127.0.0.1:7890", nil, `{"id":"x"}`, http.StatusForbidden},
		{"form body", "POST", "127.0.0.1:7890", map[string]string{"Content-Type": "application/x-www-form-urlencoded"}, `a=b`, http.StatusForbidden},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g, called := newTestGuard(0, true, access.NewAdmins(""))
			var body *strings.Reader
			if tt.body != "" {
				body = strings.NewReader(tt.body)
			}
			var req *http.Request
			if body != nil {
				req = httptest.NewRequest(tt.method, "/services", body)
			} else {
				req = httptest.NewRequest(tt.method, "/services", nil)
			}
			req.Host = tt.host
			for k, v := range tt.headers {
				req.Header.Set(k, v)
			}
			rec := httptest.NewRecorder()
			g.ServeHTTP(rec, req)
			if rec.Code != tt.want {
				t.Fatalf("status = %d, want %d (%s)", rec.Code, tt.want, rec.Body.String())
			}
			if *called != (tt.want == http.StatusOK) {
				t.Errorf("next called = %v", *called)
			}
		})
	}
}

func TestGuardRequiresAdminForChanges(t *testing.T) {
	admins := access.NewAdminsWithLookup("reign", func(uid int, group string) ([]string, string, error) {
		switch uid {
		case 1000:
			return []string{"1000", "990"}, "990", nil
		case 1001:
			return []string{"1001"}, "990", nil
		}
		return nil, "", errors.New("unknown user")
	})
	tests := []struct {
		name     string
		method   string
		uid      int
		uidKnown bool
		open     bool
		want     int
	}{
		{"root may change", "POST", 0, true, false, http.StatusOK},
		{"group member may change", "DELETE", 1000, true, false, http.StatusOK},
		{"other user may not change", "POST", 1001, true, false, http.StatusForbidden},
		{"unknown user may not change", "PUT", 4242, true, false, http.StatusForbidden},
		{"unidentified caller may not change", "POST", 0, false, false, http.StatusForbidden},
		{"other user may read", "GET", 1001, true, false, http.StatusOK},
		{"unidentified caller may read", "GET", 0, false, false, http.StatusOK},
		{"open api lets anyone change", "POST", 1001, true, true, http.StatusOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g, called := newTestGuard(tt.uid, tt.uidKnown, admins)
			g.open = tt.open
			req := httptest.NewRequest(tt.method, "/services/x/start", nil)
			req.Host = "127.0.0.1:7890"
			rec := httptest.NewRecorder()
			g.ServeHTTP(rec, req)
			if rec.Code != tt.want {
				t.Fatalf("status = %d, want %d (%s)", rec.Code, tt.want, rec.Body.String())
			}
			if *called != (tt.want == http.StatusOK) {
				t.Errorf("next called = %v", *called)
			}
		})
	}
}
