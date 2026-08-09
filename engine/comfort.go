package zztgo

import (
	"errors"
	"regexp"
)

const (
	ComfortKeyPresetVanilla   = "vanilla"
	ComfortKeyPresetOneHanded = "one-handed"
	ComfortKeyPresetCustom    = "custom"

	ComfortPaletteVanilla          = "vanilla"
	ComfortPaletteHighContrast     = "high-contrast"
	ComfortPaletteColorblindAssist = "colorblind-assist"
)

var (
	ErrInvalidComfort = errors.New("zztgo: invalid comfort preferences")
	keyCodePattern    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{1,31}$`)
)

type ComfortPreferences struct {
	KeyPreset      string              `json:"keyPreset,omitempty"`
	KeyBindings    map[string][]string `json:"keyBindings,omitempty"`
	ReduceFlashing bool                `json:"reduceFlashing,omitempty"`
	Palette        string              `json:"palette,omitempty"`
}

var comfortActions = map[string]bool{
	"up": true, "down": true, "left": true, "right": true, "shoot": true, "shift": true,
	"enter": true, "escape": true, "torch": true, "pause": true, "sound": true,
	"save": true, "quit": true, "help": true,
}

var comfortMovementActions = map[string]bool{
	"up": true, "down": true, "left": true, "right": true, "shoot": true, "shift": true,
}

func SanitizeComfortPreferences(in ComfortPreferences) (ComfortPreferences, error) {
	out := ComfortPreferences{
		KeyPreset:      in.KeyPreset,
		ReduceFlashing: in.ReduceFlashing,
		Palette:        in.Palette,
	}
	if out.KeyPreset == "" && out.Palette == "" && !out.ReduceFlashing && len(in.KeyBindings) == 0 {
		return out, nil
	}
	if out.KeyPreset == "" {
		out.KeyPreset = ComfortKeyPresetVanilla
	}
	switch out.KeyPreset {
	case ComfortKeyPresetVanilla, ComfortKeyPresetOneHanded, ComfortKeyPresetCustom:
	default:
		return ComfortPreferences{}, ErrInvalidComfort
	}
	if out.Palette == "" {
		out.Palette = ComfortPaletteVanilla
	}
	switch out.Palette {
	case ComfortPaletteVanilla, ComfortPaletteHighContrast, ComfortPaletteColorblindAssist:
	default:
		return ComfortPreferences{}, ErrInvalidComfort
	}
	if len(in.KeyBindings) == 0 {
		return out, nil
	}
	if len(in.KeyBindings) > len(comfortActions) {
		return ComfortPreferences{}, ErrInvalidComfort
	}
	seenCodeClass := make(map[string]bool)
	out.KeyBindings = make(map[string][]string, len(in.KeyBindings))
	for action, codes := range in.KeyBindings {
		if !comfortActions[action] || len(codes) == 0 || len(codes) > 4 {
			return ComfortPreferences{}, ErrInvalidComfort
		}
		isMovement := comfortMovementActions[action]
		for _, code := range codes {
			if !keyCodePattern.MatchString(code) {
				return ComfortPreferences{}, ErrInvalidComfort
			}
			if prior, ok := seenCodeClass[code]; ok && prior != isMovement {
				return ComfortPreferences{}, ErrInvalidComfort
			}
			seenCodeClass[code] = isMovement
			out.KeyBindings[action] = append(out.KeyBindings[action], code)
		}
	}
	return out, nil
}
