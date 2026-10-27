package mock

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// longPoll starts a getUpdates long poll and waits until the mock registered
// it, so a following request deterministically supersedes it.
func longPoll(t *testing.T, srv *Server, token string) <-chan *httptest.ResponseRecorder {
	t.Helper()
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/bot"+token+"/getUpdates?timeout=30", nil))
		done <- rec
	}()
	q := srv.store.Bot(token).updates
	deadline := time.Now().Add(2 * time.Second)
	for {
		q.mu.Lock()
		active := q.pollCancel != nil
		q.mu.Unlock()
		if active {
			return done
		}
		if time.Now().After(deadline) {
			t.Fatal("long poll did not start")
		}
		time.Sleep(time.Millisecond)
	}
}

func awaitConflict(t *testing.T, done <-chan *httptest.ResponseRecorder, want pollConflict) {
	t.Helper()
	select {
	case rec := <-done:
		var resp APIResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if rec.Code != http.StatusConflict || resp.OK || resp.Description != string(want) {
			t.Fatalf("status=%d body=%s, want 409 %q", rec.Code, rec.Body.String(), want)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("superseded poll did not return")
	}
}

func TestPollingNewestRequestWins(t *testing.T) {
	srv := New(Config{Quiet: true})
	startPolling(t, srv, "888:poll")
	older := longPoll(t, srv, "888:poll")

	// Like Telegram, the new long poll takes over and the old one gets 409.
	newer := longPoll(t, srv, "888:poll")
	awaitConflict(t, older, errPollTerminated)

	queueText(t, srv, "888:poll", "hello")
	select {
	case rec := <-newer:
		updates := decodeUpdates(t, rec)
		if len(updates) != 1 || updates[0].Message == nil || updates[0].Message.Text != "hello" {
			t.Fatalf("unexpected updates: %+v", updates)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("newest poll did not wake up")
	}
}

func TestPollingSupersededWithOfficialText(t *testing.T) {
	srv := New(Config{Quiet: true})
	startPolling(t, srv, "888:poll")
	old := longPoll(t, srv, "888:poll")

	updates := decodeUpdates(t, pollingRequest(srv, http.MethodGet, "/bot888:poll/getUpdates", ""))
	if len(updates) != 0 {
		t.Fatalf("unexpected updates: %+v", updates)
	}
	awaitConflict(t, old, errPollTerminated)
}

func TestSetWebhookTerminatesPoll(t *testing.T) {
	srv := New(Config{Quiet: true})
	startPolling(t, srv, "888:poll")
	old := longPoll(t, srv, "888:poll")

	rec := pollingRequest(srv, http.MethodPost, "/bot888:poll/setWebhook", `{"url":"https://example.com/hook"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("setWebhook status=%d body=%s", rec.Code, rec.Body.String())
	}
	awaitConflict(t, old, errPollWebhookSet)

	rec = pollingRequest(srv, http.MethodGet, "/bot888:poll/getUpdates", "")
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), string(errPollWebhook)) {
		t.Fatalf("getUpdates with webhook status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestInjectWithoutTargetFailsFast(t *testing.T) {
	srv := New(Config{Quiet: true})
	rec := pollingRequest(srv, http.MethodPost, "/debug/inject/update", `{"token":"888:poll","chat_id":42,"text":"lost"}`)
	if rec.Code != http.StatusBadGateway || !strings.Contains(rec.Body.String(), "no webhook URL set") {
		t.Fatalf("inject without webhook or polling: status=%d body=%s, want 502", rec.Code, rec.Body.String())
	}
	metrics := httptest.NewRecorder()
	srv.metrics.Handler().ServeHTTP(metrics, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if !strings.Contains(metrics.Body.String(), `telegym_mock_webhook_dispatches_total{result="no_url"} 1`) {
		t.Fatalf("no_url not counted: %s", metrics.Body.String())
	}

	// The first getUpdates opts the bot into polling; later updates are queued.
	decodeUpdates(t, pollingRequest(srv, http.MethodGet, "/bot888:poll/getUpdates", ""))
	queueText(t, srv, "888:poll", "kept")
	updates := decodeUpdates(t, pollingRequest(srv, http.MethodGet, "/bot888:poll/getUpdates", ""))
	if len(updates) != 1 || updates[0].Message == nil || updates[0].Message.Text != "kept" {
		t.Fatalf("unexpected updates: %+v", updates)
	}
}

func TestGetUpdatesTolerantBody(t *testing.T) {
	srv := New(Config{Quiet: true})
	startPolling(t, srv, "888:poll")
	queueText(t, srv, "888:poll", "first")
	queueText(t, srv, "888:poll", "second")
	first := decodeUpdates(t, pollingRequest(srv, http.MethodGet, "/bot888:poll/getUpdates", ""))[0].UpdateID

	// An empty JSON body means "no parameters", as in Telegram.
	req := httptest.NewRequest(http.MethodPost, "/bot888:poll/getUpdates", nil)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if updates := decodeUpdates(t, rec); len(updates) != 2 {
		t.Fatalf("empty body: %+v", updates)
	}

	// String-encoded numbers are accepted.
	body := fmt.Sprintf(`{"offset":"%d","limit":"1"}`, first+1)
	updates := decodeUpdates(t, pollingRequest(srv, http.MethodPost, "/bot888:poll/getUpdates", body))
	if len(updates) != 1 || updates[0].Message == nil || updates[0].Message.Text != "second" {
		t.Fatalf("string params: %+v", updates)
	}
}

// TestPollingConcurrentInjectionLosesNothing injects from many goroutines while
// a client polls and confirms; every update must arrive exactly once.
func TestPollingConcurrentInjectionLosesNothing(t *testing.T) {
	srv := New(Config{Quiet: true})
	startPolling(t, srv, "888:poll")
	const workers, perWorker = 32, 50
	const total = workers * perWorker

	var injected atomic.Int64
	var wg sync.WaitGroup
	for w := range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range perWorker {
				body := fmt.Sprintf(`{"token":"888:poll","chat_id":42,"text":"%d-%d"}`, w, i)
				rec := pollingRequest(srv, http.MethodPost, "/debug/inject/update", body)
				if rec.Code != http.StatusOK {
					t.Errorf("inject status=%d body=%s", rec.Code, rec.Body.String())
					return
				}
				injected.Add(1)
			}
		}()
	}

	seen := make(map[string]bool, total)
	var offset int64
	deadline := time.Now().Add(10 * time.Second)
	for len(seen) < total && time.Now().Before(deadline) {
		path := fmt.Sprintf("/bot888:poll/getUpdates?offset=%d&timeout=1", offset)
		updates := decodeUpdates(t, pollingRequest(srv, http.MethodGet, path, ""))
		for _, u := range updates {
			if u.UpdateID < offset {
				t.Fatalf("update_id %d below offset %d", u.UpdateID, offset)
			}
			if seen[u.Message.Text] {
				t.Fatalf("duplicate update %q", u.Message.Text)
			}
			seen[u.Message.Text] = true
			offset = u.UpdateID + 1
		}
	}
	wg.Wait()
	if len(seen) != total || injected.Load() != total {
		t.Fatalf("received %d of %d updates (injected %d)", len(seen), total, injected.Load())
	}
}

func TestUpdateQueueOrderedUnderConcurrency(t *testing.T) {
	q := newUpdateQueue()
	var seq atomic.Int64
	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 200 {
				q.Enqueue(Update{}, func() int64 { return seq.Add(1) })
			}
		}()
	}
	wg.Wait()
	for i := 1; i < len(q.updates); i++ {
		if q.updates[i].UpdateID <= q.updates[i-1].UpdateID {
			t.Fatalf("queue out of order at %d: %d after %d", i, q.updates[i].UpdateID, q.updates[i-1].UpdateID)
		}
	}
}
