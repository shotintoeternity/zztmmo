package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	zztgo "github.com/shotintoeternity/zztmmo/engine"
	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"
)

type loadBot struct {
	conn *websocket.Conn
	id   zztgo.PlayerID

	mu        sync.Mutex
	bytesRead int64
	diffCount int
	err       error
}

type metricsSample struct {
	At     time.Time           `json:"at"`
	Status zztgo.ServiceStatus `json:"status"`
}

func main() {
	base := flag.String("url", "https://zztmmo.com", "base HTTP(S) URL of the server")
	world := flag.String("world", "TOWN", "world basename to join")
	clients := flag.Int("clients", 30, "number of WebSocket clients")
	ticks := flag.Int("ticks", 100, "number of input ticks to drive")
	interval := flag.Duration("metrics-interval", time.Second, "interval for /api/metrics samples; 0 disables during-run sampling")
	jsonOut := flag.Bool("json", false, "write the full measurement as JSON")
	flag.Parse()

	result, err := runLoad(*base, *world, *clients, *ticks, *interval)
	if err != nil {
		log.Fatal(err)
	}
	if *jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(result); err != nil {
			log.Fatal(err)
		}
		return
	}
	printSummary(result)
}

type loadResult struct {
	URL              string          `json:"url"`
	World            string          `json:"world"`
	Clients          int             `json:"clients"`
	Ticks            int             `json:"ticks"`
	WriteFanoutP50MS float64         `json:"writeFanoutP50Ms"`
	WriteFanoutP95MS float64         `json:"writeFanoutP95Ms"`
	WriteFanoutMaxMS float64         `json:"writeFanoutMaxMs"`
	TotalBytesRead   int64           `json:"totalBytesRead"`
	TotalDiffs       int             `json:"totalDiffs"`
	MinDiffs         int             `json:"minDiffs"`
	BotErrors        []string        `json:"botErrors,omitempty"`
	MetricsBefore    metricsSample   `json:"metricsBefore"`
	MetricsDuring    []metricsSample `json:"metricsDuring,omitempty"`
	MetricsAfter     metricsSample   `json:"metricsAfter"`
	ElapsedSeconds   float64         `json:"elapsedSeconds"`
}

