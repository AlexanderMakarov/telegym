package mock

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

// updateQueue retains the latest maxPendingUpdates unconfirmed updates per bot.
// Closing notify wakes waiting polls when an update arrives.
type updateQueue struct {
	mu      sync.Mutex
	updates []Update
	notify  chan struct{}

	// pollCancel terminates the active getUpdates request; pollSeq tells a
	// finishing request whether it still owns the slot.
	pollCancel context.CancelCauseFunc
	pollSeq    uint64

	// polling is set once the bot opts into getUpdates (by calling it or
	// deleteWebhook). Until then updates without a webhook fail fast instead
	// of piling up in a queue nobody reads.
	polling atomic.Bool
}

const maxPendingUpdates = 5000

// pollConflict is a 409 description copied verbatim from the Bot API.
type pollConflict string

func (e pollConflict) Error() string { return string(e) }

const (
	errPollTerminated pollConflict = "Conflict: terminated by other getUpdates request; make sure that only one bot instance is running"
	errPollWebhookSet pollConflict = "Conflict: terminated by setWebhook request"
	errPollWebhook    pollConflict = "Conflict: can't use getUpdates method while webhook is active; use deleteWebhook to delete the webhook first"
)

func newUpdateQueue() *updateQueue {
	return &updateQueue{notify: make(chan struct{})}
}

// Enqueue assigns the update ID via nextID and appends the update under the
// queue lock, so IDs in the queue are strictly increasing. Assigning IDs
// before taking the lock would let concurrent injectors enqueue [7, 6]; a
// confirmation with offset=8 would then silently drop 6.
// It returns the update as queued and whether the oldest update was evicted.
func (q *updateQueue) Enqueue(upd Update, nextID func() int64) (Update, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	upd.UpdateID = nextID()
	q.updates = append(q.updates, upd)
	evicted := false
	if len(q.updates) > maxPendingUpdates {
		// Re-slicing is O(1); append reallocates (and compacts) the backing
		// array only when capacity runs out, which keeps eviction amortized
		// O(1) instead of a 5000-element memmove on every enqueue.
		q.updates[0] = Update{}
		q.updates = q.updates[1:]
		evicted = true
	}
	close(q.notify)
	q.notify = make(chan struct{})
	return upd, evicted
}

// BeginPoll registers a new getUpdates request, terminating the previous one
// the way Telegram does: the newest request wins. The returned context is
// canceled with errPollTerminated or errPollWebhookSet when the request is
// superseded; release must be called when the request finishes.
func (q *updateQueue) BeginPoll(parent context.Context) (ctx context.Context, release func()) {
	ctx, cancel := context.WithCancelCause(parent)
	q.polling.Store(true)

	q.mu.Lock()
	prev := q.pollCancel
	q.pollSeq++
	seq := q.pollSeq
	q.pollCancel = cancel
	q.mu.Unlock()

	if prev != nil {
		prev(errPollTerminated)
	}
	return ctx, func() {
		q.mu.Lock()
		if q.pollSeq == seq {
			q.pollCancel = nil
		}
		q.mu.Unlock()
		cancel(nil)
	}
}

// TerminatePoll ends the active getUpdates request, if any, with cause.
func (q *updateQueue) TerminatePoll(cause error) {
	q.mu.Lock()
	cancel := q.pollCancel
	q.pollCancel = nil
	q.mu.Unlock()
	if cancel != nil {
		cancel(cause)
	}
}

// OptIn marks the bot as a polling client (see updateQueue.polling).
func (q *updateQueue) OptIn() { q.polling.Store(true) }

// Polling reports whether the bot opted into getUpdates.
func (q *updateQueue) Polling() bool { return q.polling.Load() }

func (q *updateQueue) Poll(ctx context.Context, offset int64, limit int, timeout time.Duration) []Update {
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	var timer *time.Timer
	if timeout > 0 {
		timer = time.NewTimer(timeout)
		defer timer.Stop()
	}

	for {
		q.mu.Lock()
		q.applyOffset(offset)
		if len(q.updates) > 0 {
			n := min(limit, len(q.updates))
			out := append([]Update(nil), q.updates[:n]...)
			q.mu.Unlock()
			return out
		}
		notify := q.notify
		q.mu.Unlock()

		if timeout <= 0 {
			return []Update{}
		}
		select {
		case <-ctx.Done():
			return []Update{}
		case <-timer.C:
			return []Update{}
		case <-notify:
		}
	}
}

func (q *updateQueue) applyOffset(offset int64) {
	switch {
	case offset > 0:
		first := 0
		for first < len(q.updates) && q.updates[first].UpdateID < offset {
			first++
		}
		clear(q.updates[:first])
		q.updates = q.updates[first:]
	case offset < 0:
		if offset > -int64(len(q.updates)) {
			keep := int(-offset)
			clear(q.updates[:len(q.updates)-keep])
			q.updates = q.updates[len(q.updates)-keep:]
		}
	}
}

func (q *updateQueue) Clear() {
	q.mu.Lock()
	q.updates = nil
	q.mu.Unlock()
}

func (q *updateQueue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.updates)
}
