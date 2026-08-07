package zztgo

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/gif"
	"io"
	"sync"
	"time"
)

const (
	// PostcardMaxTicks bounds one GIF render to about five and a half seconds at
	// the server tick. The endpoint is intentionally tiny: a share hook, not an
	// offline movie renderer.
	PostcardMaxTicks     = 50
	PostcardDefaultTicks = 30
	postcardDelayCS      = int(ServerTickDuration / (10 * time.Millisecond))
	postcardRateMax      = 5
	postcardRateWindow   = 10 * time.Second
)

type ReplayPostcardOptions struct {
	StartTick int
	Ticks     int
	BoardID   int16
}

type ReplayPostcard struct {
	GIF       []byte
	WorldName string
	FinalTick int
	Frames    int
}

type replayPostcardKey struct {
	ID    string
	Start int
	Ticks int
	Board int16
}

type replayPostcardCache struct {
	mu    sync.Mutex
	items map[replayPostcardKey]ReplayPostcard
}

func (c *replayPostcardCache) get(key replayPostcardKey) (ReplayPostcard, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	postcard, ok := c.items[key]
	return postcard, ok
}

func (c *replayPostcardCache) put(key replayPostcardKey, postcard ReplayPostcard) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.items == nil {
		c.items = make(map[replayPostcardKey]ReplayPostcard)
	}
	c.items[key] = postcard
}

type postcardRateLimiter struct {
	mu       sync.Mutex
	accepted map[string][]time.Time
}

func (l *postcardRateLimiter) allow(client string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	kept := l.accepted[client][:0]
	for _, t := range l.accepted[client] {
		if now.Sub(t) < postcardRateWindow {
			kept = append(kept, t)
		}
	}
	if len(kept) >= postcardRateMax {
		l.accepted[client] = kept
		return false
	}
	if l.accepted == nil {
		l.accepted = make(map[string][]time.Time)
	}
	l.accepted[client] = append(kept, now)
	return true
}

// RenderReplayPostcardGIF replays a bounded tick range and renders the selected
// board to an animated GIF. It deliberately renders protocol cells rather than
// .ZZT bytes, so it shares the read-only viewer's view of the room.
func RenderReplayPostcardGIF(r io.Reader, opt ReplayPostcardOptions) (ReplayPostcard, error) {
	if opt.StartTick < 0 {
		return ReplayPostcard{}, fmt.Errorf("start tick must be >= 0")
	}
	if opt.Ticks <= 0 {
		opt.Ticks = PostcardDefaultTicks
	}
	if opt.Ticks > PostcardMaxTicks {
		return ReplayPostcard{}, fmt.Errorf("tick range %d exceeds cap %d", opt.Ticks, PostcardMaxTicks)
	}
	if opt.BoardID == 0 {
		opt.BoardID = 1
	}

	playback, err := NewReplayPlayback(r)
	if err != nil {
		return ReplayPostcard{}, err
	}
	if opt.BoardID < 0 || opt.BoardID > playback.RoomManager().FrozenWorld().BoardCount {
		return ReplayPostcard{}, fmt.Errorf("board %d out of range", opt.BoardID)
	}

	var frames []*image.Paletted
	var delays []int
	endTick := opt.StartTick + opt.Ticks
	lastTick := -1
	for {
		tick, _, done, err := playback.Step()
		if err != nil {
			return ReplayPostcard{}, err
		}
		if done {
			break
		}
		lastTick = tick
		if tick < opt.StartTick || tick >= endTick {
			continue
		}
		snapshot, _ := playback.RoomManager().SpectatorSnapshot(opt.BoardID)
		img, err := RenderScreenCellsImage(snapshot.Screen, BOARD_WIDTH, BOARD_HEIGHT)
		if err != nil {
			return ReplayPostcard{}, err
		}
		frames = append(frames, palettedEGA(img))
		delays = append(delays, postcardDelayCS)
	}
	if playback.PlayerCountEver() > 1 {
		return ReplayPostcard{}, fmt.Errorf("postcards are beta-limited to single-player recordings")
	}
	if len(frames) == 0 {
		return ReplayPostcard{}, fmt.Errorf("recording has no frames in tick range %d..%d", opt.StartTick, endTick-1)
	}

	var buf bytes.Buffer
	if err := gif.EncodeAll(&buf, &gif.GIF{Image: frames, Delay: delays}); err != nil {
		return ReplayPostcard{}, err
	}
	return ReplayPostcard{
		GIF:       buf.Bytes(),
		WorldName: playback.WorldName(),
		FinalTick: lastTick,
		Frames:    len(frames),
	}, nil
}

func palettedEGA(img image.Image) *image.Paletted {
	palette := make(color.Palette, len(renderEGA))
	for i := range renderEGA {
		palette[i] = renderEGA[i]
	}
	out := image.NewPaletted(img.Bounds(), palette)
	draw.Draw(out, out.Bounds(), img, img.Bounds().Min, draw.Src)
	return out
}
