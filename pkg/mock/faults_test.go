package mock

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func callBot(t *testing.T, srv *Server, method, body string) (int, APIResponse) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/bot903:faults/"+method, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	var resp APIResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("%s: decode %q: %v", method, rec.Body.String(), err)
	}
	return rec.Code, resp
}

func sendTo(chatID string) string {
	return `{"chat_id":` + chatID + `,"text":"hi"}`
}

func TestFaultsDisabledByDefault(t *testing.T) {
	srv := New(Config{Quiet: true})
	for i := range 10 {
		if code, _ := callBot(t, srv, "sendMessage", sendTo("42")); code != http.StatusOK {
			t.Fatalf("call %d: status %d, want 200", i, code)
		}
	}
}

func TestRateLimitsPrivateChat(t *testing.T) {
	srv := New(Config{Quiet: true, RateLimits: true})
	for i := range int(DefaultFloodLimits.Private.Burst) {
		code, resp := callBot(t, srv, "sendMessage", sendTo("42"))
		if code != http.StatusOK {
			t.Fatalf("call %d: status %d, want 200", i, code)
		}
		// The body reaches the handler intact after chat_id is peeked.
		if msg, _ := resp.Result.(map[string]any); msg["text"] != "hi" {
			t.Fatalf("call %d: result %+v, want text hi", i, resp.Result)
		}
	}

	code, resp := callBot(t, srv, "sendMessage", sendTo("42"))
	if code != http.StatusTooManyRequests || resp.OK || resp.ErrorCode != 429 ||
		resp.Parameters == nil || resp.Parameters.RetryAfter < 1 ||
		!strings.HasPrefix(resp.Description, "Too Many Requests: retry after ") {
		t.Fatalf("over limit: status %d, response %+v", code, resp)
	}

	// Other chats and non-sending methods are unaffected.
	if code, _ := callBot(t, srv, "sendMessage", sendTo("43")); code != http.StatusOK {
		t.Fatalf("other chat: status %d, want 200", code)
	}
	for _, method := range []string{"getMe", "sendChatAction"} {
		if code, _ := callBot(t, srv, method, sendTo("42")); code == http.StatusTooManyRequests {
			t.Fatalf("%s was rate limited", method)
		}
	}
}

func TestRateLimitsGroupAndBot(t *testing.T) {
	// The default bot bucket refills 30 tokens/s, so a slow CI runner could
	// earn an extra token mid-test; keep the burst but make refill negligible.
	bot := RateLimit{Rate: 0.001, Burst: DefaultFloodLimits.Bot.Burst}
	srv := New(Config{Quiet: true, RateLimits: true, FloodLimits: FloodLimits{Bot: bot}})
	for i := range int(DefaultFloodLimits.Group.Burst) {
		if code, _ := callBot(t, srv, "sendMessage", sendTo(`"-100500"`)); code != http.StatusOK {
			t.Fatalf("group call %d: status %d, want 200", i, code)
		}
	}
	code, resp := callBot(t, srv, "sendMessage", sendTo(`"-100500"`))
	if code != http.StatusTooManyRequests || resp.Parameters.RetryAfter < 2 {
		t.Fatalf("group over limit: status %d, response %+v", code, resp)
	}

	// The group used 20 of the bot's 30 tokens; ten more private chats fit.
	for i := range int(DefaultFloodLimits.Bot.Burst - DefaultFloodLimits.Group.Burst) {
		if code, _ := callBot(t, srv, "sendMessage", sendTo(strconv.Itoa(100+i))); code != http.StatusOK {
			t.Fatalf("private call %d: status %d, want 200", i, code)
		}
	}
	if code, _ := callBot(t, srv, "sendMessage", sendTo("999")); code != http.StatusTooManyRequests {
		t.Fatalf("bot over limit: status %d, want 429", code)
	}
}

func TestRateLimiterRefillsAndSweeps(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	l := newRateLimiter(FloodLimits{})
	l.now = func() time.Time { return now }

	for range int(DefaultFloodLimits.Private.Burst) {
		if wait := l.allow("t", "42", false); wait != 0 {
			t.Fatalf("burst refused, wait %v", wait)
		}
	}
	if wait := l.allow("t", "42", false); wait != time.Second {
		t.Fatalf("wait = %v, want 1s", wait)
	}
	now = now.Add(time.Second)
	if wait := l.allow("t", "42", false); wait != 0 {
		t.Fatalf("after refill wait = %v, want 0", wait)
	}

	now = now.Add(limitSweepInterval)
	l.allow("t", "43", false)
	if _, ok := l.chats["t\x0042"]; ok || len(l.chats) != 1 {
		t.Fatalf("idle bucket kept: %v", l.chats)
	}
}

