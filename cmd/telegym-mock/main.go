// Command telegym-mock runs the telegym mock Bot API server.
//
// Configuration is via flags or environment variables:
//
//	-listen,                TELEGYM_MOCK_LISTEN                 ":5678"
//	-metrics-listen,        TELEGYM_MOCK_METRICS_LISTEN         ":9104"  (empty=disabled)
//	-default-webhook,       TELEGYM_MOCK_DEFAULT_WEBHOOK        ""  (use per-bot setWebhook)
//	-max-messages-per-chat, TELEGYM_MOCK_MAX_MESSAGES_PER_CHAT  200
//	-file-store-max-bytes,  TELEGYM_MOCK_FILE_STORE_MAX_BYTES   0  (0=100 MiB)
//	-quiet,                 TELEGYM_MOCK_QUIET                  false
//	-request-journal,       TELEGYM_MOCK_REQUEST_JOURNAL        true  (record calls for /debug/requests)
//	-rate-limits,           TELEGYM_MOCK_RATE_LIMITS            false (429 past Telegram flood limits)
//	-latency,               TELEGYM_MOCK_LATENCY                ""  (e.g. 50ms or 20ms-150ms)
//	-fault-rate,            TELEGYM_MOCK_FAULT_RATE             0  (fraction of calls answered 500)
//	-banner                                                     print ASCII banner with ANSI colors and exit
package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/kolomiichenko/telegym/pkg/mock"
)

func envOr(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return fallback
}

func envBool(key string, fallback bool) bool {
	if v, ok := os.LookupEnv(key); ok {
		return v == "1" || v == "true" || v == "TRUE"
	}
	return fallback
}

func envInt(key string, fallback int) int {
	if v, ok := os.LookupEnv(key); ok {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}

func envInt64(key string, fallback int64) int64 {
	if value, ok := os.LookupEnv(key); ok {
		if parsed, err := strconv.ParseInt(value, 10, 64); err == nil {
			return parsed
		}
	}
	return fallback
}

// parseLatency accepts "" (none), a fixed "50ms" or a range "20ms-150ms".
func parseLatency(s string) (lo, hi time.Duration, err error) {
	if s == "" {
		return 0, 0, nil
	}
	from, to, isRange := strings.Cut(s, "-")
	if lo, err = time.ParseDuration(strings.TrimSpace(from)); err != nil {
		return 0, 0, err
	}
	hi = lo
	if isRange {
		if hi, err = time.ParseDuration(strings.TrimSpace(to)); err != nil {
			return 0, 0, err
		}
	}
	if lo < 0 || hi < lo {
		return 0, 0, fmt.Errorf("want 0 <= min <= max, got %s", s)
	}
	return lo, hi, nil
}

func main() {
	cfg := mock.Config{}

	var bannerOnly bool
	flag.StringVar(&cfg.Listen, "listen", envOr("TELEGYM_MOCK_LISTEN", ":5678"), "HTTP listen address")
	flag.StringVar(&cfg.MetricsListen, "metrics-listen", envOr("TELEGYM_MOCK_METRICS_LISTEN", ":9104"), "Prometheus metrics listen address (empty=disabled)")
	flag.StringVar(&cfg.DefaultWebhookURL, "default-webhook", envOr("TELEGYM_MOCK_DEFAULT_WEBHOOK", ""), "Fallback webhook URL when a bot has not called setWebhook")
	flag.Int64Var(&cfg.FileStoreMaxBytes, "file-store-max-bytes", envInt64("TELEGYM_MOCK_FILE_STORE_MAX_BYTES", 0), "Maximum retained upload bytes (0 uses 100 MiB)")
	flag.BoolVar(&cfg.Quiet, "quiet", envBool("TELEGYM_MOCK_QUIET", false), "Suppress per-request logs")
	flag.IntVar(&cfg.MaxMessagesPerChat, "max-messages-per-chat", envInt("TELEGYM_MOCK_MAX_MESSAGES_PER_CHAT", mock.DefaultMaxMessagesPerChat), "Outbound messages kept per chat")
	requestJournal := flag.Bool("request-journal", envBool("TELEGYM_MOCK_REQUEST_JOURNAL", true), "Record Bot API calls for /debug/requests (disable on load runs)")
	flag.BoolVar(&cfg.RateLimits, "rate-limits", envBool("TELEGYM_MOCK_RATE_LIMITS", false), "Answer 429 with retry_after past Telegram flood limits")
	latency := flag.String("latency", envOr("TELEGYM_MOCK_LATENCY", ""), "Delay Bot API calls: fixed (50ms) or random range (20ms-150ms)")
	faultRate := flag.String("fault-rate", envOr("TELEGYM_MOCK_FAULT_RATE", "0"), "Fraction of Bot API calls answered with 500 (0-1)")
	flag.BoolVar(&bannerOnly, "banner", false, "Print the ASCII banner with raw ANSI colors to stdout and exit (for `freeze` and similar renderers)")
	flag.Parse()
	cfg.DisableRequestJournal = !*requestJournal

	var err error
	if cfg.LatencyMin, cfg.LatencyMax, err = parseLatency(*latency); err != nil {
		log.Fatalf("telegym-mock: invalid -latency %q: %v", *latency, err)
	}
	if cfg.FaultRate, err = strconv.ParseFloat(*faultRate, 64); err != nil || cfg.FaultRate < 0 || cfg.FaultRate > 1 {
		log.Fatalf("telegym-mock: invalid -fault-rate %q: want a number from 0 to 1", *faultRate)
	}

	if bannerOnly {
		// Write raw (always-colored) banner so downstream tools like
		// `freeze` can render the ANSI escapes into SVG/PNG. We
		// bypass mock.PrintBanner because that one strips colors
		// when stdout is a pipe.
		_, _ = io.WriteString(os.Stdout, mock.Banner)
		return
	}

	srv := mock.New(cfg)
	if err := srv.Run(); err != nil {
		log.Fatalf("telegym-mock: %v", err)
	}
}
