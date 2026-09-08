// cmd/zzt-fetch downloads and extracts .ZZT files listed in a manifest.
//
// Usage:
//
//	go run ./cmd/zzt-fetch [-manifest worlds.manifest.json] [-out .] [-dry-run] [-force]
//
// The manifest (default: worlds.manifest.json in the current directory) lists
// Museum of ZZT worlds to fetch. Each entry downloads a zip from
// museumofzzt.com and extracts .ZZT files into the output directory.
//
// Files that already exist are skipped unless -force is given.
// Courtesy constraints: 300 ms between requests, User-Agent identifies the project.
package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	baseURL    = "https://museumofzzt.com/zgames"
	userAgent  = "zztmmo-fetch/1.0 (github.com/shotintoeternity/zztmmo)"
	fetchDelay = 300 * time.Millisecond

	// Retry policy. This is somebody else's archive, run for the community out
	// of goodwill, and the whole catalogue is a lot of requests to make of it in
	// one sitting -- so a refusal is treated as "you are going too fast", not as
	// a failure to report and move past.
	//
	// The backoff doubles from a second and gives up after six tries, which is
	// about a minute of waiting on one file. A 429 or a 503 that names a
	// Retry-After is obeyed instead: the server saying how long it wants beats
	// anything guessed here.
	maxAttempts    = 6
	backoffInitial = 1 * time.Second
	backoffMax     = 32 * time.Second
)

// ManifestEntry describes one Museum of ZZT world to fetch.
type ManifestEntry struct {
	ID       string   `json:"id"`
	Letter   string   `json:"letter"`
	Zip      string   `json:"zip"`
	Title    string   `json:"title"`
	Author   string   `json:"author"`
	Year     int      `json:"year"`
	ZZTFiles []string `json:"zzt_files"` // explicit list; empty = take all .ZZT from zip
}

// Manifest is the top-level structure of worlds.manifest.json.
type Manifest struct {
	Worlds []ManifestEntry `json:"worlds"`
}

func main() {
	manifestPath := flag.String("manifest", "worlds.manifest.json", "path to worlds.manifest.json")
	outDir := flag.String("out", ".", "directory to write .ZZT files into")
	dryRun := flag.Bool("dry-run", false, "print what would be fetched without downloading")
	force := flag.Bool("force", false, "re-fetch even if the file already exists")
	flag.Parse()

	data, err := os.ReadFile(*manifestPath)
	if err != nil {
		fatalf("reading manifest: %v", err)
	}
	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		fatalf("parsing manifest: %v", err)
	}

	if err := os.MkdirAll(*outDir, 0755); err != nil {
		fatalf("creating output dir: %v", err)
	}

	client := &http.Client{Timeout: 60 * time.Second}
	totalWritten := 0
	totalSkipped := 0
	totalErrors := 0

	// Deduplicate by zip filename so tp2 (which appears once with two files) isn't
	// fetched twice if the user somehow listed it twice.
	seen := make(map[string]bool)

	for i, entry := range manifest.Worlds {
		// Display-metadata-only entries carry no zip (they describe worlds that
		// are bundled/built-in, not Museum downloads). Nothing to fetch.
		if entry.Zip == "" {
			continue
		}
		zipKey := strings.ToLower(entry.Zip)
		if seen[zipKey] {
			continue
		}
		seen[zipKey] = true

		if i > 0 {
			time.Sleep(fetchDelay)
		}
		written, skipped, err := fetchEntry(client, entry, *outDir, *dryRun, *force)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ERROR [%s] %v\n", entry.ID, err)
			totalErrors++
			continue
		}
		totalWritten += written
		totalSkipped += skipped
	}

	fmt.Printf("\nDone. %d file(s) written, %d skipped (already present), %d error(s).\n",
		totalWritten, totalSkipped, totalErrors)
	if totalErrors > 0 {
		os.Exit(1)
	}
}

// backoffSleep is the wait between retries. It is a variable so that the test
// can watch what the policy asks for without paying it in wall-clock seconds.
var backoffSleep = time.Sleep