func runLoad(base, world string, clients, ticks int, interval time.Duration) (loadResult, error) {
	if clients <= 0 || ticks <= 0 {
		return loadResult{}, fmt.Errorf("clients and ticks must be positive")
	}
	httpBase, wsEndpoint, err := endpoints(base, world)
	if err != nil {
		return loadResult{}, err
	}
	result := loadResult{URL: httpBase, World: world, Clients: clients, Ticks: ticks}

	before, err := fetchMetrics(httpBase)
	if err != nil {
		return result, err
	}
	result.MetricsBefore = before

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(ticks)*zztgo.ServerTickDuration+30*time.Second)
	defer cancel()

	bots := make([]*loadBot, 0, clients)
	var wg sync.WaitGroup
	for i := 0; i < clients; i++ {
		conn, _, err := websocket.Dial(ctx, wsEndpoint, nil)
		if err != nil {
			return result, fmt.Errorf("bot %d dial: %w", i+1, err)
		}
		conn.SetReadLimit(zztgo.ServerReadLimit)
		if err := wsjson.Write(ctx, conn, zztgo.JoinMessage{Type: zztgo.MessageTypeJoin, Name: fmt.Sprintf("LoadBot%02d", i+1)}); err != nil {
			_ = conn.Close(websocket.StatusInternalError, "join failed")
			return result, fmt.Errorf("bot %d join: %w", i+1, err)
		}
		var snap zztgo.SnapshotMessage
		if err := wsjson.Read(ctx, conn, &snap); err != nil {
			_ = conn.Close(websocket.StatusInternalError, "snapshot failed")
			return result, fmt.Errorf("bot %d snapshot: %w", i+1, err)
		}
		bot := &loadBot{conn: conn, id: snap.You.ID}
		bots = append(bots, bot)
		wg.Add(1)
		go readBot(ctx, bot, &wg)
	}

	metricsCtx, stopMetrics := context.WithCancel(context.Background())
	var metricsMu sync.Mutex
	if interval > 0 {
		go func() {
			ticker := time.NewTicker(interval)
			defer ticker.Stop()
			for {
				select {
				case <-metricsCtx.Done():
					return
				case <-ticker.C:
					sample, err := fetchMetrics(httpBase)
					if err != nil {
						continue
					}
					metricsMu.Lock()
					result.MetricsDuring = append(result.MetricsDuring, sample)
					metricsMu.Unlock()
				}
			}
		}()
	}

	started := time.Now()
	writeFanout := make([]time.Duration, 0, ticks)
	for tick := 0; tick < ticks; tick++ {
		start := time.Now()
		for i, bot := range bots {
			dx, dy := direction(i)
			input := map[string]interface{}{
				"type": "input",
				"seq":  uint64(tick + 1),
				"input": map[string]interface{}{
					"dx": dx, "dy": dy, "shift": false,
				},
			}
			if err := wsjson.Write(ctx, bot.conn, input); err != nil {
				bot.mu.Lock()
				bot.err = err
				bot.mu.Unlock()
			}
		}
		writeFanout = append(writeFanout, time.Since(start))
		time.Sleep(zztgo.ServerTickDuration)
	}
	time.Sleep(750 * time.Millisecond)
	result.ElapsedSeconds = time.Since(started).Seconds()
	stopMetrics()

	cancel()
	for _, bot := range bots {
		_ = bot.conn.Close(websocket.StatusNormalClosure, "")
	}
	wg.Wait()

	p50, p95, max := loadPercentiles(writeFanout)
	result.WriteFanoutP50MS = durationMS(p50)
	result.WriteFanoutP95MS = durationMS(p95)
	result.WriteFanoutMaxMS = durationMS(max)
	result.MinDiffs = -1
	for i, bot := range bots {
		bot.mu.Lock()
		bytesRead, diffCount, botErr := bot.bytesRead, bot.diffCount, bot.err
		bot.mu.Unlock()
		result.TotalBytesRead += bytesRead
		result.TotalDiffs += diffCount
		if result.MinDiffs < 0 || diffCount < result.MinDiffs {
			result.MinDiffs = diffCount
		}
		if botErr != nil && !normalReadShutdown(botErr) {
			result.BotErrors = append(result.BotErrors, fmt.Sprintf("bot %d player %d: %v", i+1, bot.id, botErr))
		}
	}
	if result.MinDiffs < 0 {
		result.MinDiffs = 0
	}

	after, err := fetchMetrics(httpBase)
	if err != nil {
		return result, err
	}
	result.MetricsAfter = after
	return result, nil
}

func normalReadShutdown(err error) bool {
	if err == nil {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "context canceled") ||
		strings.Contains(msg, "use of closed network connection") ||
		strings.Contains(msg, "WebSocket closed: status = StatusNormalClosure")
}

func readBot(ctx context.Context, bot *loadBot, wg *sync.WaitGroup) {
	defer wg.Done()
	for {
		typ, data, err := bot.conn.Read(ctx)
		if err != nil {
			bot.mu.Lock()
			bot.err = err
			bot.mu.Unlock()
			return
		}
		if typ == websocket.MessageText {
			bot.mu.Lock()
			bot.bytesRead += int64(len(data))
			if strings.Contains(string(data), `"type":"diff"`) {
				bot.diffCount++
			}
			bot.mu.Unlock()
		}
	}
}

func endpoints(base, world string) (string, string, error) {
	base = strings.TrimRight(base, "/")
	u, err := url.Parse(base)
	if err != nil {
		return "", "", err
	}
	if u.Scheme == "" || u.Host == "" {
		return "", "", fmt.Errorf("url must include scheme and host")
	}
	ws := *u
	switch u.Scheme {
	case "https":
		ws.Scheme = "wss"
	case "http":
		ws.Scheme = "ws"
	default:
		return "", "", fmt.Errorf("unsupported scheme %q", u.Scheme)
	}
	ws.Path = "/ws"
	q := ws.Query()
	q.Set("world", world)
	ws.RawQuery = q.Encode()
	return base, ws.String(), nil
}

