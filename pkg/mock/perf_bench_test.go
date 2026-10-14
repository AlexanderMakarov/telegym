package mock

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// New-feature microbenchmarks (this PR only). Not run by `go test` / `make test`
// / `make preflight` unless you pass -bench. Prefer scripts/bench-compare.sh for
// before/after vs upstream main; use these for createForumTopic / HTML sanitize.

func featurePOST(b *testing.B, srv *Server, token, method, body string) {
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

func BenchmarkFeature_CreateForumTopic_idempotent(b *testing.B) {
	srv := New(Config{Quiet: true})
	featurePOST(b, srv, "feat:topic-idemp", "createForumTopic", `{"chat_id":1,"name":"Theory"}`)
}

func BenchmarkFeature_CreateForumTopic_new(b *testing.B) {
	srv := New(Config{Quiet: true})
	token := "feat:topic-new"
	h := srv.Handler()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
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

func BenchmarkFeature_SanitizeTelegramHTML(b *testing.B) {
	const sample = `Lesson <b>one</b>: see <a href="https://aybuben.com/selfteacher-1">hub</a> and <i>gloss</i>.`
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = sanitizeTelegramHTML(sample)
	}
}

func BenchmarkFeature_SendMessage_HTML(b *testing.B) {
	srv := New(Config{Quiet: true})
	token := "feat:html"
	h := srv.Handler()
	body := `{"chat_id":1,"text":"Lesson <b>one</b>: <a href=\"https://example.com\">link</a>","parse_mode":"HTML"}`
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/bot"+token+"/sendMessage", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			b.Fatal(rec.Body.String())
		}
	}
}

func BenchmarkFeature_DeleteMessage_real(b *testing.B) {
	// Same shape as BenchmarkPRImpact_DeleteMessage but named for feature docs.
	srv := New(Config{Quiet: true})
	token := "feat:del"
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
		var env APIResponse
		_ = json.Unmarshal(recS.Body.Bytes(), &env)
		raw, _ := json.Marshal(env.Result)
		var sent Message
		_ = json.Unmarshal(raw, &sent)
		b.StartTimer()
		body := fmt.Sprintf(`{"chat_id":1,"message_id":%d}`, sent.MessageID)
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/bot"+token+"/deleteMessage", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			b.Fatalf("deleteMessage status=%d", rec.Code)
		}
	}
}