// getWithBackoff is one GET, retried on the answers that mean "later".
//
// Retried: a transport error (the connection died mid-archive), a 429, and any
// 5xx. Not retried: a 404 or any other 4xx, which will say the same thing
// however long we wait -- a zip that is not there is a manifest bug, and
// hammering it would only be rude about it.
//
// A Retry-After header wins over the computed delay, in either of its forms:
// seconds, or an HTTP date. The server is allowed to know better than we do.
func getWithBackoff(client *http.Client, url string) (*http.Response, error) {
	delay := backoffInitial
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		req, err := http.NewRequest("GET", url, nil)
		if err != nil {
			return nil, fmt.Errorf("building request: %w", err)
		}
		req.Header.Set("User-Agent", userAgent)

		resp, err := client.Do(req)
		if err == nil {
			if resp.StatusCode == http.StatusOK {
				return resp, nil
			}
			retryable := resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500
			if !retryable {
				resp.Body.Close()
				return nil, fmt.Errorf("GET %s: HTTP %d", url, resp.StatusCode)
			}
			wait := retryAfter(resp.Header.Get("Retry-After"), delay)
			resp.Body.Close()
			lastErr = fmt.Errorf("GET %s: HTTP %d", url, resp.StatusCode)
			if attempt == maxAttempts {
				break
			}
			fmt.Printf("  retrying in %s (attempt %d/%d): %v\n", wait, attempt, maxAttempts, lastErr)
			backoffSleep(wait)
		} else {
			lastErr = fmt.Errorf("GET %s: %w", url, err)
			if attempt == maxAttempts {
				break
			}
			fmt.Printf("  retrying in %s (attempt %d/%d): %v\n", delay, attempt, maxAttempts, lastErr)
			backoffSleep(delay)
		}
		if delay *= 2; delay > backoffMax {
			delay = backoffMax
		}
	}
	return nil, fmt.Errorf("%w (after %d attempts)", lastErr, maxAttempts)
}

// retryAfter reads the header in both of its forms, falling back to the
// computed delay when it is absent or nonsense. A server asking for longer than
// the cap is still obeyed -- it is their archive.
func retryAfter(header string, fallback time.Duration) time.Duration {
	if header == "" {
		return fallback
	}
	if seconds, err := strconv.Atoi(strings.TrimSpace(header)); err == nil && seconds >= 0 {
		return time.Duration(seconds) * time.Second
	}
	if when, err := http.ParseTime(header); err == nil {
		if d := time.Until(when); d > 0 {
			return d
		}
		return 0
	}
	return fallback
}

// fetchEntry downloads the zip for one manifest entry and extracts its .ZZT files.
// Returns (written, skipped, error).
func fetchEntry(client *http.Client, entry ManifestEntry, outDir string, dryRun, force bool) (written, skipped int, err error) {
	// Build URL: letter/filename with spaces percent-encoded.
	url := fmt.Sprintf("%s/%s/%s", baseURL, entry.Letter, strings.ReplaceAll(entry.Zip, " ", "%20"))

	// If we have an explicit file list and all are present, skip early.
	if !force && len(entry.ZZTFiles) > 0 {
		allPresent := true
		for _, zf := range entry.ZZTFiles {
			if _, statErr := os.Stat(filepath.Join(outDir, strings.ToUpper(zf))); os.IsNotExist(statErr) {
				allPresent = false
				break
			}
		}
		if allPresent {
			fmt.Printf("SKIP [%s] all files already present\n", entry.ID)
			return 0, len(entry.ZZTFiles), nil
		}
	}

	fmt.Printf("FETCH [%s] %s (%d) → %s\n", entry.ID, entry.Title, entry.Year, url)
	if dryRun {
		return 0, 0, nil
	}

	resp, err := getWithBackoff(client, url)
	if err != nil {
		return 0, 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, 0, fmt.Errorf("GET %s: HTTP %d", url, resp.StatusCode)
	}

	zipData, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, 0, fmt.Errorf("reading response: %w", err)
	}

	zr, err := zip.NewReader(bytes.NewReader(zipData), int64(len(zipData)))
	if err != nil {
		return 0, 0, fmt.Errorf("opening zip from %s: %w", url, err)
	}

	// Build a set of explicitly requested filenames (upper-cased).
	wantSet := make(map[string]bool)
	for _, f := range entry.ZZTFiles {
		wantSet[strings.ToUpper(f)] = true
	}

	for _, zf := range zr.File {
		if !strings.EqualFold(filepath.Ext(zf.Name), ".zzt") {
			continue
		}
		// Reject any path traversal embedded in the zip entry name.
		base := filepath.Base(zf.Name)
		if base == "" || base == "." || strings.ContainsAny(base, "/\\") {
			fmt.Fprintf(os.Stderr, "  WARN [%s] skipping suspicious zip entry: %q\n", entry.ID, zf.Name)
			continue
		}
		upper := strings.ToUpper(base)

		if len(wantSet) > 0 && !wantSet[upper] {
			continue
		}

		outPath := filepath.Join(outDir, upper)
		if !force {
			if _, statErr := os.Stat(outPath); statErr == nil {
				fmt.Printf("  SKIP %s (already exists)\n", upper)
				skipped++
				continue
			}
		}

		if extractErr := extractZipFile(zf, outPath); extractErr != nil {
			fmt.Fprintf(os.Stderr, "  WARN [%s] extracting %s: %v\n", entry.ID, upper, extractErr)
			continue
		}
		fmt.Printf("  WROTE %s\n", upper)
		written++
	}

	return written, skipped, nil
}

// extractZipFile writes one zip entry to outPath.
func extractZipFile(zf *zip.File, outPath string) error {
	rc, err := zf.Open()
	if err != nil {
		return err
	}
	defer rc.Close()

	out, err := os.Create(outPath)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, rc)
	return err
}

func fatalf(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "zzt-fetch: "+format+"\n", args...)
	os.Exit(1)
}
