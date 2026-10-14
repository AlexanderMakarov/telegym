package mock

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestMockURL covers the bind-address normalization used by the startup
// card. `:5678` and `0.0.0.0:5678` are the two forms users hit in
// practice (default flag value and docker-compose), both must collapse
// to a clickable `http://localhost:5678`.
func TestMockURL(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{":5678", "http://localhost:5678"},
		{":80", "http://localhost:80"},
		{"0.0.0.0:5678", "http://localhost:5678"},
		{"0.0.0.0:9104", "http://localhost:9104"},
		{"127.0.0.1:5678", "http://127.0.0.1:5678"},
		{"example.com:8080", "http://example.com:8080"},
	}
	for _, c := range cases {
		if got := mockURL(c.in); got != c.want {
			t.Errorf("mockURL(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestParseBotPath exercises the URL parser the NoRoute catch-all uses to
// resolve `/bot<token>/[test/]<method>` requests. Failure modes here mean
// the mock returns a generic 404 to clients that would otherwise hit the
// spec-driven dispatcher, so the parser's negative cases matter.
func TestParseBotPath(t *testing.T) {
	type result struct {
		token, method string
		ok            bool
	}
	cases := []struct {
		path string
		want result
	}{
		{"/bot123:abc/sendMessage", result{"123:abc", "sendMessage", true}},
		{"/bot123:abc/test/sendMessage", result{"123:abc", "sendMessage", true}},
		{"/bot42:xyz/getChat", result{"42:xyz", "getChat", true}},
		// Edge cases that must NOT parse:
		{"/bot123:abc", result{"", "", false}},       // no method
		{"/bot/sendMessage", result{"", "", false}},  // empty token segment
		{"/bot123:abc/", result{"", "", false}},      // trailing slash, no method
		{"/bot123:abc/test/", result{"", "", false}}, // /test/ with no method
	}
	for _, c := range cases {
		token, method, ok := parseBotPath(c.path)
		if token != c.want.token || method != c.want.method || ok != c.want.ok {
			t.Errorf("parseBotPath(%q) = (%q, %q, %v), want (%q, %q, %v)",
				c.path, token, method, ok, c.want.token, c.want.method, c.want.ok)
		}
	}
}

// TestHealthEndpoint is an end-to-end smoke that the gin router answers
// /health with the expected JSON. Useful as a regression canary -- if
// routes get accidentally reshuffled, this is the first thing that
// breaks.
func TestHealthEndpoint(t *testing.T) {
	srv := New(Config{Quiet: true})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON: %v -- body=%s", err, rec.Body.String())
	}
	if body["status"] != "ok" {
		t.Errorf("body[status] = %q, want %q", body["status"], "ok")
	}
}

// TestGetMeEndpoint is the cheapest happy-path through the real Bot API
// surface: the bot auto-registers on first hit, GetMe returns a stable
// identity. If this breaks, every load-test scenario breaks too.
func TestGetMeEndpoint(t *testing.T) {
	srv := New(Config{Quiet: true})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/bot42:test_token/getMe", nil)
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var resp APIResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid JSON: %v -- body=%s", err, rec.Body.String())
	}
	if !resp.OK {
		t.Errorf("APIResponse.OK = false, want true; description=%q", resp.Description)
	}
	if resp.Result == nil {
		t.Error("APIResponse.Result is nil, want non-empty User identity")
	}
}

// TestGetMeUnderTestPrefix confirms the /test/ prefix variant (used by
// clients with WithTestServerPath like telego) routes to the same
// handler.
func TestGetMeUnderTestPrefix(t *testing.T) {
	srv := New(Config{Quiet: true})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/bot42:test_token/test/getMe", nil)
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"ok":true`) {
		t.Errorf("expected ok:true in body, got %s", rec.Body.String())
	}
}

// TestSendMessageRoundtrip is the #1 happy path every k6 scenario depends
// on: bot calls sendMessage, the mock persists it, /debug/messages
// returns it. If this regresses every scenario silently breaks, since
// awaits poll /debug/messages until a match is found.
func TestSendMessageRoundtrip(t *testing.T) {
	srv := New(Config{Quiet: true})
	const token = "555:roundtrip_token"
	const chatID = 42

	// 1) bot sends a message
	body := bytes.NewBufferString(`{"chat_id":42,"text":"hello from bot"}`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/bot"+token+"/sendMessage", body)
	req.Header.Set("Content-Type", "application/json")
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("sendMessage status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var sendResp struct {
		OK     bool `json:"ok"`
		Result struct {
			MessageID int    `json:"message_id"`
			Text      string `json:"text"`
		} `json:"result"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &sendResp); err != nil {
		t.Fatalf("decode sendMessage response: %v", err)
	}
	if !sendResp.OK || sendResp.Result.MessageID == 0 || sendResp.Result.Text != "hello from bot" {
		t.Fatalf("unexpected sendMessage response: %+v", sendResp)
	}

	// 2) /debug/messages must surface the same message
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/debug/messages/"+token+"?chat_id=42", nil)
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("debug/messages status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	// Response shape: {"messages": [ {message_id, text, ...}, ... ]}
	var listResp struct {
		Messages []struct {
			MessageID int    `json:"message_id"`
			Text      string `json:"text"`
			Chat      struct {
				ID int64 `json:"id"`
			} `json:"chat"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("decode debug/messages response: %v -- body=%s", err, rec.Body.String())
	}
	if len(listResp.Messages) != 1 {
		t.Fatalf("debug/messages returned %d entries, want 1: %s", len(listResp.Messages), rec.Body.String())
	}
	got := listResp.Messages[0]
	if got.Text != "hello from bot" || got.Chat.ID != chatID || got.MessageID != sendResp.Result.MessageID {
		t.Errorf("debug/messages entry mismatch: got %+v, want text=%q chat_id=%d message_id=%d",
			got, "hello from bot", chatID, sendResp.Result.MessageID)
	}
}

// TestInjectUpdateFiresWebhook is the OTHER half of the roundtrip: k6
// injects an update via /debug/inject/update, the mock POSTs it to the
// bot's webhook (here a httptest receiver). Every load-test scenario
// stands or falls on this path -- if the webhook never fires, the bot
// under test never receives messages and the run is silently a no-op.
func TestInjectUpdateFiresWebhook(t *testing.T) {
	const token = "777:webhook_token"

	// Receiver captures the dispatched update body. Buffered so the
	// webhook dispatch goroutine doesn't block if the test is slow.
	received := make(chan []byte, 1)
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		select {
		case received <- body:
		default:
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer receiver.Close()

	// DefaultWebhookURL applies to any bot that hasn't called setWebhook.
	srv := New(Config{Quiet: true, DefaultWebhookURL: receiver.URL})

	// Inject a message-shaped update.
	payload := `{"token":"` + token + `","chat_id":99,"user_id":99,"first_name":"Test","text":"ping"}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/debug/inject/update", bytes.NewBufferString(payload))
	req.Header.Set("Content-Type", "application/json")
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("inject status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	// Wait for the dispatch goroutine to deliver. 2s is generous --
	// local httptest dispatch is usually sub-millisecond.
	select {
	case body := <-received:
		if !bytes.Contains(body, []byte(`"ping"`)) {
			t.Errorf("webhook body missing injected text %q: %s", "ping", string(body))
		}
		if !bytes.Contains(body, []byte(`"chat":{"id":99`)) && !bytes.Contains(body, []byte(`"chat_id":99`)) {
			t.Errorf("webhook body missing chat_id 99: %s", string(body))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("webhook never fired within 2s")
	}

	// Same path, callback-shaped. callback_query.message must carry a
	// `from`: on real Telegram that message was sent by the bot, and bots
	// dereference message.From without a nil check. A stub without it
	// panics the bot under test, and the k6 scenario just times out
	// waiting for a reply that never comes.
	payload = `{"token":"` + token + `","chat_id":99,"callback_data":"age_verify","message_id":7}`
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/debug/inject/update", bytes.NewBufferString(payload))
	req.Header.Set("Content-Type", "application/json")
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("inject callback status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	select {
	case body := <-received:
		var upd Update
		if err := json.Unmarshal(body, &upd); err != nil {
			t.Fatalf("decode webhook body: %v -- body=%s", err, string(body))
		}
		msg := upd.CallbackQuery.Message
		if msg == nil || msg.From == nil || !msg.From.IsBot {
			t.Errorf("callback_query.message.from = %+v, want the bot identity", msg)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("callback webhook never fired within 2s")
	}
}

func TestCreateForumTopic_positiveThreadID(t *testing.T) {
	srv := New(Config{Quiet: true})
	token := "123:test"
	body := `{"chat_id":42,"name":"Theory"}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/bot"+token+"/createForumTopic", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var env APIResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(env.Result)
	var topic ForumTopic
	if err := json.Unmarshal(raw, &topic); err != nil {
		t.Fatal(err)
	}
	if topic.MessageThreadID == 0 || topic.Name != "Theory" {
		t.Fatalf("want positive Theory thread, got %+v", topic)
	}
	// Idempotent same name
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodPost, "/bot"+token+"/createForumTopic", strings.NewReader(body))
	req2.Header.Set("Content-Type", "application/json")
	srv.Handler().ServeHTTP(rec2, req2)
	var env2 APIResponse
	_ = json.Unmarshal(rec2.Body.Bytes(), &env2)
	raw2, _ := json.Marshal(env2.Result)
	var topic2 ForumTopic
	_ = json.Unmarshal(raw2, &topic2)
	if topic2.MessageThreadID != topic.MessageThreadID {
		t.Fatalf("reuse thread id: got %d want %d", topic2.MessageThreadID, topic.MessageThreadID)
	}
}

func TestSendChatAction_setsTypingClearedOnSend(t *testing.T) {
	srv := New(Config{Quiet: true})
	token := "123:test"
	bot := srv.Store().Bot(token)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/bot"+token+"/sendChatAction",
		strings.NewReader(`{"chat_id":7,"action":"typing"}`))
	req.Header.Set("Content-Type", "application/json")
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("sendChatAction status=%d body=%s", rec.Code, rec.Body.String())
	}
	if _, ok := bot.ActiveTyping(7, 0); !ok {
		t.Fatal("expected active typing after sendChatAction")
	}

	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodPost, "/bot"+token+"/sendMessage",
		strings.NewReader(`{"chat_id":7,"text":"hello"}`))
	req2.Header.Set("Content-Type", "application/json")
	srv.Handler().ServeHTTP(rec2, req2)
	// Sticky window keeps “…typing” visible briefly so HTMX polls can see it.
	if _, ok := bot.ActiveTyping(7, 0); !ok {
		t.Fatal("typing should remain during sticky min-visible window after sendMessage")
	}
	time.Sleep(time.Duration(typingMinVisibleSec+1) * time.Second)
	if _, ok := bot.ActiveTyping(7, 0); ok {
		t.Fatal("typing must clear after sticky window")
	}
}

func TestSendChatAction_messagesHTMLShowsTypingBubble(t *testing.T) {
	srv := New(Config{Quiet: true})
	token := "123:typing-html"
	chatID := int64(99)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/bot"+token+"/sendChatAction",
		strings.NewReader(`{"chat_id":99,"action":"typing"}`))
	req.Header.Set("Content-Type", "application/json")
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("sendChatAction status=%d body=%s", rec.Code, rec.Body.String())
	}

	recMsg := httptest.NewRecorder()
	reqMsg := httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/debug/chat/%s/%d/messages?name=test", token, chatID), nil)
	srv.Handler().ServeHTTP(recMsg, reqMsg)
	if recMsg.Code != http.StatusOK {
		t.Fatalf("messages status=%d body=%s", recMsg.Code, recMsg.Body.String())
	}
	body := recMsg.Body.String()
	if !strings.Contains(body, "…typing") && !strings.Contains(body, "typing-dots") {
		t.Fatalf("messages HTML must include typing bubble (…typing or typing-dots), got %s", body)
	}
}

func TestDeleteMessage_removesFromChatHTML(t *testing.T) {
	srv := New(Config{Quiet: true})
	token := "123:delete"
	bot := srv.Store().Bot(token)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/bot"+token+"/sendMessage",
		strings.NewReader(`{"chat_id":42,"text":"<i>temporal</i>","reply_markup":{"inline_keyboard":[[{"text":"Cancel","callback_data":"settings:cancel"}]]}}`))
	req.Header.Set("Content-Type", "application/json")
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("sendMessage status=%d body=%s", rec.Code, rec.Body.String())
	}
	var env APIResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &env)
	raw, _ := json.Marshal(env.Result)
	var sent Message
	_ = json.Unmarshal(raw, &sent)
	if sent.MessageID == 0 {
		t.Fatal("expected message_id")
	}
	if n := len(bot.Messages(42, 10)); n != 1 {
		t.Fatalf("stored msgs=%d want 1", n)
	}

	recDel := httptest.NewRecorder()
	delBody := fmt.Sprintf(`{"chat_id":42,"message_id":%d}`, sent.MessageID)
	reqDel := httptest.NewRequest(http.MethodPost, "/bot"+token+"/deleteMessage", strings.NewReader(delBody))
	reqDel.Header.Set("Content-Type", "application/json")
	srv.Handler().ServeHTTP(recDel, reqDel)
	if recDel.Code != http.StatusOK {
		t.Fatalf("deleteMessage status=%d body=%s", recDel.Code, recDel.Body.String())
	}
	if n := len(bot.Messages(42, 10)); n != 0 {
		t.Fatalf("after delete stored msgs=%d want 0", n)
	}

	recMsg := httptest.NewRecorder()
	reqMsg := httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/debug/chat/%s/42/messages?name=test", token), nil)
	srv.Handler().ServeHTTP(recMsg, reqMsg)
	if strings.Contains(recMsg.Body.String(), "temporal") || strings.Contains(recMsg.Body.String(), "Cancel") {
		t.Fatalf("deleted chrome must not remain in messages HTML: %s", recMsg.Body.String())
	}
}

func TestCreateForumTopic_serviceMessageInChat(t *testing.T) {
	srv := New(Config{Quiet: true})
	token := "123:topic-svc"
	body := `{"chat_id":55,"name":"Theory"}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/bot"+token+"/createForumTopic", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var env APIResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &env)
	raw, _ := json.Marshal(env.Result)
	var topic ForumTopic
	_ = json.Unmarshal(raw, &topic)

	recMsg := httptest.NewRecorder()
	reqMsg := httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/debug/chat/%s/55/messages?name=test", token), nil)
	srv.Handler().ServeHTTP(recMsg, reqMsg)
	htmlBody := recMsg.Body.String()
	if !strings.Contains(htmlBody, "was created") || !strings.Contains(htmlBody, "topic-link") {
		t.Fatalf("want topic-created service row, got %s", htmlBody)
	}
	if !strings.Contains(htmlBody, fmt.Sprintf("thread=%d", topic.MessageThreadID)) {
		t.Fatalf("topic link must target thread %d: %s", topic.MessageThreadID, htmlBody)
	}
}
