package zztgo

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// M18.19 — public liveness, protected service detail.

func TestM1819HealthIsPublicAndAggregated(t *testing.T) {
	server := NewWebSocketServer(testEmptyWorld(t), 1)
	mux := (&WebAPI{Server: server}).Handler()

	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	req.RemoteAddr = "203.0.113.7:41000"
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/health from a public client = %d, want 200", rec.Code)
	}

	var body map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode health: %v", err)
	}
	if _, ok := body["totals"]; !ok {
		t.Fatalf("health body has no aggregate totals: %s", rec.Body.String())
	}
	if _, ok := body["memory"]; ok {
		t.Fatalf("health leaked memory detail: %s", rec.Body.String())
	}
	if _, ok := body["instances"]; ok {
		t.Fatalf("health leaked instance detail: %s", rec.Body.String())
	}
}

func TestM1819MetricsAreLocalOrOperatorOnly(t *testing.T) {
	auth := NewAuthService("client-id", "", "", []byte("m18-19-cookie-secret"))
	server := NewWebSocketServer(testEmptyWorld(t), 1)
	server.Auth = auth
	server.Moderators = map[string]bool{m212OperatorAccount: true}
	mux := (&WebAPI{Server: server, Auth: auth}).Handler()

	cases := []struct {
		name       string
		remoteAddr string
		forwarded  string
		cookie     *http.Cookie
		want       int
	}{
		{
			name:       "direct public client gets no detail",
			remoteAddr: "203.0.113.7:41000",
			want:       http.StatusNotFound,
		},
		{
			name:       "public client behind loopback proxy gets no detail",
			remoteAddr: "127.0.0.1:41000",
			forwarded:  "203.0.113.7",
			want:       http.StatusNotFound,
		},
		{
			name:       "local maintenance call gets detail",
			remoteAddr: "127.0.0.1:41000",
			want:       http.StatusOK,
		},
		{
			name:       "operator gets detail remotely",
			remoteAddr: "203.0.113.7:41000",
			cookie:     signedAuthCookie(t, auth, AuthenticatedAccount{ID: m212OperatorAccount, Name: "Ada"}),
			want:       http.StatusOK,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/metrics", nil)
			req.RemoteAddr = tc.remoteAddr
			if tc.forwarded != "" {
				req.Header.Set("X-Forwarded-For", tc.forwarded)
			}
			if tc.cookie != nil {
				req.AddCookie(tc.cookie)
			}
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("GET /api/metrics = %d, want %d; body=%q", rec.Code, tc.want, rec.Body.String())
			}
			if rec.Code == http.StatusOK {
				var status ServiceStatus
				if err := json.Unmarshal(rec.Body.Bytes(), &status); err != nil {
					t.Fatalf("decode metrics: %v", err)
				}
				if status.Status != "ok" || status.Memory.Goroutines == 0 || status.Totals.Instances == 0 {
					t.Fatalf("metrics detail incomplete: %+v", status)
				}
			}
		})
	}
}

func TestM1819LocalMaintenanceRequiresTheForwardedClientToBeLocal(t *testing.T) {
	cases := []struct {
		name       string
		remoteAddr string
		forwarded  string
		want       bool
	}{
		{"direct IPv4 loopback", "127.0.0.1:8080", "", true},
		{"direct IPv6 loopback", "[::1]:8080", "", true},
		{"loopback proxy carrying remote client", "127.0.0.1:8080", "198.51.100.9", false},
		{"loopback proxy carrying local client", "127.0.0.1:8080", "127.0.0.1", true},
		{"direct remote ignoring forged header", "198.51.100.9:8080", "127.0.0.1", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/metrics", nil)
			req.RemoteAddr = tc.remoteAddr
			if tc.forwarded != "" {
				req.Header.Set("X-Forwarded-For", tc.forwarded)
			}
			if got := requestIsLocalMaintenance(req); got != tc.want {
				t.Fatalf("requestIsLocalMaintenance = %v, want %v", got, tc.want)
			}
		})
	}
}
