package zztgo

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	defaultMuseumAPIBase   = "https://museumofzzt.com/api/v1"
	defaultMuseumFilesBase = "https://museumofzzt.com/zgames"
	museumUserAgent        = "zztmmo-museum/1.0 (github.com/shotintoeternity/zztmmo)"
	museumRequestDelay     = 300 * time.Millisecond
	museumMaxZipBytes      = 64 << 20
)

type MuseumService struct {
	Server       *WebSocketServer
	Client       *http.Client
	APIBaseURL   string
	FilesBaseURL string
	CacheDir     string

	mu          sync.Mutex
	lastRequest time.Time
}

type MuseumSearchResult struct {
	ID             string   `json:"id"`
	Letter         string   `json:"letter"`
	Filename       string   `json:"filename"`
	Title          string   `json:"title"`
	Author         []string `json:"author"`
	ReleaseDate    string   `json:"releaseDate"`
	Genres         []string `json:"genres,omitempty"`
	Rating         *float64 `json:"rating,omitempty"`
	PlayableBoards int      `json:"playableBoards,omitempty"`
	TotalBoards    int      `json:"totalBoards,omitempty"`
	ArchiveName    string   `json:"archiveName,omitempty"`
	Explicit       int      `json:"explicit,omitempty"`
}

type MuseumSearchResponse struct {
	Results []MuseumSearchResult `json:"results"`
	Count   int                  `json:"count"`
}

type MuseumPlayRequest struct {
	Letter   string `json:"letter"`
	Filename string `json:"filename"`
	ZZTFile  string `json:"zztFile,omitempty"`
}

type MuseumPlayChoice struct {
	Name string `json:"name"`
}

type MuseumPlayResponse struct {
	World   string             `json:"world,omitempty"`
	Choices []MuseumPlayChoice `json:"choices,omitempty"`
}

type museumAPIResponse struct {
	Status string `json:"status"`
	Count  int    `json:"count"`
	Data   struct {
		Results []museumAPIFile `json:"results"`
	} `json:"data"`
}

type museumAPIFile struct {
	Letter         string   `json:"letter"`
	Filename       string   `json:"filename"`
	Title          string   `json:"title"`
	Author         []string `json:"author"`
	ReleaseDate    string   `json:"release_date"`
	Genres         []string `json:"genres"`
	Rating         *float64 `json:"rating"`
	PlayableBoards int      `json:"playable_boards"`
	TotalBoards    int      `json:"total_boards"`
	ArchiveName    string   `json:"archive_name"`
	Explicit       int      `json:"explicit"`
}

func NewMuseumService(server *WebSocketServer) *MuseumService {
	cacheDir := ""
	if server != nil {
		cacheDir = filepath.Join(server.worldsDir(), ".museum-cache")
	}
	return &MuseumService{
		Server:       server,
		Client:       &http.Client{Timeout: 60 * time.Second},
		APIBaseURL:   defaultMuseumAPIBase,
		FilesBaseURL: defaultMuseumFilesBase,
		CacheDir:     cacheDir,
	}
}

func (m *MuseumService) Search(ctx context.Context, query string) (MuseumSearchResponse, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return MuseumSearchResponse{}, nil
	}

	fields := []string{"title", "author", "filename", "genre"}
	if _, err := strconv.Atoi(query); err == nil && len(query) == 4 {
		fields = append(fields, "year")
	}

	resultsByKey := make(map[string]MuseumSearchResult)
	for _, field := range fields {
		results, err := m.searchField(ctx, field, query)
		if err != nil {
			return MuseumSearchResponse{}, err
		}
		for _, result := range results {
			key := strings.ToLower(result.Letter + "/" + result.Filename)
			if _, exists := resultsByKey[key]; !exists {
				resultsByKey[key] = result
			}
		}
	}

	results := make([]MuseumSearchResult, 0, len(resultsByKey))
	for _, result := range resultsByKey {
		results = append(results, result)
	}
	sort.SliceStable(results, func(i, j int) bool {
		return strings.ToLower(results[i].Title) < strings.ToLower(results[j].Title)
	})
	return MuseumSearchResponse{Results: results, Count: len(results)}, nil
}

