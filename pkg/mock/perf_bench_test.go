package mock

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Benchmarks for the debug-chat / Bot API parity handlers. These are in-process
// httptest costs of the mock itself (not Telegram network RTT). Numbers belong in
// the PR so reviewers can confirm the new paths stay in the same ballpark as
// existing boolean stubs / sendMessage.

func benchPOST(b *testing.B, srv *Server, token, method, body string) {
	b.Helper()
	h := srv.Handler()
	path := "/bot" + token + "/" + method
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			b.Fatalf("%s status=%d body=%s", method, rec.Code, rec.Body.String())
		}
	}
}

func BenchmarkSendChatAction(b *testing.B) {
	srv := New(Config{Quiet: true})
	benchPOST(b, srv, "bench:typing", "sendChatAction", `{"chat_id":1,"action":"typing"}`)
}

func BenchmarkPinChatMessage_booleanStub(b *testing.B) {
	// Baseline: a still-generic boolean OK stub (same class as pre-PR sendChatAction).
	srv := New(Config{Quiet: true})
	token := "bench:pin"
	// Need a message to pin; seed once outside the timed loop.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/bot"+token+"/sendMessage",
		strings.NewReader(`{"chat_id":1,"text":"seed"}`))
	req.Header.Set("Content-Type", "application/json")
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		b.Fatalf("seed sendMessage: %s", rec.Body.String())
	}
	benchPOST(b, srv, token, "pinChatMessage", `{"chat_id":1,"message_id":1}`)
}

func BenchmarkCreateForumTopic_new(b *testing.B) {
	srv := New(Config{Quiet: true})
	token := "bench:topic-new"
	h := srv.Handler()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Unique names force allocate + service row (worst case for this PR).
		body := fmt.Sprintf(`{"chat_id":1,"name":"T%d"}`, i)
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/bot"+token+"/createForumTopic", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			b.Fatalf("createForumTopic status=%d body=%s", rec.Code, rec.Body.String())
		}
	}
}

func BenchmarkCreateForumTopic_idempotent(b *testing.B) {
	srv := New(Config{Quiet: true})
	benchPOST(b, srv, "bench:topic-idemp", "createForumTopic", `{"chat_id":1,"name":"Theory"}`)
}

func BenchmarkDeleteMessage(b *testing.B) {
	srv := New(Config{Quiet: true})
	token := "bench:del"
	h := srv.Handler()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		recS := httptest.NewRecorder()
		reqS := httptest.NewRequest(http.MethodPost, "/bot"+token+"/sendMessage",
			strings.NewReader(`{"chat_id":1,"text":"x"}`))
		reqS.Header.Set("Content-Type", "application/json")
		h.ServeHTTP(recS, reqS)
		if recS.Code != http.StatusOK {
			b.Fatalf("sendMessage: %s", recS.Body.String())
		}
		var env APIResponse
		if err := json.Unmarshal(recS.Body.Bytes(), &env); err != nil {
			b.Fatal(err)
		}
		raw, _ := json.Marshal(env.Result)
		var sent Message
		if err := json.Unmarshal(raw, &sent); err != nil || sent.MessageID == 0 {
			b.Fatalf("parse sendMessage: id=%d err=%v", sent.MessageID, err)
		}
		b.StartTimer()
		body := fmt.Sprintf(`{"chat_id":1,"message_id":%d}`, sent.MessageID)
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/bot"+token+"/deleteMessage", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			b.Fatalf("deleteMessage status=%d body=%s", rec.Code, rec.Body.String())
		}
	}
}

func BenchmarkSanitizeTelegramHTML(b *testing.B) {
	const sample = `Lesson <b>one</b>: see <a href="https://aybuben.com/selfteacher-1">hub</a> and <i>gloss</i>.`
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = sanitizeTelegramHTML(sample)
	}
}

func BenchmarkSendMessage_plainVsHTML(b *testing.B) {
	srv := New(Config{Quiet: true})
	token := "bench:html"
	h := srv.Handler()

	b.Run("plain", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/bot"+token+"/sendMessage",
				strings.NewReader(`{"chat_id":1,"text":"plain lesson text without tags"}`))
			req.Header.Set("Content-Type", "application/json")
			h.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				b.Fatal(rec.Body.String())
			}
		}
	})
	b.Run("html", func(b *testing.B) {
		b.ReportAllocs()
		body := `{"chat_id":1,"text":"Lesson <b>one</b>: <a href=\"https://example.com\">link</a>","parse_mode":"HTML"}`
		for i := 0; i < b.N; i++ {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/bot"+token+"/sendMessage", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			h.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				b.Fatal(rec.Body.String())
			}
		}
	})
}
