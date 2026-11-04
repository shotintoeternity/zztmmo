package zztgo

import (
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"testing"
)

// M36.4: /api/help echoes its title through the same codec as every other
// CP437 field. The title arrives as the client wrote it, widened CP437 runes
// sent as UTF-8 by encodeURIComponent, so it must be decoded before it is
// encoded again; widening the raw UTF-8 bytes double-encoded anything above
// ASCII.
func TestM364HelpTitleRoundTripsCP437(t *testing.T) {
	api := &WebAPI{}
	// "Caf\x82" (é in CP437) as the client sends it: byte 0x82 widened to the
	// rune U+0082, then percent-encoded as UTF-8.
	wire := "Caf\u0082"
	req := httptest.NewRequest("GET", "/api/help?file=ABOUT.HLP&title="+url.QueryEscape(wire), nil)
	rec := httptest.NewRecorder()
	api.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("/api/help status %d: %s", rec.Code, rec.Body.String())
	}
	var msg struct {
		Title string `json:"title"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &msg); err != nil {
		t.Fatal(err)
	}
	if msg.Title != wire {
		t.Fatalf("title came back as %q (%d runes), want %q: the high byte was double-encoded", msg.Title, len([]rune(msg.Title)), wire)
	}
}
