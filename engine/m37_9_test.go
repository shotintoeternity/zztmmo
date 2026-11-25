package zztgo

// M37.9: the server keeps a record of every account that signs in with Google:
// the Google id, the email, the Google name, and the first and last sign-in.

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestM379AccountStoreRecordsSignIns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "accounts.json")
	store := NewAccountStore(path)
	first := time.Date(2026, 11, 25, 9, 0, 0, 0, time.UTC)
	later := first.Add(36 * time.Hour)

	ada := AuthenticatedAccount{ID: "google:ada", Email: "ada@example.test", Name: "Ada Lovelace"}
	if _, err := store.RecordSignIn(ada, first); err != nil {
		t.Fatalf("RecordSignIn: %v", err)
	}
	// Between visits Ada changes her email and name at Google.
	ada.Email, ada.Name = "ada@new.example.test", "Ada King"
	if _, err := store.RecordSignIn(ada, later); err != nil {
		t.Fatalf("RecordSignIn again: %v", err)
	}
	// An empty id is not an account and leaves no record.
	if _, err := store.RecordSignIn(AuthenticatedAccount{Email: "nobody@example.test"}, later); err != nil {
		t.Fatalf("RecordSignIn with no id: %v", err)
	}

	want := AccountRecord{ID: "google:ada", Email: "ada@new.example.test", GoogleName: "Ada King", FirstSignIn: first, LastSignIn: later, SignIns: 2}
	if got, ok := store.Get("google:ada"); !ok || got != want {
		t.Fatalf("record = %+v (found %v), want %+v", got, ok, want)
	}
	if store.Len() != 1 {
		t.Fatalf("store holds %d records, want 1", store.Len())
	}

	// It survives a restart, and only the server's own user can read it.
	reloaded := NewAccountStore(path)
	if got, ok := reloaded.Get("google:ada"); !ok || got != want {
		t.Fatalf("reloaded record = %+v (found %v), want %+v", got, ok, want)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("accounts file mode %o, want 600", perm)
	}

	// A corrupt file is set aside rather than stopping the server.
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if NewAccountStore(path).Len() != 0 {
		t.Fatal("a corrupt accounts file loaded records")
	}
	if _, err := os.Stat(path + ".corrupt"); err != nil {
		t.Fatalf("the corrupt accounts file was not set aside: %v", err)
	}

	// No store (a server without -saves wires none) records nothing and fails nothing.
	var none *AccountStore
	if _, err := none.RecordSignIn(ada, later); err != nil || none.Len() != 0 {
		t.Fatalf("a nil store: err=%v len=%d", err, none.Len())
	}
}

// Through the real callback: a completed sign-in is recorded, a refused one is not.
func TestM379SignInThroughTheCallbackIsRecorded(t *testing.T) {
	svc := m1616NewService(t, true)
	at := time.Date(2026, 11, 25, 12, 30, 0, 0, time.UTC)
	svc.auth.Now = func() time.Time { return at }
	svc.auth.Accounts = NewAccountStore(AccountStorePath(svc.savesDir))

	// Refused first: Google's token does not verify, so nobody signed in.
	svc.idp.signInNext("nobody")
	stateCookie, state := m1616BeginAuth(t, svc)
	m1616ArmProviderCode(t, svc, stateCookie, state)
	req, err := http.NewRequest(http.MethodGet, svc.app.URL+"/api/auth/google/callback?code="+m1616BadCode+"&state="+state, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.AddCookie(stateCookie)
	resp, err := svc.noRedirect().Do(req)
	if err != nil {
		t.Fatalf("callback: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unverifiable callback status=%d, want 401", resp.StatusCode)
	}
	if n := svc.auth.Accounts.Len(); n != 0 {
		t.Fatalf("a refused sign-in left %d account record(s)", n)
	}

	ada := m1616AccountByKey(t, "ada")
	svc.idp.signInNext(ada.Key)
	client := svc.browser()
	resp, err = client.Get(svc.app.URL + "/api/auth/google/start?return=/play")
	if err != nil {
		t.Fatalf("sign-in chain: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("sign-in chain ended with status %d", resp.StatusCode)
	}
	want := AccountRecord{ID: ada.ID, Email: ada.Email, GoogleName: ada.Name, FirstSignIn: at, LastSignIn: at, SignIns: 1}
	if got, ok := NewAccountStore(AccountStorePath(svc.savesDir)).Get(ada.ID); !ok || got != want {
		t.Fatalf("the record on disk = %+v (found %v), want %+v", got, ok, want)
	}
	// The record is the server's: /api/auth/me tells the browser nothing new.
	if me := svc.me(client); !me.Authenticated || me.ID != ada.ID {
		t.Fatalf("/api/auth/me = %+v", me)
	}
}
