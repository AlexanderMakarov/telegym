package mock

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestChatActionPerTopic(t *testing.T) {
	const token = "741:topic-typing"
	srv := New(Config{Quiet: true})
	bot := srv.Store().Bot(token)
	theory := createTestTopic(t, srv, token, `{"chat_id":42,"name":"Theory"}`).MessageThreadID
	doTypingTestPOST(t, srv, "/bot"+token+"/sendChatAction",
		fmt.Sprintf(`{"chat_id":42,"message_thread_id":%d,"action":"typing"}`, theory))

	view := func(thread string) string {
		path := "/debug/chat/" + token + "/42/messages?name=ann"
		if thread != "" {
			path += "&thread=" + thread
		}
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		return rec.Body.String()
	}
	const bubble = `class="msg bot chat-action"`
	if html := view(fmt.Sprint(theory)); !strings.Contains(html, bubble) {
		t.Fatalf("Theory tab must show its typing:\n%s", html)
	}
	if html := view("0"); strings.Contains(html, bubble) {
		t.Fatalf("General tab must not show Theory typing:\n%s", html)
	}
	if html := view(""); !strings.Contains(html, `<span class="topic-label">Theory</span>typing…`) {
		t.Fatalf("All view must label typing with its topic:\n%s", html)
	}

	// Age past the min-visible window, then a General message must not end
	// Theory typing; a Theory message must.
	bot.typingMu.Lock()
	ty := bot.typing[typingKey{42, theory}]
	ty.since = ty.since.Add(-2 * typingMinVisible)
	bot.typing[typingKey{42, theory}] = ty
	bot.typingMu.Unlock()
	doTypingTestPOST(t, srv, "/bot"+token+"/sendMessage", `{"chat_id":42,"text":"general"}`)
	if len(bot.ActiveTyping(42)) != 1 {
		t.Fatal("a General message must not end Theory typing")
	}
	doTypingTestPOST(t, srv, "/bot"+token+"/sendMessage",
		fmt.Sprintf(`{"chat_id":42,"message_thread_id":%d,"text":"theory"}`, theory))
	if len(bot.ActiveTyping(42)) != 0 {
		t.Fatal("a Theory message must end Theory typing")
	}
}

func TestChatActionWithoutTopicsHasNoLabel(t *testing.T) {
	const token = "741:plain-typing"
	srv := New(Config{Quiet: true})
	doTypingTestPOST(t, srv, "/bot"+token+"/sendChatAction", `{"chat_id":42,"action":"typing"}`)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/debug/chat/"+token+"/42/messages", nil))
	html := rec.Body.String()
	if !strings.Contains(html, `aria-live="polite">typing…</div>`) || strings.Contains(html, "topic-label") {
		t.Fatalf("chat without topics: plain typing bubble, no topic label:\n%s", html)
	}
}
