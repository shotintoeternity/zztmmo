package zztgo

// M37.5 — accented names keep their accents.
//
// Typed text (chat, player names, high-score names) used to be folded to
// printable ASCII, so a Google display name "José" was shown as "Jos". It is
// now encoded to CP437 where it is admitted: é is byte 0x82, and the client
// draws it from the atlas like any other world byte. A character CP437 cannot
// draw is folded (dashes, curly quotes) or dropped (日), as before.

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"
)

// m375Codec is CP437 0x80-0xFF as Python's cp437 codec (the Unicode.org
// mapping) decodes it, one character per byte. It is a second, independent
// copy of cp437_table.go, so a hand edit to either one shows up here.
const m375Codec = "ÇüéâäàåçêëèïîìÄÅÉæÆôöòûùÿÖÜ¢£¥₧ƒ" +
	"áíóúñÑªº¿⌐¬½¼¡«»░▒▓│┤╡╢╖╕╣║╗╝╜╛┐" +
	"└┴┬├─┼╞╟╚╔╩╦╠═╬╧╨╤╥╙╘╒╓╫╪┘┌█▄▌▐▀" +
	"αßΓπΣσµτΦΘΩδ∞φε∩≡±≥≤⌠⌡÷≈°∙·√ⁿ²■\u00a0"

func TestM375TableMatchesTheCodec(t *testing.T) {
	want := []rune(m375Codec)
	if len(want) != 128 {
		t.Fatalf("codec literal has %d characters, want 128", len(want))
	}
	for i, r := range want {
		if cp437HighRunes[i] != r {
			t.Errorf("byte %#x: table has %U, codec has %U", 0x80+i, cp437HighRunes[i], r)
		}
		if b, ok := cp437ByteForRune[r]; !ok || b != byte(0x80+i) {
			t.Errorf("%U: inverse gives %#x (%v), want %#x", r, b, ok, 0x80+i)
		}
	}
}

func TestM375TypedTextIsEncodedToCP437(t *testing.T) {
	cases := []struct{ typed, want string }{
		{"José", "Jos\x82"},
		{"Zoë Müller", "Zo\x89 M\x81ller"},
		{"Ñandú", "\xa5and\xa3"},
		{"£5 ½", "\x9c5 \xab"},
		{"Straße", "Stra\xe1e"},
		{"plain ASCII", "plain ASCII"},
		{"O’Neil — “hi”…", "O'Neil - \"hi\"..."}, // CP437 has none of these; folded as before
		{"日本", ""},                               // no CP437 form: dropped as before
		{"a\u00a0b", "a b"},                      // a no-break space is a space
		{"a\x00b\tc\x7fd", "abcd"},               // controls dropped
		{"\U0001f642", ""},
	}
	for _, c := range cases {
		if got := encodeTypedCP437(c.typed); got != c.want {
			t.Errorf("encodeTypedCP437(%q) = %q, want %q", c.typed, got, c.want)
		}
		got := encodeTypedCP437(c.typed)
		if again := encodeTypedCP437(got); again != got {
			t.Errorf("encodeTypedCP437 is not idempotent on %q: %q then %q", c.typed, got, again)
		}
	}
	if got, ok := admitChatText("José"); !ok || got != "Jos\x82" {
		t.Errorf("admitChatText(José) = %q, %v", got, ok)
	}
	if got := admitPlayerName("José"); got != "Jos\x82" {
		t.Errorf("admitPlayerName(José) = %q", got)
	}
	if got := admitHighScoreName("José"); got != "Jos\x82" {
		t.Errorf("admitHighScoreName(José) = %q", got)
	}
	// The cap is still a glyph count: one byte per glyph.
	if got := admitPlayerName(strings.Repeat("é", 100)); got != strings.Repeat("\x82", playerNameMax) {
		t.Errorf("admitPlayerName(100 x é) = %q, want %d x 0x82", got, playerNameMax)
	}
	// A title wordmark is not typed text and still folds (M37.5 leaves it).
	if got := foldWordmark("José"); got != "Jos" {
		t.Errorf("foldWordmark(José) = %q, want Jos", got)
	}
}

// m375Glyphs is what the browser draws for a wire string: each rune masked to
// a byte, the atlas index (main.ts charCodeAt(i) & 0xff).
func m375Glyphs(wire string) []byte {
	var out []byte
	for _, r := range wire {
		out = append(out, byte(r&0xff))
	}
	return out
}

