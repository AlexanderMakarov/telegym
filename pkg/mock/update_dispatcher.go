package mock

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"
)

// UpdateDispatcher delivers Updates to a bot's webhook, or queues them for
// polling when no webhook is configured.
type UpdateDispatcher struct {
	client      *http.Client
	defaultURL  string // fallback if a bot never called setWebhook
	logFailures bool
	metrics     *Metrics // optional - instruments dispatch counts and duration
}

// WebhookDispatcher is an alias for UpdateDispatcher.
//
// Deprecated: Use UpdateDispatcher instead.
type WebhookDispatcher = UpdateDispatcher

// NewWebhookDispatcher creates an UpdateDispatcher.
//
// Deprecated: Use NewUpdateDispatcher instead.
func NewWebhookDispatcher(defaultURL string) *WebhookDispatcher {
	return NewUpdateDispatcher(defaultURL)
}

func NewUpdateDispatcher(defaultURL string) *UpdateDispatcher {
	return &UpdateDispatcher{
		client: &http.Client{
			Timeout: 10 * time.Second,
			Transport: &http.Transport{
				MaxIdleConns:        500,
				MaxIdleConnsPerHost: 500,
				IdleConnTimeout:     90 * time.Second,
			},
		},
		defaultURL:  defaultURL,
		logFailures: true,
	}
}

func (d *UpdateDispatcher) target(bot *botEntry) (url, secret string) {
	bot.msgMu.RLock()
	defer bot.msgMu.RUnlock()
	url, secret = bot.webhookURL, bot.secretToken
	// An explicit webhook deletion disables the configured default as well.
	if !bot.webhookSet {
		url = d.defaultURL
	}
	return url, secret
}

func (d *UpdateDispatcher) hasTarget(bot *botEntry) bool {
	url, _ := d.target(bot)
	return url != ""
}

// DeliveryResult describes what happened to a dispatched update.
type DeliveryResult struct {
	// UpdateID is the ID assigned to the update.
	UpdateID int64
	// Status is the webhook's HTTP status; zero when the update was queued.
	Status int
	// Queued reports that the update went to the getUpdates queue.
	Queued bool
}

// ErrNoUpdateTarget is returned when a bot has neither a webhook nor opted
// into getUpdates, so the update would sit in a queue nobody reads.
var ErrNoUpdateTarget = errors.New("no webhook URL set for bot (call setWebhook, set TELEGYM_MOCK_DEFAULT_WEBHOOK, or start polling with getUpdates/deleteWebhook)")

// Deliver posts the update to the bot's webhook and returns its HTTP status,
// or queues it for getUpdates and returns zero.
//
// Deprecated: Use DeliverUpdate, which also reports the assigned update ID.
func (d *UpdateDispatcher) Deliver(ctx context.Context, bot *botEntry, upd Update) (int, error) {
	res, err := d.DeliverUpdate(ctx, bot, upd)
	return res.Status, err
}

// DeliverUpdate posts the update to the bot's webhook, or queues it for
// getUpdates once the bot opted into polling. A zero upd.UpdateID is assigned
// here; queued updates always get a fresh ID under the queue lock so the
// queue stays ordered.
func (d *UpdateDispatcher) DeliverUpdate(ctx context.Context, bot *botEntry, upd Update) (DeliveryResult, error) {
	url, secret := d.target(bot)
	if url == "" {
		if !bot.updates.Polling() {
			if d.metrics != nil {
				d.metrics.WebhookDispatchTotal.WithLabelValues("no_url").Inc()
			}
			return DeliveryResult{}, ErrNoUpdateTarget
		}
		queued, evicted := bot.updates.Enqueue(upd, bot.parent.NextUpdateID)
		if d.metrics != nil {
			d.metrics.WebhookDispatchTotal.WithLabelValues("queued").Inc()
			if evicted {
				d.metrics.UpdatesEvictedTotal.Inc()
			}
		}
		return DeliveryResult{UpdateID: queued.UpdateID, Queued: true}, nil
	}
	if upd.UpdateID == 0 {
		upd.UpdateID = bot.parent.NextUpdateID()
	}
	res := DeliveryResult{UpdateID: upd.UpdateID}

	body, err := json.Marshal(upd)
	if err != nil {
		return res, fmt.Errorf("marshal update: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return res, err
	}
	req.Header.Set("Content-Type", "application/json")
	if secret != "" {
		// Real Telegram sends this header so bots can verify the call originated
		// from their setWebhook setup; mirror that behavior.
		req.Header.Set("X-Telegram-Bot-Api-Secret-Token", secret)
	}

	start := time.Now()
	resp, err := d.client.Do(req)
	dur := time.Since(start)
	if err != nil {
		d.observe("fail", dur)
		if d.logFailures {
			log.Printf("dispatcher: deliver to %s failed: %v", url, err)
		}
		return res, err
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)

	if resp.StatusCode >= 400 {
		d.observe("fail", dur)
		if d.logFailures {
			log.Printf("dispatcher: %s returned %d for update_id=%d", url, resp.StatusCode, upd.UpdateID)
		}
	} else {
		d.observe("ok", dur)
	}
	res.Status = resp.StatusCode
	return res, nil
}

func (d *UpdateDispatcher) observe(result string, dur time.Duration) {
	if d.metrics == nil {
		return
	}
	d.metrics.WebhookDispatchTotal.WithLabelValues(result).Inc()
	d.metrics.WebhookDispatchDuration.Observe(dur.Seconds())
}
