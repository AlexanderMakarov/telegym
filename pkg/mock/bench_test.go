package mock

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Hot-path benchmarks. Not run by `make test`; use e.g.
//
//	go test -run '^$' -bench . -benchmem ./pkg/mock

// benchPOST reports a failed request with Errorf and false, so it is safe
// from RunParallel goroutines, where FailNow must not be called.
func benchPOST(b *testing.B, h http.Handler, path, body string) bool {
	b.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		b.Errorf("POST %s status=%d body=%s", path, rec.Code, rec.Body.String())
		return false
	}
	return true
}

func BenchmarkSendMessage(b *testing.B) {
	h := New(Config{Quiet: true}).Handler()
	const body = `{"chat_id":42,"text":"Hello, /start","reply_markup":{"inline_keyboard":[[{"text":"Next","callback_data":"next"}]]}}`
	b.ReportAllocs()
	for b.Loop() {
		if !benchPOST(b, h, "/bot741:bench/sendMessage", body) {
			return
		}
	}
}

func BenchmarkSendMessageParallel(b *testing.B) {
	h := New(Config{Quiet: true}).Handler()
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if !benchPOST(b, h, "/bot741:bench/sendMessage", `{"chat_id":42,"text":"Hello"}`) {
				return
			}
		}
	})
}

// BenchmarkInjectUpdateToWebhook measures inject → webhook delivery end to
// end: each iteration waits until the receiver got the update.
func BenchmarkInjectUpdateToWebhook(b *testing.B) {
	received := make(chan struct{}, 1)
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusOK)
		received <- struct{}{}
	}))
	defer receiver.Close()
	h := New(Config{Quiet: true, DefaultWebhookURL: receiver.URL}).Handler()
	const payload = `{"token":"741:bench","chat_id":99,"user_id":99,"first_name":"Bench","text":"ping"}`
	b.ReportAllocs()
	for b.Loop() {
		if !benchPOST(b, h, "/debug/inject/update", payload) {
			return
		}
		select {
		case <-received:
		case <-time.After(5 * time.Second):
			b.Fatal("webhook not delivered within 5s")
		}
	}
}

// BenchmarkDebugChatMessages measures one /debug/chat poll (every 500ms
// per open tab) over a full 200-message history.
func BenchmarkDebugChatMessages(b *testing.B) {
	h := New(Config{Quiet: true}).Handler()
	for i := range 200 {
		if !benchPOST(b, h, "/bot741:bench/sendMessage",
			fmt.Sprintf(`{"chat_id":42,"text":"Message %d with /cmd%d","reply_markup":{"inline_keyboard":[[{"text":"A","callback_data":"a"},{"text":"B","callback_data":"b"}]]}}`, i, i)) {
			return
		}
	}
	b.ReportAllocs()
	for b.Loop() {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/debug/chat/741:bench/42/messages", nil))
		if rec.Code != http.StatusOK {
			b.Fatalf("status=%d", rec.Code)
		}
	}
}
