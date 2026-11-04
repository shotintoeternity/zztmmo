package main

import (
	"archive/zip"
	"bytes"
	"hash/crc32"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// m3627CorruptZip is a zip holding GOOD.ZZT intact and BAD.ZZT stored with a
// CRC that does not match its bytes, so extracting BAD.ZZT writes all of it and
// then fails the checksum: the shape of a truncated or corrupt download.
func m3627CorruptZip(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	good, err := zw.Create("GOOD.ZZT")
	if err != nil {
		t.Fatal(err)
	}
	good.Write([]byte("good world bytes"))
	body := []byte("bad world bytes")
	bad, err := zw.CreateRaw(&zip.FileHeader{
		Name: "BAD.ZZT", Method: zip.Store,
		CRC32:            crc32.ChecksumIEEE(body) ^ 1,
		CompressedSize64: uint64(len(body)), UncompressedSize64: uint64(len(body)),
	})
	if err != nil {
		t.Fatal(err)
	}
	bad.Write(body)
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// M36.27b: a failed extraction is an error, leaves nothing behind, and so is
// fetched again next run rather than SKIPped forever.
func TestFailedExtractionIsAnErrorAndLeavesNoFile(t *testing.T) {
	zipBytes := m3627CorruptZip(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(zipBytes)
	}))
	defer srv.Close()
	previous := baseURL
	baseURL = srv.URL
	t.Cleanup(func() { baseURL = previous })

	dir := t.TempDir()
	entry := ManifestEntry{ID: "corrupt", Letter: "c", Zip: "corrupt.zip"}
	written, _, err := fetchEntry(srv.Client(), entry, dir, false, false)
	if err == nil {
		t.Fatal("fetchEntry reported success although BAD.ZZT failed its checksum")
	}
	if written != 1 {
		t.Errorf("written = %d, want 1 (GOOD.ZZT)", written)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "BAD.ZZT")); !os.IsNotExist(statErr) {
		t.Fatalf("a failed extraction left BAD.ZZT on disk (stat: %v); every later run would SKIP it", statErr)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Fatalf("output directory holds %d entries, want only GOOD.ZZT (no temp file)", len(entries))
	}
}
