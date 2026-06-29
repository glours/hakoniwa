package orchestrator

import (
	"encoding/json"
	"fmt"
	"sync"
)

// ChannelEntry is the registry record for one event channel.
type ChannelEntry struct {
	Emitter string          // agent key of the emitter (from config)
	Fired   bool            // true once the emitter's session completes + out file exists
	Payload json.RawMessage // content of .hako/out/<channel>.json, nil until fired
}

// ChannelRegistry is the in-process channel bus for a single project run.
// It is safe for concurrent use by goroutines driving multiple agents.
//
// Channels are pre-registered from the project config; agents observe and fire
// them during Up execution. The registry is one-shot: once fired, a channel
// stays fired for the lifetime of the Up call.
type ChannelRegistry struct {
	mu      sync.RWMutex
	entries map[string]*ChannelEntry // key = channel name
	// fired is closed once each channel fires; used for blocking waits.
	fired map[string]chan struct{}
}

// NewChannelRegistry constructs a registry pre-populated with the project's
// declared channels. emitterOf maps channel name → agent key of the emitter
// (determined from config.Agent.Emits).
func NewChannelRegistry(channels []string, emitterOf map[string]string) *ChannelRegistry {
	r := &ChannelRegistry{
		entries: make(map[string]*ChannelEntry, len(channels)),
		fired:   make(map[string]chan struct{}, len(channels)),
	}
	for _, ch := range channels {
		emitter := emitterOf[ch]
		r.entries[ch] = &ChannelEntry{Emitter: emitter}
		r.fired[ch] = make(chan struct{})
	}
	return r
}

// Fire marks channel ch as fired with the given payload.
// Returns an error if ch is not a registered channel or is already fired.
func (r *ChannelRegistry) Fire(ch string, payload json.RawMessage) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	entry, ok := r.entries[ch]
	if !ok {
		return fmt.Errorf("channel %q is not registered", ch)
	}
	if entry.Fired {
		return fmt.Errorf("channel %q is already fired", ch)
	}
	entry.Fired = true
	entry.Payload = payload
	close(r.fired[ch]) // unblocks any WaitFired callers
	return nil
}

// IsFired returns true if ch has already been fired.
func (r *ChannelRegistry) IsFired(ch string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.entries[ch]
	return ok && e.Fired
}

// Payload returns the payload for channel ch.
// Returns nil, false if the channel is not fired or not registered.
func (r *ChannelRegistry) Payload(ch string) (json.RawMessage, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.entries[ch]
	if !ok || !e.Fired {
		return nil, false
	}
	return e.Payload, true
}

// WaitFired returns a channel that is closed when ch fires.
// Returns a nil channel (blocks forever) if ch is not registered —
// callers should always pre-check that the channel name is valid.
func (r *ChannelRegistry) WaitFired(ch string) <-chan struct{} {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if c, ok := r.fired[ch]; ok {
		return c
	}
	return nil // not registered; caller's select will never select on nil
}

// Emitter returns the agent key configured as the emitter for channel ch.
// Returns "" if ch is not registered.
func (r *ChannelRegistry) Emitter(ch string) string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if e, ok := r.entries[ch]; ok {
		return e.Emitter
	}
	return ""
}
