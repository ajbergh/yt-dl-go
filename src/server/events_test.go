package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type sseTestWriter struct {
	mu      sync.Mutex
	header  http.Header
	body    bytes.Buffer
	flushed chan struct{}
}

func newSSETestWriter() *sseTestWriter {
	return &sseTestWriter{header: make(http.Header), flushed: make(chan struct{}, 1)}
}
func (w *sseTestWriter) Header() http.Header { return w.header }
func (w *sseTestWriter) WriteHeader(int)     {}
func (w *sseTestWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.body.Write(p)
}
func (w *sseTestWriter) Flush() {
	select {
	case w.flushed <- struct{}{}:
	default:
	}
}
func (w *sseTestWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.body.String()
}

func TestSSERequiresAuthentication(t *testing.T) {
	s := testServer(t, fixtureClient(1), nil)
	response := request(s, http.MethodGet, "/api/events", "", map[string]string{"Authorization": ""})
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated SSE endpoint = %d, want 401", response.Code)
	}
}

func TestSSEInitialSnapshotAndFraming(t *testing.T) {
	s := testServer(t, fixtureClient(1), nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8080/api/events", nil).WithContext(ctx)
	writer := newSSETestWriter()
	done := make(chan struct{})
	go func() {
		s.serveEvents(writer, req)
		close(done)
	}()

	select {
	case <-writer.flushed:
	case <-time.After(time.Second):
		t.Fatal("SSE snapshot was not flushed")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("SSE handler did not stop after cancellation")
	}

	body := writer.String()
	if writer.Header().Get("Content-Type") != "text/event-stream" ||
		!strings.Contains(body, "event: snapshot\n") ||
		!strings.Contains(body, `"type":"snapshot"`) ||
		!strings.Contains(body, `"jobs":[]`) ||
		!strings.Contains(body, `"settings"`) {
		t.Fatalf("invalid initial SSE response: headers=%v body=%q", writer.Header(), body)
	}
}

func TestSSEPublishesCreatedJobSnapshot(t *testing.T) {
	s := testServer(t, fixtureClient(1), nil)
	ch := s.events.subscribe()
	defer s.events.unsubscribe(ch)
	created := createJob(t, s, testVideo)

	deadline := time.After(time.Second)
	for {
		select {
		case event := <-ch:
			if event.Type != "job-created" {
				continue
			}
			if event.Job == nil || event.Job.ID != created.ID || event.JobID != created.ID {
				t.Fatalf("job-created SSE payload = %+v", event)
			}
			return
		case <-deadline:
			t.Fatal("job-created SSE event was not published")
		}
	}
}