func (m *MuseumService) searchField(ctx context.Context, field, query string) ([]MuseumSearchResult, error) {
	endpoint := strings.TrimRight(m.APIBaseURL, "/") + "/search/files"
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, err
	}
	q := u.Query()
	q.Set(field, query)
	q.Set("sort", "title")
	u.RawQuery = q.Encode()

	var api museumAPIResponse
	if err := m.getJSON(ctx, u.String(), &api); err != nil {
		return nil, err
	}
	out := make([]MuseumSearchResult, 0, len(api.Data.Results))
	for _, file := range api.Data.Results {
		out = append(out, museumSearchResult(file))
	}
	return out, nil
}

func museumSearchResult(file museumAPIFile) MuseumSearchResult {
	id := strings.TrimSpace(file.ArchiveName)
	if id == "" {
		id = strings.TrimSuffix(file.Filename, filepath.Ext(file.Filename))
	}
	return MuseumSearchResult{
		ID:             id,
		Letter:         file.Letter,
		Filename:       file.Filename,
		Title:          file.Title,
		Author:         file.Author,
		ReleaseDate:    file.ReleaseDate,
		Genres:         file.Genres,
		Rating:         file.Rating,
		PlayableBoards: file.PlayableBoards,
		TotalBoards:    file.TotalBoards,
		ArchiveName:    file.ArchiveName,
		Explicit:       file.Explicit,
	}
}

func (m *MuseumService) Play(ctx context.Context, req MuseumPlayRequest) (MuseumPlayResponse, error) {
	if m.Server == nil {
		return MuseumPlayResponse{}, fmt.Errorf("museum play requires a WebSocketServer")
	}
	if err := validateMuseumDownloadName(req.Letter, req.Filename); err != nil {
		return MuseumPlayResponse{}, err
	}

	zipData, err := m.downloadZip(ctx, req.Letter, req.Filename)
	if err != nil {
		return MuseumPlayResponse{}, err
	}
	worlds, err := zztFilesFromZip(zipData)
	if err != nil {
		return MuseumPlayResponse{}, err
	}
	if len(worlds) == 0 {
		return MuseumPlayResponse{}, fmt.Errorf("archive contains no .ZZT worlds")
	}
	if req.ZZTFile == "" && len(worlds) > 1 {
		choices := make([]MuseumPlayChoice, 0, len(worlds))
		for _, world := range worlds {
			choices = append(choices, MuseumPlayChoice{Name: world.Name})
		}
		return MuseumPlayResponse{Choices: choices}, nil
	}

	selected := worlds[0]
	if req.ZZTFile != "" {
		var ok bool
		for _, world := range worlds {
			if strings.EqualFold(world.Name, req.ZZTFile) {
				selected = world
				ok = true
				break
			}
		}
		if !ok {
			return MuseumPlayResponse{}, fmt.Errorf("archive does not contain %q", req.ZZTFile)
		}
	}

	if err := validateGeneratedZWD(selected.Data); err != nil {
		return MuseumPlayResponse{}, fmt.Errorf("world failed validation: %w", err)
	}
	world, err := LoadWorldBytes(selected.Data)
	if err != nil {
		return MuseumPlayResponse{}, err
	}

	name := museumHostedWorldName(req.Filename, selected.Name, selected.Data)
	dir := m.Server.worldsDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return MuseumPlayResponse{}, err
	}
	if err := os.WriteFile(filepath.Join(dir, name+".ZZT"), selected.Data, 0o644); err != nil {
		return MuseumPlayResponse{}, err
	}
	if err := m.Server.HostGeneratedWorld(name, world); err != nil {
		return MuseumPlayResponse{}, err
	}
	return MuseumPlayResponse{World: name}, nil
}

type museumZipWorld struct {
	Name string
	Data []byte
}

func zztFilesFromZip(data []byte) ([]museumZipWorld, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}
	var worlds []museumZipWorld
	for _, zf := range zr.File {
		if !strings.EqualFold(path.Ext(strings.ReplaceAll(zf.Name, "\\", "/")), ".zzt") {
			continue
		}
		base, err := museumZipEntryBase(zf.Name)
		if err != nil {
			return nil, err
		}
		rc, err := zf.Open()
		if err != nil {
			return nil, err
		}
		worldData, readErr := io.ReadAll(io.LimitReader(rc, museumMaxZipBytes+1))
		closeErr := rc.Close()
		if readErr != nil {
			return nil, readErr
		}
		if closeErr != nil {
			return nil, closeErr
		}
		if len(worldData) > museumMaxZipBytes {
			return nil, fmt.Errorf("world %q is too large", base)
		}
		worlds = append(worlds, museumZipWorld{Name: strings.ToUpper(base), Data: worldData})
	}
	sort.SliceStable(worlds, func(i, j int) bool { return worlds[i].Name < worlds[j].Name })
	return worlds, nil
}

