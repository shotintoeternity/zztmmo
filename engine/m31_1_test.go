package zztgo

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestM311ComfortPreferencesRoundTripValidateAndPreserveFields(t *testing.T) {
	db := NewMemChatDatabase()
	input := AccountPreferences{
		Color:                      "#112233",
		BlockedAccounts:            []string{"google:blocked"},
		Hints:                      AccountHintPreferences{Players: true, Death: true, Chat: true},
		Profile:                    AccountProfilePreferences{Handle: "ada", DisplayName: "Ada", About: []string{"Builder"}},
		FollowedAccounts:           []string{"google:bo"},
		ShareLocationWithFollowers: true,
		FavoriteWorlds:             []string{"ALPHA"},
		Comfort: ComfortPreferences{
			KeyPreset:      ComfortKeyPresetCustom,
			KeyBindings:    map[string][]string{"up": []string{"KeyI"}, "torch": []string{"KeyO"}},
			ReduceFlashing: true,
			Palette:        ComfortPaletteColorblindAssist,
		},
	}
	if err := db.PutAccountPreferences("google:ada", input); err != nil {
		t.Fatalf("PutAccountPreferences: %v", err)
	}
	got, ok, err := db.GetAccountPreferences("google:ada")
	if err != nil || !ok {
		t.Fatalf("GetAccountPreferences = ok %v err %v", ok, err)
	}
	if got.Color != input.Color || len(got.BlockedAccounts) != 1 || !got.Hints.Chat ||
		got.Profile.Handle != "ada" || len(got.FollowedAccounts) != 1 ||
		!got.ShareLocationWithFollowers || len(got.FavoriteWorlds) != 1 {
		t.Fatalf("comfort write did not preserve existing preference fields: %+v", got)
	}
	if got.Comfort.KeyPreset != ComfortKeyPresetCustom || !got.Comfort.ReduceFlashing ||
		got.Comfort.Palette != ComfortPaletteColorblindAssist ||
		len(got.Comfort.KeyBindings["up"]) != 1 || got.Comfort.KeyBindings["up"][0] != "KeyI" {
		t.Fatalf("comfort did not round-trip: %+v", got.Comfort)
	}
}

func TestM311ComfortPreferencesRejectMalformedPaletteAndConflicts(t *testing.T) {
	for _, prefs := range []ComfortPreferences{
		{Palette: "sepia"},
		{KeyPreset: "dvorak-ish"},
		{KeyBindings: map[string][]string{"dance": []string{"KeyD"}}},
		{KeyBindings: map[string][]string{"up": []string{"KeyT"}, "torch": []string{"KeyT"}}},
		{KeyBindings: map[string][]string{"up": []string{"bad code"}}},
	} {
		if _, err := SanitizeComfortPreferences(prefs); err == nil {
			t.Fatalf("SanitizeComfortPreferences(%+v) succeeded, want error", prefs)
		}
	}
}

func TestM311PreferencesEndpointStoresComfortAndPreservesDocument(t *testing.T) {
	db := NewMemChatDatabase()
	secret := []byte("test-cookie-secret")
	account := AuthenticatedAccount{ID: "google:ada", Email: "ada@example.test", Name: "Ada"}
	if err := db.PutAccountPreferences(account.ID, AccountPreferences{
		Color:                      "#445566",
		BlockedAccounts:            []string{"google:blocked"},
		Hints:                      AccountHintPreferences{Players: true},
		Profile:                    AccountProfilePreferences{Handle: "ada", DisplayName: "Ada"},
		FollowedAccounts:           []string{"google:bo"},
		ShareLocationWithFollowers: true,
		FavoriteWorlds:             []string{"ALPHA"},
	}); err != nil {
		t.Fatalf("PutAccountPreferences: %v", err)
	}

	server, _ := m193Server(t, "ALPHA", db, secret)
	api := &WebAPI{RoomManager: server.RoomManager, Server: server, Auth: server.Auth}
	httpServer := httptest.NewServer(api.Handler())
	defer httpServer.Close()
	cookie := signedAuthCookie(t, server.Auth, account)

	body := `{"comfort":{"keyPreset":"custom","keyBindings":{"up":["KeyI"],"torch":["KeyO"]},"reduceFlashing":true,"palette":"high-contrast"}}`
	code, response := m193PutPreferences(t, httpServer.URL, cookie, body)
	if code != http.StatusOK {
		t.Fatalf("comfort PUT status = %d, want 200", code)
	}
	if response.Color != "#445566" || !response.Hints.Players || response.Profile.Handle != "ada" ||
		!response.ShareLocationWithFollowers || len(response.FavoriteWorlds) != 1 {
		t.Fatalf("comfort PUT did not preserve existing response fields: %+v", response)
	}
	if response.Comfort.KeyPreset != ComfortKeyPresetCustom || response.Comfort.Palette != ComfortPaletteHighContrast ||
		!response.Comfort.ReduceFlashing || response.Comfort.KeyBindings["torch"][0] != "KeyO" {
		t.Fatalf("comfort response = %+v", response.Comfort)
	}
	if code, _ := m193PutPreferences(t, httpServer.URL, nil, body); code != http.StatusUnauthorized {
		t.Fatalf("guest comfort PUT status = %d, want 401", code)
	}
	if code, _ := m193PutPreferences(t, httpServer.URL, cookie, `{"comfort":{"palette":"brownout"}}`); code != http.StatusBadRequest {
		t.Fatalf("bad palette PUT status = %d, want 400", code)
	}
	if code, _ := m193PutPreferences(t, httpServer.URL, cookie, `{"comfort":{"keyBindings":{"up":["KeyT"],"torch":["KeyT"]}}}`); code != http.StatusBadRequest {
		t.Fatalf("conflicting keymap PUT status = %d, want 400", code)
	}
}