func TestRateLimitsMultipartUpload(t *testing.T) {
	srv := New(Config{Quiet: true, RateLimits: true})
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	_ = w.WriteField("chat_id", "42")
	part, _ := w.CreateFormFile("photo", "a.png")
	_, _ = part.Write([]byte("png"))
	_ = w.Close()

	req := httptest.NewRequest(http.MethodPost, "/bot903:faults/sendPhoto", &body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	if count, _ := srv.Store().Files.Stats(); count != 1 {
		t.Fatalf("upload not captured, files = %d", count)
	}
}

func TestFaultRateAndLatency(t *testing.T) {
	srv := New(Config{Quiet: true, FaultRate: 1})
	code, resp := callBot(t, srv, "getMe", "{}")
	if code != http.StatusInternalServerError || resp.ErrorCode != 500 || resp.OK {
		t.Fatalf("fault: status %d, response %+v", code, resp)
	}

	srv = New(Config{Quiet: true, LatencyMin: 30 * time.Millisecond, LatencyMax: 30 * time.Millisecond})
	start := time.Now()
	if code, _ := callBot(t, srv, "getMe", "{}"); code != http.StatusOK {
		t.Fatalf("latency: status %d", code)
	}
	if elapsed := time.Since(start); elapsed < 30*time.Millisecond {
		t.Fatalf("latency not applied: %v", elapsed)
	}

	// Debug routes stay fast and healthy.
	srv = New(Config{Quiet: true, FaultRate: 1})
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/debug/bots", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("debug route faulted: %d", rec.Code)
	}
}

func TestParseRateLimit(t *testing.T) {
	tests := []struct {
		in   string
		want RateLimit
		ok   bool
	}{
		{"30/s", RateLimit{Rate: 30, Burst: 30}, true},
		{"1/s,3", RateLimit{Rate: 1, Burst: 3}, true},
		{"20/min", RateLimit{Rate: 20.0 / 60, Burst: 20}, true},
		{" 3600 / h , 10 ", RateLimit{Rate: 1, Burst: 10}, true},
		{"30", RateLimit{}, false},
		{"0/s", RateLimit{}, false},
		{"5/day", RateLimit{}, false},
		{"5/s,0", RateLimit{}, false},
	}
	for _, tt := range tests {
		got, err := ParseRateLimit(tt.in)
		if (err == nil) != tt.ok || got != tt.want {
			t.Errorf("ParseRateLimit(%q) = %+v, %v; want %+v, ok=%v", tt.in, got, err, tt.want, tt.ok)
		}
	}
}

func TestRateLimitsCustom(t *testing.T) {
	srv := New(Config{Quiet: true, RateLimits: true, FloodLimits: FloodLimits{Private: RateLimit{Rate: 1, Burst: 5}}})
	for i := range 5 {
		if code, _ := callBot(t, srv, "sendMessage", sendTo("42")); code != http.StatusOK {
			t.Fatalf("call %d: status %d, want 200", i, code)
		}
	}
	if code, _ := callBot(t, srv, "sendMessage", sendTo("42")); code != http.StatusTooManyRequests {
		t.Fatalf("over custom burst: status %d, want 429", code)
	}
}

func TestRateLimitsPaidBroadcast(t *testing.T) {
	srv := New(Config{Quiet: true, RateLimits: true})
	paid := func(chatID int) string {
		return `{"chat_id":` + strconv.Itoa(chatID) + `,"text":"hi","allow_paid_broadcast":true}`
	}
	// Twice the free per-bot burst goes through when paid, one chat each.
	for i := range 2 * int(DefaultFloodLimits.Bot.Burst) {
		if code, _ := callBot(t, srv, "sendMessage", paid(1000+i)); code != http.StatusOK {
			t.Fatalf("paid call %d: status %d, want 200", i, code)
		}
	}
	rec := httptest.NewRecorder()
	srv.metrics.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	want := "telegym_mock_paid_broadcast_messages_total " + strconv.Itoa(2*int(DefaultFloodLimits.Bot.Burst)) + "\n"
	if !strings.Contains(rec.Body.String(), want) {
		t.Fatalf("metrics missing %q", want)
	}
	// Paid calls use their own bucket, and per-chat limits still apply.
	if code, _ := callBot(t, srv, "sendMessage", sendTo("1")); code != http.StatusOK {
		t.Fatalf("free call after paid burst: status %d, want 200", code)
	}
	for range int(DefaultFloodLimits.Private.Burst) - 1 {
		callBot(t, srv, "sendMessage", paid(1000))
	}
	if code, _ := callBot(t, srv, "sendMessage", paid(1000)); code != http.StatusTooManyRequests {
		t.Fatalf("paid call past chat limit: status %d, want 429", code)
	}
}
