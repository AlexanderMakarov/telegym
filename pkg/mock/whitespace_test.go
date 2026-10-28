package mock

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Telegram clients keep runs of spaces in text and parse_mode=HTML
// messages (space-aligned columns stay aligned). Rich message HTML follows
// HTML whitespace rules instead.
func TestDebugChatKeepsTextWhitespace(t *testing.T) {
	const token = "742:space"
	srv := New(Config{Quiet: true})
	send := func(method, body string) {
		t.Helper()
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/bot"+token+"/"+method, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		srv.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s status=%d body=%s", method, rec.Code, rec.Body.String())
		}
	}
	send("sendMessage", `{"chat_id":42,"text":"Column 1      Column 2\n 1   She sees me."}`)
	send("sendMessage", `{"chat_id":42,"text":"<b>N</b>      V      Adj.","parse_mode":"HTML"}`)
	send("sendRichMessage", `{"chat_id":42,"rich_message":{"html":"<p>rich\n   text</p>"}}`)

	get := func(path string) string {
		t.Helper()
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s status=%d", path, rec.Code)
		}
		return rec.Body.String()
	}
	msgs := get("/debug/chat/" + token + "/42/messages")
	for _, want := range []string{
		`<div class="msg-text">Column 1      Column 2<br> 1   She sees me.</div>`,
		`<div class="msg-text"><b>N</b>      V      Adj.</div>`,
		`<div class="msg-text"><div class="rich">`,
	} {
		if !strings.Contains(msgs, want) {
			t.Errorf("missing %q in debug chat:\n%s", want, msgs)
		}
	}

	page := get("/debug/chat/" + token + "/42")
	for _, want := range []string{
		".msg-text { white-space: pre-wrap; }",
		".msg-text .rich { white-space: normal; }",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("chat page CSS missing %q", want)
		}
	}
}
