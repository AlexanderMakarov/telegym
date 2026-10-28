package mock

import (
	"bytes"
	"encoding/json"
	"io"
	"math"
	"math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// Telegram flood limits, per https://core.telegram.org/bots/faq#my-bot-is-hitting-limits-how-do-i-avoid-this.
// Each is a token bucket: burst messages at once, then refilling at rate/s.
const (
	botRate, botBurst         = 30.0, 30.0      // ~30 messages/s across all chats
	privateRate, privateBurst = 1.0, 3.0        // ~1 message/s per private chat, short bursts tolerated
	groupRate, groupBurst     = 20.0 / 60, 20.0 // 20 messages/min per group or channel
)

// limitSweepInterval bounds how often idle buckets are dropped, so long runs
// over many chats keep the limiter small.
const limitSweepInterval = time.Minute

type bucket struct {
	tokens float64
	last   time.Time
}

// refill tops the bucket up for the time elapsed since its last use.
func (b *bucket) refill(now time.Time, rate, burst float64) {
	if b.last.IsZero() {
		b.tokens = burst
	} else {
		b.tokens = min(burst, b.tokens+now.Sub(b.last).Seconds()*rate)
	}
	b.last = now
}

// wait returns how long until the bucket holds a whole token.
func (b *bucket) wait(rate float64) time.Duration {
	if b.tokens >= 1 {
		return 0
	}
	return time.Duration((1 - b.tokens) / rate * float64(time.Second))
}

// rateLimiter emulates Telegram's per-bot and per-chat flood limits.
type rateLimiter struct {
	mu        sync.Mutex
	now       func() time.Time
	bots      map[string]*bucket
	chats     map[string]*bucket // keyed by token + "\x00" + chat_id
	lastSweep time.Time
}

func newRateLimiter() *rateLimiter {
	return &rateLimiter{
		now:   time.Now,
		bots:  make(map[string]*bucket),
		chats: make(map[string]*bucket),
	}
}

// allow takes a token from both the bot and chat buckets, or returns how
// long the caller must wait when either is empty. Nothing is consumed on
// refusal, matching Telegram where rejected calls do not count.
func (l *rateLimiter) allow(token, chatID string) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	l.sweep(now)

	chatRate, chatBurst := privateRate, privateBurst
	if strings.HasPrefix(chatID, "-") || strings.HasPrefix(chatID, "@") {
		chatRate, chatBurst = groupRate, groupBurst
	}
	bot := l.bucket(l.bots, token)
	chat := l.bucket(l.chats, token+"\x00"+chatID)
	bot.refill(now, botRate, botBurst)
	chat.refill(now, chatRate, chatBurst)

	if wait := max(bot.wait(botRate), chat.wait(chatRate)); wait > 0 {
		return wait
	}
	bot.tokens--
	chat.tokens--
	return 0
}

func (l *rateLimiter) bucket(m map[string]*bucket, key string) *bucket {
	b, ok := m[key]
	if !ok {
		b = &bucket{}
		m[key] = b
	}
	return b
}

// sweep drops buckets idle long enough to have refilled completely; a
// fresh bucket starts full, so forgetting them changes nothing.
func (l *rateLimiter) sweep(now time.Time) {
	if now.Sub(l.lastSweep) < limitSweepInterval {
		return
	}
	l.lastSweep = now
	for _, m := range []map[string]*bucket{l.bots, l.chats} {
		for key, b := range m {
			if now.Sub(b.last) >= limitSweepInterval {
				delete(m, key)
			}
		}
	}
}

// isFloodLimited reports whether a Bot API method sends a message and so
// counts against Telegram's flood limits.
func isFloodLimited(method string) bool {
	switch method {
	case "sendChatAction", "sendMessageDraft", "sendRichMessageDraft":
		return false
	case "copyMessage", "copyMessages", "forwardMessage", "forwardMessages":
		return true
	}
	return strings.HasPrefix(method, "send")
}

// faultsMiddleware adds configured latency, random 500s and Telegram flood
// limits to Bot API calls. Debug and chat UI routes are left untouched.
func (s *Server) faultsMiddleware() gin.HandlerFunc {
	var limiter *rateLimiter
	if s.cfg.RateLimits {
		limiter = newRateLimiter()
	}
	return func(c *gin.Context) {
		token, method, ok := parseBotPath(c.Request.URL.Path)
		if !ok {
			c.Next()
			return
		}

		if delay := s.latency(); delay > 0 {
			select {
			case <-time.After(delay):
			case <-c.Request.Context().Done():
				c.Abort()
				return
			}
		}

		if s.cfg.FaultRate > 0 && rand.Float64() < s.cfg.FaultRate {
			c.AbortWithStatusJSON(http.StatusInternalServerError, APIResponse{
				OK: false, ErrorCode: http.StatusInternalServerError, Description: "Internal Server Error",
			})
			return
		}

		if limiter != nil && isFloodLimited(method) {
			if wait := limiter.allow(token, requestChatID(c)); wait > 0 {
				retryAfter := max(1, int(math.Ceil(wait.Seconds())))
				c.AbortWithStatusJSON(http.StatusTooManyRequests, APIResponse{
					OK:          false,
					ErrorCode:   http.StatusTooManyRequests,
					Description: "Too Many Requests: retry after " + strconv.Itoa(retryAfter),
					Parameters:  &ResponseParameters{RetryAfter: retryAfter},
				})
				return
			}
		}
		c.Next()
	}
}

// latency picks a delay in [LatencyMin, LatencyMax].
func (s *Server) latency() time.Duration {
	lo, hi := s.cfg.LatencyMin, s.cfg.LatencyMax
	if hi <= lo {
		return lo
	}
	return lo + rand.N(hi-lo+1)
}

// requestChatID reads chat_id from the query, form or JSON body without
// consuming the body for the handler that runs next.
func requestChatID(c *gin.Context) string {
	ct := c.ContentType()
	if ct == "multipart/form-data" || ct == "application/x-www-form-urlencoded" || c.Request.Body == nil {
		// Parsed forms are cached on the request, so handlers reuse them.
		return c.Request.FormValue("chat_id")
	}
	if id := c.Query("chat_id"); id != "" {
		return id
	}
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		// Replay what was read, then the same error, so the handler still
		// reports the broken body.
		c.Request.Body = io.NopCloser(io.MultiReader(bytes.NewReader(body), errReader{err}))
		return ""
	}
	c.Request.Body = io.NopCloser(bytes.NewReader(body))
	var payload struct {
		ChatID json.RawMessage `json:"chat_id"`
	}
	if json.Unmarshal(body, &payload) != nil {
		return ""
	}
	return strings.Trim(string(payload.ChatID), `"`)
}

type errReader struct{ err error }

func (r errReader) Read([]byte) (int, error) { return 0, r.err }