func TestM375RosterCarriesTheCP437Accent(t *testing.T) {
	rm := NewRoomManager(townWorld(t))
	guest := rm.JoinPlayer(1, 0, 0)
	member := rm.JoinPlayer(1, 0, 0)
	rm.SetPlayerName(guest, "José")
	rm.SetPlayerIdentity(member, "google:jose", "José")
	snap, ok := rm.Snapshot(guest)
	if !ok {
		t.Fatal("no snapshot")
	}
	data, err := json.Marshal(snap)
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Players []struct {
			ID   PlayerID `json:"id"`
			Name string   `json:"name"`
		} `json:"players"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		t.Fatal(err)
	}
	seen := 0
	for _, p := range wire.Players {
		if p.ID != guest && p.ID != member {
			continue
		}
		seen++
		if p.Name != "Jos\u0082" || !bytes.Equal(m375Glyphs(p.Name), []byte("Jos\x82")) {
			t.Errorf("roster name for %d on the wire = %q, want Jos + U+0082 (drawn as CP437 é)", p.ID, p.Name)
		}
	}
	if seen != 2 {
		t.Fatalf("roster = %+v, want both players", wire.Players)
	}
}

func TestM375ChatKeepsTheAccentLiveAndAfterRestart(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "chat.jsonl")
	db, err := NewFileChatDatabase(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	server := NewWebSocketServer(testEmptyWorld(t), 1)
	server.ChatDB = db
	httpServer := httptest.NewServer(server)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	wsURL := "ws" + strings.TrimPrefix(httpServer.URL, "http")
	conn, _ := dialJoinWithCookie(t, ctx, wsURL, JoinMessage{Type: MessageTypeJoin, Name: "José", Board: 1}, nil)
	if err := wsjson.Write(ctx, conn, map[string]string{"type": "chat", "text": "¡Olé, señor!"}); err != nil {
		t.Fatal(err)
	}
	live := readChatMessage(t, ctx, conn)
	if !bytes.Equal(m375Glyphs(live.From), []byte("Jos\x82")) {
		t.Errorf("live chat From = %q, want the CP437 é", live.From)
	}
	if !bytes.Equal(m375Glyphs(live.Text), []byte("\xadOl\x82, se\xa4or!")) {
		t.Errorf("live chat Text = %q, want CP437 ¡ é ñ", live.Text)
	}
	conn.Close(websocket.StatusNormalClosure, "")
	httpServer.Close()
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := NewFileChatDatabase(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	recs, err := reopened.GetRecentMessages(10)
	if err != nil || len(recs) != 1 {
		t.Fatalf("history = %+v, %v; want one line", recs, err)
	}
	if recs[0].From != "Jos\x82" || recs[0].Text != "\xadOl\x82, se\xa4or!" {
		t.Fatalf("history after restart = %q / %q, want the CP437 bytes back", recs[0].From, recs[0].Text)
	}

	restarted := NewWebSocketServer(testEmptyWorld(t), 1)
	restarted.ChatDB = reopened
	httpServer2 := httptest.NewServer(restarted)
	defer httpServer2.Close()
	conn2, _ := dialJoinWithCookie(t, ctx, "ws"+strings.TrimPrefix(httpServer2.URL, "http"),
		JoinMessage{Type: MessageTypeJoin, Name: "Reader", Board: 1}, nil)
	defer conn2.Close(websocket.StatusNormalClosure, "")
	replayed := readChatMessage(t, ctx, conn2)
	if !bytes.Equal(m375Glyphs(replayed.From), []byte("Jos\x82")) || !bytes.Equal(m375Glyphs(replayed.Text), []byte("\xadOl\x82, se\xa4or!")) {
		t.Errorf("replayed history = %q / %q, want the CP437 accents", replayed.From, replayed.Text)
	}
}

func TestM375HighScoreLineKeepsTheAccent(t *testing.T) {
	rm := NewRoomManager(testEmptyWorld(t))
	playerID := rm.JoinPlayer(1, 0, 0)
	state, _ := rm.PlayerState(playerID)
	state.Score = 100
	rm.SubmitQuitReply(playerID, true)
	rm.StepDiffs(nil)
	if quits := rm.DrainQuits(); len(quits) != 1 || quits[0].ListPos == 0 {
		t.Fatalf("score did not qualify: %+v", quits)
	}
	if !rm.RecordHighScore(playerID, "José") {
		t.Fatal("RecordHighScore refused")
	}
	if got := rm.HighScores()[0].Name; got != "Jos\x82" {
		t.Fatalf("stored name = %q, want Jos + 0x82", got)
	}
	data, err := json.Marshal(CP437Lines(rm.HighScoreLines(0, 0)))
	if err != nil {
		t.Fatal(err)
	}
	var wire []string
	if err := json.Unmarshal(data, &wire); err != nil {
		t.Fatal(err)
	}
	if got := m375Glyphs(wire[2]); !bytes.Equal(got, []byte("  100  Jos\x82")) {
		t.Fatalf("high-score line draws %q, want \"  100  Jos\" + CP437 é", got)
	}
}

// A recorded name is CP437 and replays as the same bytes. The name here is
// chosen so that re-admitting it would change it: ╞ (0xC6) then é (0x82) is
// also the valid UTF-8 for U+0182, which CP437 does not have. A replay that
// admitted the recorded name again would drop both glyphs.
func TestM375RecordedNameReplaysAsTheSameBytes(t *testing.T) {
	world := townWorld(t)
	var buf bytes.Buffer
	rm, rec := recordedRoomManager(t, "TOWN", world, &buf)
	ada := rm.JoinPlayer(1, 0, 0)
	rm.SetPlayerName(ada, "╞é José")
	const want = "\xc6\x82 Jos\x82"
	if _, name, _ := rm.PlayerIdentity(ada); name != want {
		t.Fatalf("admitted name = %q, want %q", name, want)
	}
	rm.Snapshot(ada)
	rm.StepDiffs(nil)
	rec.Close()

	replayed, err := ReplaySession(bytes.NewReader(buf.Bytes()), nil)
	if err != nil {
		t.Fatalf("ReplaySession: %v", err)
	}
	if _, name, _ := replayed.PlayerIdentity(ada); name != want {
		t.Fatalf("replayed name = %q, want %q", name, want)
	}
}

// A traveller's name was admitted in the world they left; the world they
// arrive in must not admit it again (the same ╞é trap).
func TestM375TransitDoesNotReadmitTheName(t *testing.T) {
	rm := NewRoomManager(townWorld(t))
	p := rm.JoinPlayer(1, 0, 0)
	rm.setAdmittedPlayerName(p, "\xc6\x82")
	if _, name, _ := rm.PlayerIdentity(p); name != "\xc6\x82" {
		t.Fatalf("setAdmittedPlayerName stored %q", name)
	}
	rm.setAdmittedPlayerIdentity(p, "google:x", "\xc6\x82")
	if _, name, _ := rm.PlayerIdentity(p); name != "\xc6\x82" {
		t.Fatalf("setAdmittedPlayerIdentity stored %q", name)
	}
}