func fetchMetrics(base string) (metricsSample, error) {
	resp, err := http.Get(strings.TrimRight(base, "/") + "/api/metrics")
	if err != nil {
		return metricsSample{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return metricsSample{}, fmt.Errorf("GET /api/metrics returned %s", resp.Status)
	}
	var status zztgo.ServiceStatus
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		return metricsSample{}, err
	}
	return metricsSample{At: time.Now().UTC(), Status: status}, nil
}

func direction(i int) (int16, int16) {
	switch i % 4 {
	case 0:
		return 1, 0
	case 1:
		return -1, 0
	case 2:
		return 0, 1
	default:
		return 0, -1
	}
}

func loadPercentiles(samples []time.Duration) (p50, p95, max time.Duration) {
	if len(samples) == 0 {
		return 0, 0, 0
	}
	sorted := append([]time.Duration(nil), samples...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	p50 = sorted[len(sorted)*50/100]
	idx95 := len(sorted) * 95 / 100
	if idx95 >= len(sorted) {
		idx95 = len(sorted) - 1
	}
	p95 = sorted[idx95]
	max = sorted[len(sorted)-1]
	return p50, p95, max
}

func durationMS(d time.Duration) float64 {
	return float64(d) / float64(time.Millisecond)
}

func printSummary(result loadResult) {
	fmt.Printf("ZZTMMO load result: %d clients, %d ticks, world %s\n", result.Clients, result.Ticks, result.World)
	fmt.Printf("Write fanout: p50 %.2fms, p95 %.2fms, max %.2fms\n",
		result.WriteFanoutP50MS, result.WriteFanoutP95MS, result.WriteFanoutMaxMS)
	fmt.Printf("Diffs: %d total, slowest client %d, bytes %.1f KiB\n",
		result.TotalDiffs, result.MinDiffs, float64(result.TotalBytesRead)/1024)
	before := result.MetricsBefore.Status
	after := result.MetricsAfter.Status
	fmt.Printf("Server totals before: players=%d rooms=%d activeRooms=%d instances=%d heap=%.1f MiB\n",
		before.Totals.Players, before.Totals.Rooms, before.Totals.ActiveRooms, before.Totals.Instances,
		float64(before.Memory.HeapAllocBytes)/(1024*1024))
	fmt.Printf("Server totals after:  players=%d rooms=%d activeRooms=%d instances=%d heap=%.1f MiB\n",
		after.Totals.Players, after.Totals.Rooms, after.Totals.ActiveRooms, after.Totals.Instances,
		float64(after.Memory.HeapAllocBytes)/(1024*1024))
	fmt.Printf("Server tick after: count=%d avg=%.2fms p95=%.2fms max=%.2fms\n",
		after.Tick.Count, after.Tick.AvgMS, after.Tick.P95MS, after.Tick.MaxMS)
	for _, inst := range after.Instances {
		if inst.Players == 0 && inst.Step.Count == 0 {
			continue
		}
		fmt.Printf("Instance %s: players=%d rooms=%d activeRooms=%d step avg=%.2fms p95=%.2fms max=%.2fms tick avg=%.2fms p95=%.2fms max=%.2fms\n",
			inst.Name, inst.Players, inst.Rooms, inst.ActiveRooms,
			inst.Step.AvgMS, inst.Step.P95MS, inst.Step.MaxMS,
			inst.Tick.AvgMS, inst.Tick.P95MS, inst.Tick.MaxMS)
	}
	if len(result.BotErrors) > 0 {
		fmt.Println("Bot errors:")
		for _, err := range result.BotErrors {
			fmt.Println("  " + err)
		}
	}
}
