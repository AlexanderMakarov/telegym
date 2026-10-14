package mock

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Before/after PR impact benches — same names on upstream main and this branch.
// Run via scripts/bench-compare.sh (not part of make test / preflight).
//
// On main: sendChatAction → GenericOK (NoRoute/dispatcher); deleteMessage is a no-op stub.
// On this PR: sendChatAction records typing; deleteMessage removes store entries.

func comparePOST(b *testing.B, srv *Server, token, method, body string) {
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

func BenchmarkPRImpact_SendChatAction(b *testing.B) {
	srv := New(Config{Quiet: true})
	comparePOST(b, srv, "cmp:typing", "sendChatAction", `{"chat_id":1,"action":"typing"}`)
}

func BenchmarkPRImpact_PinChatMessage(b *testing.B) {
	srv := New(Config{Quiet: true})
	token := "cmp:pin"
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/bot"+token+"/sendMessage",
		strings.NewReader(`{"chat_id":1,"text":"seed"}`))
	req.Header.Set("Content-Type", "application/json")
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		b.Fatalf("seed: %s", rec.Body.String())
	}
	comparePOST(b, srv, token, "pinChatMessage", `{"chat_id":1,"message_id":1}`)
}

func BenchmarkPRImpact_DeleteMessage(b *testing.B) {
	srv := New(Config{Quiet: true})
	token := "cmp:del"
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
		_ = json.Unmarshal(recS.Body.Bytes(), &env)
		raw, _ := json.Marshal(env.Result)
		var sent Message
		_ = json.Unmarshal(raw, &sent)
		msgID := sent.MessageID
		if msgID == 0 {
			msgID = 1 // main stub path may still decode; fall back for safety
		}
		b.StartTimer()
		body := fmt.Sprintf(`{"chat_id":1,"message_id":%d}`, msgID)
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/bot"+token+"/deleteMessage", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			b.Fatalf("deleteMessage status=%d body=%s", rec.Code, rec.Body.String())
		}
	}
}

func BenchmarkPRImpact_SendMessage(b *testing.B) {
	srv := New(Config{Quiet: true})
	comparePOST(b, srv, "cmp:send", "sendMessage", `{"chat_id":1,"text":"plain lesson text without tags"}`)
}
