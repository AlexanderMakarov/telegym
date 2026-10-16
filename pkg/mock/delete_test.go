package mock

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func doDeleteTestPOST(t *testing.T, srv *Server, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

func TestDeleteMessage(t *testing.T) {
	const token = "741:delete"
	srv := New(Config{Quiet: true})
	bot := srv.Store().Bot(token)

	rec := doDeleteTestPOST(t, srv, "/bot"+token+"/sendMessage",
		`{"chat_id":42,"text":"temporal","reply_markup":{"inline_keyboard":[[{"text":"Cancel","callback_data":"cancel"}]]}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("sendMessage status=%d body=%s", rec.Code, rec.Body.String())
	}
	var env struct {
		Result Message `json:"result"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	sent := env.Result
	// An edit is stored as a second entry with the same message_id.
	rec = doDeleteTestPOST(t, srv, "/bot"+token+"/editMessageText",
		fmt.Sprintf(`{"chat_id":42,"message_id":%d,"text":"temporal edited"}`, sent.MessageID))
	if rec.Code != http.StatusOK {
		t.Fatalf("editMessageText status=%d body=%s", rec.Code, rec.Body.String())
	}
	keep := doDeleteTestPOST(t, srv, "/bot"+token+"/sendMessage", `{"chat_id":42,"text":"keep me"}`)
	if keep.Code != http.StatusOK {
		t.Fatalf("sendMessage status=%d", keep.Code)
	}

	tests := []struct {
		name   string
		body   string
		status int
	}{
		{"stored message", fmt.Sprintf(`{"chat_id":42,"message_id":%d}`, sent.MessageID), http.StatusOK},
		{"not in store (user message or evicted)", `{"chat_id":42,"message_id":999999}`, http.StatusOK},
		{"unknown chat", `{"chat_id":7,"message_id":1}`, http.StatusOK},
		{"missing message_id", `{"chat_id":42}`, http.StatusBadRequest},
		{"malformed body", `{"chat_id":`, http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := doDeleteTestPOST(t, srv, "/bot"+token+"/deleteMessage", tt.body)
			if rec.Code != tt.status {
				t.Fatalf("status=%d want %d body=%s", rec.Code, tt.status, rec.Body.String())
			}
			if tt.status == http.StatusOK && !strings.Contains(rec.Body.String(), `"result":true`) {
				t.Fatalf("want result=true, got %s", rec.Body.String())
			}
		})
	}

	msgs := bot.Messages(42, 10)
	if len(msgs) != 1 || msgs[0].Text != "keep me" {
		t.Fatalf("want only the untouched message left, got %+v", msgs)
	}

	chat := httptest.NewRecorder()
	srv.Handler().ServeHTTP(chat, httptest.NewRequest(http.MethodGet, "/debug/chat/"+token+"/42/messages", nil))
	if body := chat.Body.String(); strings.Contains(body, "temporal") || strings.Contains(body, "Cancel") {
		t.Fatalf("deleted message must not render in debug chat: %s", body)
	}
}
