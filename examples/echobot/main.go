// Command echobot is a minimal Telegram bot used to validate telegym end-to-end.
// It speaks the Bot API directly (no telego/tgbotapi dependency) so the kit
// has no coupling to any specific Go Telegram framework.
//
// Flow:
//   - Listens on /webhook (HTTP) for Updates pushed by telegym-mock, or with
//     -poll long-polls getUpdates instead (no inbound port needed, so it also
//     works when the mock runs in Docker)
//   - On any text message: replies with "Hello <name>" + an inline button
//   - On callback_data "echo_btn": acks + replies "you clicked!"
//
// All outbound calls go to the mock Bot API at TELEGYM_MOCK_URL.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"time"
)

type update struct {
	UpdateID int64 `json:"update_id"`
	Message  *struct {
		MessageID int    `json:"message_id"`
		Text      string `json:"text"`
		From      struct {
			ID        int64  `json:"id"`
			FirstName string `json:"first_name"`
		} `json:"from"`
		Chat struct {
			ID int64 `json:"id"`
		} `json:"chat"`
	} `json:"message,omitempty"`
	CallbackQuery *struct {
		ID   string `json:"id"`
		Data string `json:"data"`
		From struct {
			ID int64 `json:"id"`
		} `json:"from"`
		Message struct {
			Chat struct {
				ID int64 `json:"id"`
			} `json:"chat"`
		} `json:"message"`
	} `json:"callback_query,omitempty"`
}

var (
	mockURL string
	token   string
	httpc   = &http.Client{}
)

func main() {
	listen := flag.String("listen", envOr("ECHOBOT_LISTEN", ":8443"), "HTTP listen address")
	flag.StringVar(&mockURL, "mock-url", envOr("TELEGYM_MOCK_URL", "http://localhost:5678"), "telegym-mock base URL")
	flag.StringVar(&token, "token", envOr("BOT_TOKEN", "1234567890:telegym_default_mock_token_xxxxxxxx"), "bot token")
	poll := flag.Bool("poll", os.Getenv("ECHOBOT_POLL") == "true", "long-poll getUpdates instead of serving a webhook")
	flag.Parse()

	if *poll {
		// Drop any webhook left from an earlier run: getUpdates answers 409
		// while one is set.
		retry("deleteWebhook", map[string]any{})
		log.Printf("echobot: polling %s for updates", mockURL)
		pollUpdates()
		return
	}

	retry("setWebhook", map[string]any{"url": "http://localhost" + *listen + "/webhook"})
	log.Printf("echobot: registered webhook → http://localhost%s/webhook", *listen)

	http.HandleFunc("/webhook", handleWebhook)
	http.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})

	log.Printf("echobot: listening on %s, mock=%s, token=%s", *listen, mockURL, token)
	log.Fatal(http.ListenAndServe(*listen, nil))
}

// retry calls method until the mock answers, so the bot doesn't race with
// mock startup in `make scenario-*` orchestration scripts.
func retry(method string, body map[string]any) {
	var err error
	for range 30 {
		if _, err = apiCall(method, body); err == nil {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	log.Fatalf("%s (after retries): %v", method, err)
}

// pollUpdates long-polls getUpdates and handles each update concurrently,
// like a webhook server would.
func pollUpdates() {
	var offset int64
	for {
		res, err := apiCall("getUpdates", map[string]any{"offset": offset, "timeout": 25})
		if err != nil {
			log.Printf("getUpdates: %v", err)
			time.Sleep(time.Second)
			continue
		}
		var updates []update
		if err := json.Unmarshal(res, &updates); err != nil {
			log.Printf("getUpdates: %v", err)
			continue
		}
		for _, u := range updates {
			offset = u.UpdateID + 1
			go handle(u)
		}
	}
}

func handleWebhook(w http.ResponseWriter, r *http.Request) {
	defer func() { _ = r.Body.Close() }()
	var u update
	if err := json.NewDecoder(r.Body).Decode(&u); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	handle(u)
	w.WriteHeader(200)
}

func handle(u update) {
	switch {
	case u.Message != nil:
		chat := u.Message.Chat.ID
		name := u.Message.From.FirstName
		if name == "" {
			name = "stranger"
		}
		_ = callBot("sendMessage", map[string]any{
			"chat_id": chat,
			"text":    fmt.Sprintf("Hello %s - you said %q", name, u.Message.Text),
			"reply_markup": map[string]any{
				"inline_keyboard": [][]map[string]string{{
					{"text": "Echo me", "callback_data": "echo_btn"},
				}},
			},
		})

	case u.CallbackQuery != nil:
		cb := u.CallbackQuery
		_ = callBot("answerCallbackQuery", map[string]any{
			"callback_query_id": cb.ID,
		})
		_ = callBot("sendMessage", map[string]any{
			"chat_id": cb.Message.Chat.ID,
			"text":    fmt.Sprintf("you clicked %q", cb.Data),
		})
	}
}

func callBot(method string, body map[string]any) error {
	_, err := apiCall(method, body)
	return err
}

// apiCall invokes a Bot API method on the mock and returns its result field.
func apiCall(method string, body map[string]any) (json.RawMessage, error) {
	b, _ := json.Marshal(body)
	url := fmt.Sprintf("%s/bot%s/%s", mockURL, token, method)
	resp, err := httpc.Post(url, "application/json", bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%s → %d %s", method, resp.StatusCode, data)
	}
	var out struct {
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("%s: %w", method, err)
	}
	return out.Result, nil
}

func envOr(k, v string) string {
	if x := os.Getenv(k); x != "" {
		return x
	}
	return v
}
