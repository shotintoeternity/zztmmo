package zztgo

import (
	"context"
	"testing"
)

// M18.18 — idle world instances are evicted on the tick clock, but only after
// every live service claim on them is gone.

func TestM1818IdleNonDefaultInstanceEvictsAfterTickBudget(t *testing.T) {
	server := NewWebSocketServer(testEmptyWorld(t), 1)
	server.InstanceEvictIdleTicks = 2
	if err := server.HostGeneratedWorld("IDLEONE", testEmptyWorld(t)); err != nil {
		t.Fatalf("host generated world: %v", err)
	}

	ctx := context.Background()
	server.Tick(ctx)
	if server.Instances["IDLEONE"] == nil {
		t.Fatal("instance evicted before its idle tick budget elapsed")
	}

	server.Tick(ctx)
	if server.Instances["IDLEONE"] != nil {
		t.Fatal("idle non-default instance survived past its tick budget")
	}
	if _, _, timings := server.metrics.snapshot(); timings["IDLEONE"].Tick.Count != 0 {
		t.Fatal("evicted instance left its timing row behind")
	}
}

func TestM1818DefaultInstanceIsNeverEvicted(t *testing.T) {
	server := NewWebSocketServer(testEmptyWorld(t), 1)
	server.InstanceEvictIdleTicks = 1

	for i := 0; i < 5; i++ {
		server.Tick(context.Background())
	}
	if server.DefaultInstance == nil {
		t.Fatal("default instance pointer was cleared")
	}
	if server.Instances[server.DefaultInstance.Name] != server.DefaultInstance {
		t.Fatal("default instance was evicted from the instance map")
	}
}

func TestM1818DetachedReconnectGraceBlocksEviction(t *testing.T) {
	server := NewWebSocketServer(testEmptyWorld(t), 1)
	server.InstanceEvictIdleTicks = 1
	if err := server.HostGeneratedWorld("GRACEFUL", testEmptyWorld(t)); err != nil {
		t.Fatalf("host generated world: %v", err)
	}
	inst := server.Instances["GRACEFUL"]

	playerID := server.mintPlayerID()
	inst.RoomManager.JoinPlayerWithID(playerID, 1, 0, 0)
	client := &webSocketClient{playerID: playerID}
	inst.mu.Lock()
	inst.Clients[playerID] = client
	inst.mintResumeTokenLocked(playerID)
	inst.mu.Unlock()

	server.handleReadLoopExit(inst, client, playerID)
	server.Tick(context.Background())

	if server.Instances["GRACEFUL"] == nil {
		t.Fatal("instance with a detached player inside reconnect grace was evicted")
	}
	inst.mu.Lock()
	detached := len(inst.Detached)
	inst.mu.Unlock()
	if detached == 0 {
		t.Fatal("test setup lost the detached reconnect state it meant to protect")
	}
}

func TestM1818TitleSubscriberBlocksEviction(t *testing.T) {
	server := NewWebSocketServer(testEmptyWorld(t), 1)
	server.InstanceEvictIdleTicks = 1
	if err := server.HostGeneratedWorld("TITLEON", testEmptyWorld(t)); err != nil {
		t.Fatalf("host generated world: %v", err)
	}
	inst := server.Instances["TITLEON"]
	_, cancel := inst.Title.Subscribe()

	server.Tick(context.Background())
	if server.Instances["TITLEON"] == nil {
		t.Fatal("instance serving a title stream was evicted")
	}

	cancel()
	server.Tick(context.Background())
	if server.Instances["TITLEON"] != nil {
		t.Fatal("instance survived after its title stream ended and idle budget elapsed")
	}
}

func TestM1818AutosaveInProgressBlocksEviction(t *testing.T) {
	server := NewWebSocketServer(testEmptyWorld(t), 1)
	server.InstanceEvictIdleTicks = 1
	if err := server.HostGeneratedWorld("SAVING", testEmptyWorld(t)); err != nil {
		t.Fatalf("host generated world: %v", err)
	}
	inst := server.Instances["SAVING"]

	inst.mu.Lock()
	inst.autosaving = true
	inst.mu.Unlock()
	server.Tick(context.Background())
	if server.Instances["SAVING"] == nil {
		t.Fatal("instance was evicted while an autosave was in progress")
	}

	inst.mu.Lock()
	inst.autosaving = false
	inst.mu.Unlock()
	server.Tick(context.Background())
	if server.Instances["SAVING"] != nil {
		t.Fatal("instance survived after autosave ended and idle budget elapsed")
	}
}
