package zztgo

import (
	"errors"
	"strings"
	"unicode/utf8"
)

const (
	ProfileHandleMin       = 3
	ProfileHandleMax       = 16
	ProfileDisplayNameMax  = 24
	ProfileAboutLineMax    = 40
	ProfileAboutMaxLines   = 3
	ProfileWindowLineWidth = 40
)

var (
	ErrInvalidProfileHandle = errors.New("zztgo: invalid profile handle")
	ErrProfileHandleTaken   = errors.New("zztgo: profile handle already claimed")
	ErrInvalidProfileText   = errors.New("zztgo: invalid profile text")
)

type AccountProfilePreferences struct {
	Handle      string   `json:"handle,omitempty"`
	DisplayName string   `json:"displayName,omitempty"`
	About       []string `json:"about,omitempty"`
}

func NormalizeProfileHandle(raw string) (string, error) {
	handle := strings.TrimSpace(raw)
	handle = strings.TrimPrefix(handle, "@")
	handle = strings.ToLower(handle)
	if handle == "" {
		return "", nil
	}
	if len(handle) < ProfileHandleMin || len(handle) > ProfileHandleMax {
		return "", ErrInvalidProfileHandle
	}
	for i := 0; i < len(handle); i++ {
		c := handle[i]
		switch {
		case c >= 'a' && c <= 'z':
		case c >= '0' && c <= '9':
		case c == '_':
		default:
			return "", ErrInvalidProfileHandle
		}
	}
	return handle, nil
}

func SanitizeAccountProfile(profile AccountProfilePreferences) (AccountProfilePreferences, error) {
	handle, err := NormalizeProfileHandle(profile.Handle)
	if err != nil {
		return AccountProfilePreferences{}, err
	}
	display, err := sanitizeProfileLine(profile.DisplayName, ProfileDisplayNameMax, false)
	if err != nil {
		return AccountProfilePreferences{}, err
	}
	if len(profile.About) > ProfileAboutMaxLines {
		return AccountProfilePreferences{}, ErrInvalidProfileText
	}
	about := make([]string, 0, len(profile.About))
	for _, line := range profile.About {
		clean, err := sanitizeProfileLine(line, ProfileAboutLineMax, true)
		if err != nil {
			return AccountProfilePreferences{}, err
		}
		if clean != "" {
			about = append(about, clean)
		}
	}
	return AccountProfilePreferences{Handle: handle, DisplayName: display, About: about}, nil
}

func sanitizeProfileLine(raw string, max int, allowEmpty bool) (string, error) {
	line := strings.TrimSpace(raw)
	if line == "" {
		if allowEmpty {
			return "", nil
		}
		return "", nil
	}
	if utf8.RuneCountInString(line) > max {
		runes := []rune(line)
		line = string(runes[:max])
	}
	for _, r := range line {
		if r < 32 || r == 127 {
			return "", ErrInvalidProfileText
		}
		// The browser text window is CP437-shaped, but the transport is UTF-8.
		// Keep v1 profile text to printable ASCII until a real CP437 encoder is
		// shared with the editor/import path.
		if r > 126 {
			return "", ErrInvalidProfileText
		}
	}
	return line, nil
}

func PublicProfileLines(name string, profile AccountProfilePreferences, found bool) []string {
	if !found {
		if strings.TrimSpace(name) == "" {
			name = "That player"
		}
		return []string{"", "  " + fitProfileLine(name, ProfileWindowLineWidth), "  Guest, no profile.", ""}
	}
	display := profile.DisplayName
	if display == "" {
		display = name
	}
	if display == "" {
		display = "Player"
	}
	lines := []string{"", "  " + fitProfileLine(display, ProfileWindowLineWidth)}
	if profile.Handle != "" {
		lines = append(lines, "  @"+profile.Handle)
	}
	if len(profile.About) > 0 {
		lines = append(lines, "")
		for _, line := range profile.About {
			lines = append(lines, "  "+fitProfileLine(line, ProfileWindowLineWidth))
		}
	}
	if len(lines) == 2 {
		lines = append(lines, "  No profile yet.")
	}
	lines = append(lines, "")
	return lines
}

func fitProfileLine(line string, max int) string {
	if utf8.RuneCountInString(line) <= max {
		return line
	}
	runes := []rune(line)
	return string(runes[:max])
}
