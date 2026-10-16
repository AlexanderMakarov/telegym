package mock

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func doTypingTestPOST(t *testing.T, srv *Server, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

func typingTestChatHTML(t *testing.T, srv *Server, path string) string {
	t.Helper()
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("messages status=%d body=%s", rec.Code, rec.Body.String())
	}
	return rec.Body.String()
}

func TestSendChatActionValidation(t *testing.T) {
	tests := []struct {
		name   string
		body   string
		status int
	}{
		{"typing", `{"chat_id":7,"action":"typing"}`, http.StatusOK},
		{"upload_photo", `{"chat_id":7,"action":"upload_photo"}`, http.StatusOK},
		{"unknown action", `{"chat_id":7,"action":"dancing"}`, http.StatusBadRequest},
		{"missing action", `{"chat_id":7}`, http.StatusBadRequest},
		{"missing chat_id", `{"action":"typing"}`, http.StatusBadRequest},
		{"malformed body", `{"chat_id":`, http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := New(Config{Quiet: true})
			rec := doTypingTestPOST(t, srv, "/bot741:typing/sendChatAction", tt.body)
			if rec.Code != tt.status {
				t.Fatalf("status=%d want %d body=%s", rec.Code, tt.status, rec.Body.String())
			}
		})
	}
}

func TestSendChatActionShownUntilNextMessage(t *testing.T) {
	const token = "741:typing"
	srv := New(Config{Quiet: true})
	chatPath := "/debug/chat/" + token + "/7/messages"

	if rec := doTypingTestPOST(t, srv, "/bot"+token+"/sendChatAction", `{"chat_id":7,"action":"typing"}`); rec.Code != http.StatusOK {
		t.Fatalf("sendChatAction status=%d body=%s", rec.Code, rec.Body.String())
	}
	if html := typingTestChatHTML(t, srv, chatPath); !strings.Contains(html, "typing…") {
		t.Fatalf("want typing bubble, got %s", html)
	}
	if html := typingTestChatHTML(t, srv, "/debug/chat/"+token+"/8/messages"); strings.Contains(html, "typing…") {
		t.Fatalf("typing must be scoped to its chat, got %s", html)
	}

	if rec := doTypingTestPOST(t, srv, "/bot"+token+"/sendMessage", `{"chat_id":7,"text":"hello"}`); rec.Code != http.StatusOK {
		t.Fatalf("sendMessage status=%d", rec.Code)
	}
	// Still visible inside the min-visible window so a fast reply does not
	// clear it before the 500ms poll can paint it.
	if html := typingTestChatHTML(t, srv, chatPath); !strings.Contains(html, "typing…") {
		t.Fatalf("typing must stay visible for typingMinVisible, got %s", html)
	}
	time.Sleep(typingMinVisible + 100*time.Millisecond)
	if html := typingTestChatHTML(t, srv, chatPath); strings.Contains(html, "typing…") {
		t.Fatalf("typing must clear after the next message, got %s", html)
	}
}

func TestChatActionExpiresAfterTTL(t *testing.T) {
	bot := New(Config{Quiet: true}).Store().Bot("741:typing")
	bot.SetTyping(7, 0, "upload_photo")
	if got := bot.ActiveTyping(7); len(got) != 1 || got[0].Action != "upload_photo" {
		t.Fatalf("ActiveTyping = %+v", got)
	}
	bot.typingMu.Lock()
	bot.typing[typingKey{7, 0}] = chatTyping{action: "upload_photo", since: time.Now().Add(-typingTTL), until: time.Now()}
	bot.typingMu.Unlock()
	if got := bot.ActiveTyping(7); len(got) != 0 {
		t.Fatal("chat action must expire after typingTTL")
	}
}

func TestChatActionSurvivesEdits(t *testing.T) {
	const token = "741:typing"
	srv := New(Config{Quiet: true})
	bot := srv.Store().Bot(token)
	doTypingTestPOST(t, srv, "/bot"+token+"/sendChatAction", `{"chat_id":7,"action":"typing"}`)
	// Age the action past the min-visible window so a clear would drop it.
	bot.typingMu.Lock()
	ty := bot.typing[typingKey{7, 0}]
	ty.since = ty.since.Add(-2 * typingMinVisible)
	bot.typing[typingKey{7, 0}] = ty
	bot.typingMu.Unlock()

	doTypingTestPOST(t, srv, "/bot"+token+"/editMessageText", `{"chat_id":7,"message_id":1,"text":"streamed"}`)
	if len(bot.ActiveTyping(7)) != 1 {
		t.Fatal("an edit must not end the chat action")
	}
	doTypingTestPOST(t, srv, "/bot"+token+"/sendMessage", `{"chat_id":7,"text":"done"}`)
	if len(bot.ActiveTyping(7)) != 0 {
		t.Fatal("a new message must end the chat action")
	}
}
