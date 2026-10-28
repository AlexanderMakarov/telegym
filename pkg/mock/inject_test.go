package mock

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func TestInjectUpdateDispatchObservability(t *testing.T) {
	for _, webhook := range []bool{false, true} {
		result := "queued"
		if webhook {
			result = "ok"
		}

		t.Run(result, func(t *testing.T) {
			cfg := Config{Quiet: true}
			wantStatus := 0
			if webhook {
				receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusOK)
				}))
				defer receiver.Close()
				cfg.DefaultWebhookURL = receiver.URL
				wantStatus = http.StatusOK
			}

			srv := New(cfg)
			if !webhook {
				startPolling(t, srv, "889:inject")
			}
			rec := pollingRequest(srv, http.MethodPost, "/debug/inject/update", `{"token":"889:inject","chat_id":42,"text":"hello"}`)
			var response struct {
				OK             bool  `json:"ok"`
				Queued         *bool `json:"queued"`
				DeliveryStatus int   `json:"delivery_status"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
				t.Fatalf("decode inject response: %v", err)
			}
			if rec.Code != http.StatusOK || !response.OK || response.DeliveryStatus != wantStatus {
				t.Fatalf("unexpected inject response: status=%d body=%s", rec.Code, rec.Body.String())
			}
			if response.Queued == nil || *response.Queued != !webhook {
				t.Fatalf("expected queued=%t: %s", !webhook, rec.Body.String())
			}

			rec = httptest.NewRecorder()
			srv.metrics.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
			wantMetric := `telegym_mock_webhook_dispatches_total{result="` + result + `"} 1`
			if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), wantMetric) {
				t.Fatalf("expected metric %s: status=%d body=%s", wantMetric, rec.Code, rec.Body.String())
			}
			if !webhook && !strings.Contains(rec.Body.String(), "telegym_mock_webhook_dispatch_duration_seconds_count 0") {
				t.Fatal("queueing was counted as webhook delivery latency")
			}
		})
	}
}

// injectWebhook returns the update delivered by the injection endpoint.
func injectWebhook(t *testing.T, payload string, wantStatus int) *Update {
	t.Helper()
	received := make(chan []byte, 1)
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		received <- body
		w.WriteHeader(http.StatusOK)
	}))
	defer receiver.Close()

	srv := New(Config{Quiet: true, DefaultWebhookURL: receiver.URL})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/debug/inject/update", bytes.NewBufferString(payload))
	req.Header.Set("Content-Type", "application/json")
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != wantStatus {
		t.Fatalf("inject status=%d body=%s, want %d", rec.Code, rec.Body.String(), wantStatus)
	}

	// Delivery completes before the injection response is returned.
	select {
	case body := <-received:
		if wantStatus != http.StatusOK {
			t.Fatalf("rejected update reached webhook: %s", body)
		}
		var update Update
		if err := json.Unmarshal(body, &update); err != nil {
			t.Fatalf("decode webhook update: %v", err)
		}
		if update.UpdateID == 0 {
			t.Fatalf("webhook update lacks ID: %s", body)
		}
		return &update
	default:
		if wantStatus == http.StatusOK {
			t.Fatal("successful injection did not deliver a webhook update")
		}
		return nil
	}
}

func TestInjectPrivateMessageCompatibility(t *testing.T) {
	update := injectWebhook(t, `{"token":"889:inject","chat_id":42,"user_id":7,"first_name":"Alice","username":"alice","text":"hello"}`, http.StatusOK)
	message := update.Message
	if message == nil || update.EditedMessage != nil || update.CallbackQuery != nil || update.ChatMember != nil {
		t.Fatalf("unexpected update type: %+v", update)
	}
	if message.MessageID == 0 || message.Text != "hello" || message.From == nil || message.From.ID != 7 ||
		message.From.FirstName != "Alice" || message.From.Username != "alice" || message.From.IsBot {
		t.Fatalf("unexpected private message: %+v", message)
	}
	if message.Chat.ID != 42 || message.Chat.Type != "private" || message.Chat.FirstName != "Alice" || message.Chat.Username != "alice" {
		t.Fatalf("private chat identity changed: %+v", message.Chat)
	}
}

func TestInjectGroupMessages(t *testing.T) {
	for _, kind := range []string{"message", "edited_message"} {
		t.Run(kind, func(t *testing.T) {
			payload := `{
				"token":"889:inject", "type":"` + kind + `",
				"chat_id":-10042, "chat_type":"supergroup",
				"chat_title":"Test group", "user_id":7,
				"first_name":"Alice", "message_id":123, "text":"hello"
			}`
			update := injectWebhook(t, payload, http.StatusOK)
			message := update.Message
			if kind == "edited_message" {
				message = update.EditedMessage
				if update.Message != nil {
					t.Fatalf("edited update also contains a message: %+v", update)
				}
			}
			if message == nil || message.MessageID == 0 || message.Text != "hello" || message.Date == 0 ||
				message.From == nil || message.From.ID != 7 || message.From.IsBot {
				t.Fatalf("unexpected group message: %+v", message)
			}
			if kind == "edited_message" && message.MessageID != 123 {
				t.Fatalf("edit message ID=%d, want 123", message.MessageID)
			}
			if kind == "message" && message.MessageID == 123 {
				t.Fatal("ordinary message used the supplied ID instead of generating one")
			}
			if message.Chat.ID != -10042 || message.Chat.Type != "supergroup" || message.Chat.Title != "Test group" ||
				message.Chat.FirstName != "" || message.Chat.Username != "" {
				t.Fatalf("unexpected group chat: %+v", message.Chat)
			}
		})
	}
}

func TestInjectGroupCallback(t *testing.T) {
	for _, kind := range []string{"", "callback_query"} {
		t.Run("type="+kind, func(t *testing.T) {
			update := injectWebhook(t, `{"token":"889:inject","type":"`+kind+`","chat_id":-42,"chat_type":"group","chat_title":"Test group","user_id":7,"message_id":123,"callback_data":"next"}`, http.StatusOK)
			callback := update.CallbackQuery
			if callback == nil || callback.Data != "next" || callback.From.ID != 7 || callback.ID == "" || update.Message != nil {
				t.Fatalf("unexpected callback update: %+v", update)
			}
			message := callback.Message
			if message == nil || message.MessageID != 123 || message.From == nil || !message.From.IsBot ||
				message.Chat.ID != -42 || message.Chat.Type != "group" || message.Chat.Title != "Test group" {
				t.Fatalf("unexpected callback message: %+v", message)
			}
		})
	}
}

func TestInjectChatMember(t *testing.T) {
	for _, actorID := range []int64{0, 7, 9} {
		t.Run("actor="+strconv.FormatInt(actorID, 10), func(t *testing.T) {
			update := injectWebhook(t, `{"token":"889:inject","type":"chat_member","chat_id":-42,"chat_type":"supergroup","chat_title":"Test group","user_id":7,"first_name":"Alice","actor_id":`+strconv.FormatInt(actorID, 10)+`,"old_status":"left","new_status":"member"}`, http.StatusOK)
			member := update.ChatMember
			if member == nil || update.Message != nil || update.CallbackQuery != nil || update.EditedMessage != nil {
				t.Fatalf("unexpected member update: %+v", update)
			}
			if member.Chat.ID != -42 || member.Chat.Type != "supergroup" || member.Chat.Title != "Test group" || member.Date == 0 ||
				member.OldChatMember.Status != "left" || member.NewChatMember.Status != "member" ||
				member.OldChatMember.User.ID != 7 || member.NewChatMember.User.ID != 7 || member.NewChatMember.User.FirstName != "Alice" {
				t.Fatalf("unexpected member transition: %+v", member)
			}
			wantActor := actorID
			if wantActor == 0 {
				wantActor = 7
			}
			if member.From.ID != wantActor || member.From.FirstName == "" || (wantActor == 7 && member.From.FirstName != "Alice") {
				t.Fatalf("unexpected member actor: %+v", member.From)
			}
		})
	}
}

func TestInjectRejectsUnsupportedUpdates(t *testing.T) {
	tests := []struct {
		name   string
		fields string
	}{
		{"unknown type", `"type":"unknown"`},
		{"missing old status", `"type":"chat_member","new_status":"member"`},
		{"missing new status", `"type":"chat_member","old_status":"left"`},
		{"unsupported old status", `"type":"chat_member","old_status":"administrator","new_status":"member"`},
		{"unsupported new status", `"type":"chat_member","old_status":"member","new_status":"restricted"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			injectWebhook(t, `{"token":"889:inject","chat_id":-42,"user_id":7,`+tt.fields+`}`, http.StatusBadRequest)
		})
	}
}
