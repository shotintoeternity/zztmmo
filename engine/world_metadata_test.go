package zztgo

import "testing"

func TestWorldListEntriesUsesMuseumMetadata(t *testing.T) {
	entries := WorldListEntries([]string{"TEEN", "TOWN"}, map[string]int{"TEEN": 2})
	if len(entries) != 2 {
		t.Fatalf("len(entries)=%d, want 2", len(entries))
	}
	if entries[0].World != "TOWN" {
		t.Fatalf("first world=%q, want TOWN lobby first", entries[0].World)
	}
	teen := entries[1]
	if teen.World != "TEEN" || teen.ID != "teen" || teen.Title != "Teen Priest" {
		t.Fatalf("TEEN metadata=%+v, want Museum id/title", teen)
	}
	if teen.Author != "Draco" {
		t.Fatalf("TEEN author=%q, want Draco", teen.Author)
	}
	if teen.Created != "1998" {
		t.Fatalf("TEEN created=%q, want 1998", teen.Created)
	}
	if teen.Players != 2 {
		t.Fatalf("TEEN players=%d, want 2", teen.Players)
	}
}

func TestWorldListEntriesFallsBackToFilename(t *testing.T) {
	entries := WorldListEntries([]string{"EDITED"}, nil)
	if len(entries) != 1 {
		t.Fatalf("len(entries)=%d, want 1", len(entries))
	}
	entry := entries[0]
	if entry.World != "EDITED" || entry.ID != "EDITED" || entry.Title != "EDITED" {
		t.Fatalf("fallback metadata=%+v, want filename fields", entry)
	}
	if entry.Author != "Unknown" {
		t.Fatalf("fallback author=%q, want Unknown", entry.Author)
	}
}
