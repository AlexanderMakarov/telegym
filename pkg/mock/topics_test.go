package mock

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func doTopicsTestPOST(t *testing.T, srv *Server, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

func createTestTopic(t *testing.T, srv *Server, token, body string) ForumTopic {
	t.Helper()
	rec := doTopicsTestPOST(t, srv, "/bot"+token+"/createForumTopic", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("createForumTopic status=%d body=%s", rec.Code, rec.Body.String())
	}
	var env struct {
		Result ForumTopic `json:"result"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	return env.Result
}

func topicsTestChatHTML(t *testing.T, srv *Server, path string) string {
	t.Helper()
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s status=%d body=%s", path, rec.Code, rec.Body.String())
	}
	return rec.Body.String()
}

func TestCreateForumTopicValidation(t *testing.T) {
	tests := []struct {
		name   string
		body   string
		status int
	}{
		{"default icon", `{"chat_id":42,"name":"Theory"}`, http.StatusOK},
		{"allowed icon color", `{"chat_id":42,"name":"Theory","icon_color":16766590}`, http.StatusOK},
		{"unsupported icon color", `{"chat_id":42,"name":"Theory","icon_color":1}`, http.StatusBadRequest},
		{"empty name", `{"chat_id":42,"name":""}`, http.StatusBadRequest},
		{"name too long", fmt.Sprintf(`{"chat_id":42,"name":%q}`, strings.Repeat("ա", 129)), http.StatusBadRequest},
		{"missing chat_id", `{"name":"Theory"}`, http.StatusBadRequest},
		{"malformed body", `{"chat_id":`, http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := New(Config{Quiet: true})
			rec := doTopicsTestPOST(t, srv, "/bot741:topics/createForumTopic", tt.body)
			if rec.Code != tt.status {
				t.Fatalf("status=%d want %d body=%s", rec.Code, tt.status, rec.Body.String())
			}
		})
	}
}

func TestCreateForumTopicCreatesNewTopicEachCall(t *testing.T) {
	const token = "741:topics"
	srv := New(Config{Quiet: true})
	first := createTestTopic(t, srv, token, `{"chat_id":42,"name":"Theory"}`)
	second := createTestTopic(t, srv, token, `{"chat_id":42,"name":"Theory"}`)
	if first.MessageThreadID <= 0 || second.MessageThreadID <= 0 || first.MessageThreadID == second.MessageThreadID {
		t.Fatalf("want two distinct positive thread ids, got %d and %d", first.MessageThreadID, second.MessageThreadID)
	}
	if first.Name != "Theory" || first.IconColor != 0x6FB9F0 {
		t.Fatalf("unexpected topic %+v", first)
	}

	// The topic opens with a forum_topic_created service message whose
	// message_id is the thread id, like Telegram.
	var created []Message
	for _, m := range srv.Store().Bot(token).Messages(42, 10) {
		if m.ForumTopicCreated != nil {
			created = append(created, m)
		}
	}
	if len(created) != 2 {
		t.Fatalf("want 2 service messages, got %+v", created)
	}
	for _, m := range created {
		if m.MessageID != m.MessageThreadID || !m.IsTopicMessage || m.ForumTopicCreated.Name != "Theory" {
			t.Fatalf("bad service message %+v", m)
		}
	}
}

func TestCreateForumTopicConcurrent(t *testing.T) {
	const token = "741:topics"
	srv := New(Config{Quiet: true})
	const n = 20
	ids := make(chan int, n)
	var wg sync.WaitGroup
	for range n {
		wg.Go(func() {
			ids <- createTestTopic(t, srv, token, `{"chat_id":42,"name":"Same"}`).MessageThreadID
		})
	}
	wg.Wait()
	close(ids)
	seen := map[int]bool{}
	for id := range ids {
		if seen[id] {
			t.Fatalf("duplicate thread id %d", id)
		}
		seen[id] = true
	}
	if got := len(srv.Store().Bot(token).ForumTopics(42)); got != n {
		t.Fatalf("stored topics=%d want %d", got, n)
	}
}

func TestSendMessageToTopic(t *testing.T) {
	const token = "741:topics"
	srv := New(Config{Quiet: true})
	topic := createTestTopic(t, srv, token, `{"chat_id":42,"name":"Theory"}`)
	rec := doTopicsTestPOST(t, srv, "/bot"+token+"/sendMessage",
		fmt.Sprintf(`{"chat_id":42,"message_thread_id":%d,"text":"lesson"}`, topic.MessageThreadID))
	if rec.Code != http.StatusOK {
		t.Fatalf("sendMessage status=%d", rec.Code)
	}
	var env struct {
		Result Message `json:"result"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if env.Result.MessageThreadID != topic.MessageThreadID || !env.Result.IsTopicMessage {
		t.Fatalf("result must carry the topic, got %+v", env.Result)
	}
}

func TestDebugChatTopicTabsAndFilter(t *testing.T) {
	const token = "741:topics"
	srv := New(Config{Quiet: true})
	base := "/debug/chat/" + token + "/42/messages?name=ann"

	doTopicsTestPOST(t, srv, "/bot"+token+"/sendMessage", `{"chat_id":42,"text":"general hello"}`)
	if html := topicsTestChatHTML(t, srv, base); !strings.Contains(html, `<nav id="topic-tabs" hx-swap-oob="true"></nav>`) || strings.Contains(html, "topic-label") {
		t.Fatalf("chat without topics must render no topic chrome: %s", html)
	}

	topic := createTestTopic(t, srv, token, `{"chat_id":42,"name":"Theory & <Practice>"}`)
	tid := topic.MessageThreadID
	rec := doTopicsTestPOST(t, srv, "/bot"+token+"/sendMessage",
		fmt.Sprintf(`{"chat_id":42,"message_thread_id":%d,"text":"topic lesson"}`, tid))
	var env struct {
		Result Message `json:"result"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &env)
	// Edits carry no thread id but must stay in the topic.
	doTopicsTestPOST(t, srv, "/bot"+token+"/editMessageText",
		fmt.Sprintf(`{"chat_id":42,"message_id":%d,"text":"topic lesson v2"}`, env.Result.MessageID))

	all := topicsTestChatHTML(t, srv, base)
	for _, want := range []string{
		`>All</a>`, `>General</a>`, `Theory &amp; &lt;Practice&gt;</a>`,
		fmt.Sprintf(`thread=%d`, tid), "general hello", "topic lesson v2", "was created",
		`<span class="topic-label">General</span>`,
	} {
		if !strings.Contains(all, want) {
			t.Fatalf("All view missing %q:\n%s", want, all)
		}
	}

	inTopic := topicsTestChatHTML(t, srv, fmt.Sprintf("%s&thread=%d", base, tid))
	if !strings.Contains(inTopic, "topic lesson v2") || strings.Contains(inTopic, "general hello") {
		t.Fatalf("topic view must show only topic messages:\n%s", inTopic)
	}
	general := topicsTestChatHTML(t, srv, base+"&thread=0")
	if !strings.Contains(general, "general hello") || strings.Contains(general, "topic lesson") {
		t.Fatalf("General view must show only General messages:\n%s", general)
	}
}

func TestDebugChatTopicLinksEscapeName(t *testing.T) {
	const token = "741:topics"
	srv := New(Config{Quiet: true})
	createTestTopic(t, srv, token, `{"chat_id":42,"name":"Theory"}`)
	html := topicsTestChatHTML(t, srv, "/debug/chat/"+token+"/42/messages?name=a%26b%3Dc")
	if !strings.Contains(html, "name=a%26b%3Dc") {
		t.Fatalf("user name must be query-escaped in topic links:\n%s", html)
	}
	page := topicsTestChatHTML(t, srv, "/debug/chat/"+token+"/42?name=a%26b&thread=3")
	if !strings.Contains(page, "/messages?name=a%26b&amp;thread=3") {
		t.Fatalf("poll URL must keep name and thread:\n%s", page)
	}
}
