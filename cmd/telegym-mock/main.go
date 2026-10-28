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
//	-version                                                    print the version and exit
//	-banner                                                     print ASCII banner with ANSI colors and exit
package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"strconv"

	"github.com/kolomiichenko/telegym/pkg/mock"
)

// version is set at release build time via -ldflags "-X main.version=...".
var version = "dev"

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
	flag.BoolVar(&bannerOnly, "banner", false, "Print the ASCII banner with raw ANSI colors to stdout and exit (for `freeze` and similar renderers)")
	printVersion := flag.Bool("version", false, "Print the version and exit")
	flag.Parse()
	if *printVersion {
		fmt.Println(version)
		return
	}
	cfg.DisableRequestJournal = !*requestJournal

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
