package orchestrator

import (
	"encoding/json"
	"sync"
	"testing"
	"time"
)

func TestChannelRegistryFire(t *testing.T) {
	reg := NewChannelRegistry([]string{"ready"}, map[string]string{"ready": "alpha"})

	if reg.IsFired("ready") {
		t.Fatal("channel should not be fired initially")
	}

	payload := json.RawMessage(`{"status":"ok"}`)
	if err := reg.Fire("ready", payload); err != nil {
		t.Fatalf("Fire: %v", err)
	}

	if !reg.IsFired("ready") {
		t.Error("channel should be fired")
	}
	got, ok := reg.Payload("ready")
	if !ok {
		t.Error("Payload should return ok after fire")
	}
	if string(got) != string(payload) {
		t.Errorf("payload = %s, want %s", got, payload)
	}
}

func TestChannelRegistryFireUnregistered(t *testing.T) {
	reg := NewChannelRegistry(nil, nil)
	if err := reg.Fire("unknown", nil); err == nil {
		t.Error("expected error for unregistered channel")
	}
}

func TestChannelRegistryFireTwice(t *testing.T) {
	reg := NewChannelRegistry([]string{"ch"}, nil)
	_ = reg.Fire("ch", nil)
	if err := reg.Fire("ch", nil); err == nil {
		t.Error("expected error on double fire")
	}
}

func TestChannelRegistryWaitFired(t *testing.T) {
	reg := NewChannelRegistry([]string{"sig"}, nil)

	done := make(chan struct{})
	go func() {
		// Fire after a brief delay.
		time.Sleep(20 * time.Millisecond)
		_ = reg.Fire("sig", nil)
	}()
	go func() {
		select {
		case <-reg.WaitFired("sig"):
			close(done)
		case <-time.After(2 * time.Second):
		}
	}()

	select {
	case <-done:
		// success
	case <-time.After(3 * time.Second):
		t.Error("WaitFired did not unblock after Fire")
	}
}

func TestChannelRegistryWaitFiredUnregistered(t *testing.T) {
	reg := NewChannelRegistry(nil, nil)
	ch := reg.WaitFired("ghost")
	if ch != nil {
		t.Error("expected nil channel for unregistered name")
	}
}

func TestChannelRegistryEmitter(t *testing.T) {
	reg := NewChannelRegistry([]string{"x"}, map[string]string{"x": "agent-a"})
	if got := reg.Emitter("x"); got != "agent-a" {
		t.Errorf("Emitter = %q, want %q", got, "agent-a")
	}
	if got := reg.Emitter("notexist"); got != "" {
		t.Errorf("Emitter for missing channel = %q, want empty", got)
	}
}

func TestChannelRegistryConcurrent(t *testing.T) {
	const n = 20
	channels := make([]string, n)
	for i := range channels {
		channels[i] = "ch" + string(rune('a'+i))
	}
	reg := NewChannelRegistry(channels, nil)

	var wg sync.WaitGroup
	for _, ch := range channels {
		ch := ch
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = reg.Fire(ch, json.RawMessage(`"ok"`))
		}()
	}
	wg.Wait()

	for _, ch := range channels {
		if !reg.IsFired(ch) {
			t.Errorf("channel %q not fired", ch)
		}
	}
}
