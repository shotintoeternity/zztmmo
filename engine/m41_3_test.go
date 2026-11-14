package zztgo

// M41.3: the browser family reaches no live Museum. NewMuseumService takes its
// base URLs from the environment when set, and TestMain (m16_19_test.go) sets
// them to a local fake before any test runs.

import (
	"context"
	"net/url"
	"testing"
)

// The variables win when set, and a blank one is the same as unset: the live
// site is what production gets by leaving them alone.
func TestM413MuseumBaseURLsComeFromTheEnvironment(t *testing.T) {
	t.Setenv(museumAPIBaseEnv, "http://museum.example/api/v1")
	t.Setenv(museumFilesBaseEnv, "http://museum.example/zgames")
	m := NewMuseumService(nil)
	if m.APIBaseURL != "http://museum.example/api/v1" || m.FilesBaseURL != "http://museum.example/zgames" {
		t.Fatalf("set variables: got API %q files %q", m.APIBaseURL, m.FilesBaseURL)
	}

	t.Setenv(museumAPIBaseEnv, "")
	t.Setenv(museumFilesBaseEnv, "  ")
	m = NewMuseumService(nil)
	if m.APIBaseURL != defaultMuseumAPIBase || m.FilesBaseURL != defaultMuseumFilesBase {
		t.Fatalf("blank variables: got API %q files %q, want the live defaults", m.APIBaseURL, m.FilesBaseURL)
	}
	if defaultMuseumAPIBase != "https://museumofzzt.com/api/v1" || defaultMuseumFilesBase != "https://museumofzzt.com/zgames" {
		t.Fatalf("the production defaults moved: API %q files %q", defaultMuseumAPIBase, defaultMuseumFilesBase)
	}
}

// Under `go test` every Museum this package builds is TestMain's loopback fake,
// and a search there succeeds with nothing in it. This is the claim the browser
// family rests on: a picker search never leaves the machine.
func TestM413TestsSearchALocalMuseum(t *testing.T) {
	m := NewMuseumService(nil)
	for name, base := range map[string]string{"API": m.APIBaseURL, "files": m.FilesBaseURL} {
		u, err := url.Parse(base)
		if err != nil {
			t.Fatalf("%s base %q: %v", name, base, err)
		}
		if u.Hostname() != "127.0.0.1" {
			t.Fatalf("%s base is %q; under go test it must be TestMain's loopback fake", name, base)
		}
	}
	got, err := m.Search(context.Background(), "town")
	if err != nil {
		t.Fatalf("search the fake Museum: %v", err)
	}
	if len(got.Results) != 0 {
		t.Fatalf("the fake Museum answers every search with nothing, got %+v", got.Results)
	}
}
