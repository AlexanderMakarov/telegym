package mock

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"testing/iotest"
	"time"
)

func journalRequest(srv *Server, method, path, contentType string, body io.Reader) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, body)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

func readJournal(t *testing.T, srv *Server, token, query string) []APIRequest {
	t.Helper()
	rec := journalRequest(srv, http.MethodGet, "/debug/requests/"+token+query, "", nil)
	var response struct {
		Count    int          `json:"count"`
		Requests []APIRequest `json:"requests"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode journal: %v", err)
	}
	if rec.Code != http.StatusOK || response.Requests == nil || response.Count != len(response.Requests) {
		t.Fatalf("unexpected journal response: status=%d body=%s", rec.Code, rec.Body.String())
	}
	return response.Requests
}

func TestRequestJournalCapturesParametersAndStatus(t *testing.T) {
	tests := []struct {
		name, method, path, contentType, body string
		status                                int
		parameters                            map[string]any
	}{
		{
			name: "explicit JSON", method: http.MethodPost, path: "sendMessage?text=query",
			contentType: "application/json", body: `{"chat_id":42,"text":"hello"}`,
			status: http.StatusOK, parameters: map[string]any{"chat_id": float64(42), "text": "hello"},
		},
		{
			name: "generic test path", method: http.MethodPost, path: "test/banChatMember",
			contentType: "application/json", body: `{"chat_id":-42,"user_id":7}`,
			status: http.StatusOK, parameters: map[string]any{"chat_id": float64(-42), "user_id": float64(7)},
		},
		{
			name: "query", method: http.MethodGet, path: "getChatAdministrators?chat_id=-42&tag=a&tag=b",
			status: http.StatusOK, parameters: map[string]any{"chat_id": "-42", "tag": []any{"a", "b"}},
		},
		{
			name: "generic form", method: http.MethodPost, path: "banChatMember?user_id=1",
			contentType: "application/x-www-form-urlencoded", body: "chat_id=-42&user_id=7",
			status: http.StatusOK, parameters: map[string]any{"chat_id": "-42", "user_id": "7"},
		},
		{
			name: "explicit form", method: http.MethodPost, path: "deleteMessage",
			contentType: "application/x-www-form-urlencoded", body: "chat_id=42&message_id=123",
			status: http.StatusOK, parameters: map[string]any{"chat_id": "42", "message_id": "123"},
		},
		{
			name: "invalid JSON", method: http.MethodPost, path: "sendMessage?chat_id=42",
			contentType: "application/json", body: "{",
			status: http.StatusBadRequest, parameters: map[string]any{"chat_id": "42"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := New(Config{Quiet: true})
			before := time.Now().Unix()
			rec := journalRequest(srv, tt.method, "/bot901:journal/"+tt.path, tt.contentType, strings.NewReader(tt.body))
			if rec.Code != tt.status {
				t.Fatalf("request status=%d body=%s, want %d", rec.Code, rec.Body.String(), tt.status)
			}
			requests := readJournal(t, srv, "901:journal", "")
			if len(requests) != 1 {
				t.Fatalf("expected one request, got %+v", requests)
			}
			got := requests[0]
			method := strings.TrimPrefix(strings.SplitN(tt.path, "?", 2)[0], "test/")
			if got.Seq <= 0 || got.Method != method || got.Status != tt.status || got.At < before || got.At > time.Now().Unix() ||
				!reflect.DeepEqual(got.Parameters, tt.parameters) {
				t.Fatalf("unexpected journal entry: %+v, want parameters=%v status=%d", got, tt.parameters, tt.status)
			}
		})
	}
}

func TestRequestJournalPreservesMultipartUploads(t *testing.T) {
	srv := New(Config{Quiet: true})
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	if err := w.WriteField("chat_id", "42"); err != nil {
		t.Fatal(err)
	}
	part, err := w.CreateFormFile("photo", "photo.jpg")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte("uploaded bytes")); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	rec := journalRequest(srv, http.MethodPost, "/bot901:journal/sendPhoto", w.FormDataContentType(), &body)
	var response struct {
		Result Message `json:"result"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusOK || len(response.Result.Photo) == 0 {
		t.Fatalf("multipart upload status=%d body=%s", rec.Code, rec.Body.String())
	}
	file := journalRequest(srv, http.MethodGet, "/debug/files/"+response.Result.Photo[0].FileID, "", nil)
	if file.Code != http.StatusOK || file.Body.String() != "uploaded bytes" {
		t.Fatalf("captured file status=%d body=%s", file.Code, file.Body.String())
	}
	requests := readJournal(t, srv, "901:journal", "")
	if len(requests) != 1 || !reflect.DeepEqual(requests[0].Parameters, map[string]any{"chat_id": "42"}) {
		t.Fatalf("multipart journal must contain fields without file bytes: %+v", requests)
	}
}

func TestRequestJournalFiltersIsolatesAndClears(t *testing.T) {
	srv := New(Config{Quiet: true})
	for _, method := range []string{"getMe", "banChatMember", "getMe"} {
		journalRequest(srv, http.MethodPost, "/bot901:journal/"+method, "", nil)
	}
	journalRequest(srv, http.MethodPost, "/bot902:other/getMe", "", nil)
	requests := readJournal(t, srv, "901:journal", "")
	if len(requests) != 3 {
		t.Fatalf("expected three requests for the first bot, got %+v", requests)
	}
	lastID := requests[2].Seq
	filtered := readJournal(t, srv, "901:journal", "?method=getMe&limit=1")
	if len(filtered) != 1 || filtered[0].Seq != lastID {
		t.Fatalf("filter did not select the latest matching request: %+v", filtered)
	}
	if unmatched := readJournal(t, srv, "901:journal", "?method=sendPhoto"); len(unmatched) != 0 {
		t.Fatalf("unexpected filter matches: %+v", unmatched)
	}
	rec := journalRequest(srv, http.MethodPost, "/debug/requests/901:journal/clear", "", nil)
	if rec.Code != http.StatusOK || rec.Body.String() != `{"cleared":3}` {
		t.Fatalf("clear status=%d body=%s", rec.Code, rec.Body.String())
	}
	if requests := readJournal(t, srv, "901:journal", ""); len(requests) != 0 {
		t.Fatalf("clear or debug requests polluted the journal: %+v", requests)
	}
	if requests := readJournal(t, srv, "902:other", ""); len(requests) != 1 {
		t.Fatalf("clearing one bot changed another's journal: %+v", requests)
	}
	journalRequest(srv, http.MethodPost, "/bot901:journal/getMe", "", nil)
	if requests := readJournal(t, srv, "901:journal", ""); len(requests) != 1 || requests[0].Seq <= lastID {
		t.Fatalf("request sequence reset after clear: %+v", requests)
	}
}

func TestRequestJournalRetention(t *testing.T) {
	srv := New(Config{Quiet: true})
	for range 5005 {
		journalRequest(srv, http.MethodPost, "/bot901:journal/getMe", "", nil)
	}
	requests := readJournal(t, srv, "901:journal", "?limit=10000")
	if len(requests) != 5000 || requests[0].Seq != 6 || requests[4999].Seq != 5005 {
		t.Fatalf("expected the latest 5000 requests, got %d", len(requests))
	}
	if requests := readJournal(t, srv, "901:journal", ""); len(requests) != 100 || requests[0].Seq != 4906 {
		t.Fatalf("expected the latest 100 requests by default, got %d", len(requests))
	}
	rec := journalRequest(srv, http.MethodPost, "/debug/requests/901:journal/clear", "", nil)
	if rec.Code != http.StatusOK || rec.Body.String() != `{"cleared":5000}` {
		t.Fatalf("clear must report retained requests: status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestRequestJournalOrdersConcurrentCalls(t *testing.T) {
	srv := New(Config{Quiet: true})
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		done <- journalRequest(srv, http.MethodPost, "/bot901:journal/sendMessage", "application/json", reader)
	}()
	// Wait until the first handler is reading its body, then let a later call
	// finish before completing the first request.
	if _, err := fmt.Fprint(writer, `{"chat_id":42,`); err != nil {
		t.Fatal(err)
	}
	journalRequest(srv, http.MethodPost, "/bot901:journal/getMe", "", nil)
	if _, err := fmt.Fprint(writer, `"text":"hello"}`); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if rec := <-done; rec.Code != http.StatusOK {
		t.Fatalf("delayed request status=%d body=%s", rec.Code, rec.Body.String())
	}
	requests := readJournal(t, srv, "901:journal", "")
	if len(requests) != 2 || requests[0].Method != "sendMessage" || requests[1].Method != "getMe" || requests[0].Seq >= requests[1].Seq {
		t.Fatalf("journal lost invocation order: %+v", requests)
	}
}

func TestRequestJournalPreservesBodyReadErrors(t *testing.T) {
	srv := New(Config{Quiet: true})
	body := io.MultiReader(strings.NewReader(`{"chat_id":42,"text":"hello"}`), iotest.ErrReader(errors.New("read failed")))
	rec := journalRequest(srv, http.MethodPost, "/bot901:journal/sendMessage", "application/json", body)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "read failed") {
		t.Fatalf("journal hid the body read error: status=%d body=%s", rec.Code, rec.Body.String())
	}
	requests := readJournal(t, srv, "901:journal", "")
	if len(requests) != 1 || requests[0].Status != http.StatusBadRequest {
		t.Fatalf("failed request missing from journal: %+v", requests)
	}
}

func TestRequestJournalDisabled(t *testing.T) {
	srv := New(Config{Quiet: true, DisableRequestJournal: true})
	rec := journalRequest(srv, http.MethodPost, "/bot902:journal/sendMessage", "application/json",
		strings.NewReader(`{"chat_id":42,"text":"hello"}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("request status=%d body=%s", rec.Code, rec.Body.String())
	}
	if requests := readJournal(t, srv, "902:journal", ""); len(requests) != 0 {
		t.Fatalf("disabled journal recorded %+v", requests)
	}
}
