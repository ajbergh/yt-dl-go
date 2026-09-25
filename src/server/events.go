package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

type serviceEvent struct {
	ID                int64             `json:"id"`
	Type              string            `json:"type"`
	Job               *Job              `json:"job,omitempty"`
	JobID             string            `json:"jobId,omitempty"`
	Jobs              *[]Job            `json:"jobs,omitempty"`
	Settings          *AppSettings      `json:"settings,omitempty"`
	SettingsSources   map[string]string `json:"settingsSources,omitempty"`
	SettingsEffective map[string]any    `json:"settingsEffective,omitempty"`
}

type eventBroker struct {
	mu      sync.Mutex
	nextID  int64
	clients map[chan serviceEvent]struct{}
}

func newEventBroker() *eventBroker {
	return &eventBroker{clients: make(map[chan serviceEvent]struct{})}
}

func (b *eventBroker) subscribe() chan serviceEvent {
	ch := make(chan serviceEvent, 128)
	b.mu.Lock()
	b.clients[ch] = struct{}{}
	b.mu.Unlock()
	return ch
}

func (b *eventBroker) unsubscribe(ch chan serviceEvent) {
	if b == nil || ch == nil {
		return
	}
	b.mu.Lock()
	delete(b.clients, ch)
	b.mu.Unlock()
}

func (b *eventBroker) publish(event serviceEvent) {
	if b == nil {
		return
	}
	b.mu.Lock()
	b.nextID++
	event.ID = b.nextID
	for ch := range b.clients {
		select {
		case ch <- event:
		default:
			// Preserve recent state for a slow client rather than blocking all
			// workers on a UI connection. Periodic reconciliation repairs gaps.
			select {
			case <-ch:
			default:
			}
			select {
			case ch <- event:
			default:
			}
		}
	}
	b.mu.Unlock()
}

func (s *server) publishJobEventLocked(eventType string, j *jobState) {
	if s == nil || s.events == nil || j == nil {
		return
	}
	job := snapshot(j)
	s.events.publish(serviceEvent{Type: eventType, Job: &job, JobID: j.ID})
}

func (s *server) publishDeletedEventLocked(jobID string) {
	if s == nil || s.events == nil || jobID == "" {
		return
	}
	s.events.publish(serviceEvent{Type: "job-deleted", JobID: jobID})
}

func (s *server) publishSettingsEventLocked(settings AppSettings) {
	if s == nil || s.events == nil {
		return
	}
	copy := settings
	copy.UserCategories = append([]string(nil), settings.UserCategories...)
	s.events.publish(serviceEvent{
		Type: "settings-changed", Settings: &copy, SettingsSources: runtimeSettingSources(settings, s.cfg),
		SettingsEffective: runtimeSettingEffectiveValues(s.cfg),
	})
}

func writeSSE(w http.ResponseWriter, event serviceEvent) error {
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	if event.ID > 0 {
		if _, err := fmt.Fprintf(w, "id: %d\n", event.ID); err != nil {
			return err
		}
	}
	if event.Type != "" {
		if _, err := fmt.Fprintf(w, "event: %s\n", event.Type); err != nil {
			return err
		}
	}
	_, err = fmt.Fprintf(w, "data: %s\n\n", data)
	return err
}

func (s *server) serveEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		fail(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		fail(w, http.StatusInternalServerError, "Streaming responses are unavailable")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	ch := s.events.subscribe()
	defer s.events.unsubscribe(ch)

	s.mu.Lock()
	jobs := make([]Job, 0, len(s.order))
	for i := len(s.order) - 1; i >= 0; i-- {
		if job := s.jobs[s.order[i]]; job != nil {
			jobs = append(jobs, snapshot(job))
		}
	}
	settings := mergeAppSettings(defaultAppSettings(), s.settings)
	settingsSources := runtimeSettingSources(settings, s.cfg)
	settingsEffective := runtimeSettingEffectiveValues(s.cfg)
	s.mu.Unlock()
	initial := serviceEvent{
		Type: "snapshot", Jobs: &jobs, Settings: &settings, SettingsSources: settingsSources,
		SettingsEffective: settingsEffective,
	}
	if err := writeSSE(w, initial); err != nil {
		return
	}
	flusher.Flush()

	heartbeat := time.NewTicker(20 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case event := <-ch:
			if err := writeSSE(w, event); err != nil {
				return
			}
			flusher.Flush()
		case <-heartbeat.C:
			if _, err := fmt.Fprint(w, ": keep-alive\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}