func museumZipEntryBase(name string) (string, error) {
	if name == "" || strings.Contains(name, "\x00") || strings.Contains(name, "\\") {
		return "", fmt.Errorf("unsafe zzt filename %q", name)
	}
	cleaned := path.Clean(name)
	if path.IsAbs(cleaned) || cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", fmt.Errorf("unsafe zzt filename %q", name)
	}
	base := path.Base(cleaned)
	if base == "" || base == "." || base == ".." {
		return "", fmt.Errorf("unsafe zzt filename %q", name)
	}
	return strings.ToUpper(base), nil
}

func (m *MuseumService) downloadZip(ctx context.Context, letter, filename string) ([]byte, error) {
	if m.CacheDir != "" {
		cachePath := filepath.Join(m.CacheDir, strings.ToLower(letter), filename)
		if data, err := os.ReadFile(cachePath); err == nil {
			return data, nil
		}
	}

	u := strings.TrimRight(m.FilesBaseURL, "/") + "/" + letter + "/" + path.Base(url.PathEscape(filename))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", museumUserAgent)

	if err := m.waitTurn(ctx); err != nil {
		return nil, err
	}
	resp, err := m.httpClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: HTTP %d", u, resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, museumMaxZipBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > museumMaxZipBytes {
		return nil, fmt.Errorf("museum archive is too large")
	}

	if m.CacheDir != "" {
		cachePath := filepath.Join(m.CacheDir, strings.ToLower(letter), filename)
		if err := os.MkdirAll(filepath.Dir(cachePath), 0o755); err == nil {
			_ = os.WriteFile(cachePath, data, 0o644)
		}
	}
	return data, nil
}

func (m *MuseumService) getJSON(ctx context.Context, endpoint string, v interface{}) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", museumUserAgent)
	if err := m.waitTurn(ctx); err != nil {
		return err
	}
	resp, err := m.httpClient().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: HTTP %d", endpoint, resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(v)
}

func (m *MuseumService) waitTurn(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	wait := museumRequestDelay - time.Since(m.lastRequest)
	if wait > 0 {
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	m.lastRequest = time.Now()
	return nil
}

func (m *MuseumService) httpClient() *http.Client {
	if m.Client != nil {
		return m.Client
	}
	return http.DefaultClient
}

func validateMuseumDownloadName(letter, filename string) error {
	if len(letter) != 1 {
		return fmt.Errorf("invalid museum letter")
	}
	c := letter[0]
	if !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9')) {
		return fmt.Errorf("invalid museum letter")
	}
	if filename == "" || filepath.Base(filename) != filename || strings.ContainsAny(filename, "/\\") {
		return fmt.Errorf("invalid museum filename")
	}
	if !strings.EqualFold(filepath.Ext(filename), ".zip") {
		return fmt.Errorf("museum filename must be a .zip")
	}
	return nil
}

func museumHostedWorldName(zipName, zztName string, data []byte) string {
	candidates := []string{
		strings.TrimSuffix(zztName, filepath.Ext(zztName)),
		strings.TrimSuffix(zipName, filepath.Ext(zipName)),
	}
	for _, candidate := range candidates {
		if safe, ok := compactSaveName(candidate); ok {
			return safe
		}
	}
	sum := sha1.Sum(data)
	return "MZ" + strings.ToUpper(hex.EncodeToString(sum[:]))[:6]
}

func compactSaveName(name string) (string, bool) {
	var out []byte
	for i := 0; i < len(name); i++ {
		c := UpCase(name[i])
		if (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' {
			out = append(out, c)
		}
		if len(out) == SaveNameMaxLength {
			break
		}
	}
	if len(out) == 0 {
		return "", false
	}
	safe, err := SanitizeSaveName(string(out))
	return safe, err == nil
}
