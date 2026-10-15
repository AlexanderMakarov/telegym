package mock

import (
	"strings"
	"testing"
)

func TestSanitizeTelegramHTML_keepsSafeTags(t *testing.T) {
	in := `<i>hi</i> <a href="https://en.wiktionary.org/wiki/բարև">բարև</a> <script>x</script>`
	out := sanitizeTelegramHTML(in)
	if !strings.Contains(out, "<i>hi</i>") {
		t.Fatalf("italic lost: %s", out)
	}
	if !strings.Contains(out, `href="https://en.wiktionary.org/wiki/`) {
		t.Fatalf("safe link lost: %s", out)
	}
	if strings.Contains(out, "<script") {
		t.Fatalf("script must be stripped: %s", out)
	}
	if strings.Contains(out, "javascript:") {
		t.Fatalf("bad scheme: %s", out)
	}
}

func TestSanitizeTelegramHTML_dropsJavascriptHref(t *testing.T) {
	out := sanitizeTelegramHTML(`<a href="javascript:alert(1)">x</a>`)
	if strings.Contains(out, "javascript:") || strings.Contains(out, "<a ") {
		t.Fatalf("js href must not render as link: %s", out)
	}
	if !strings.Contains(out, "x") {
		t.Fatalf("link text should remain: %s", out)
	}
}

func TestRenderTextWithCommands_htmlPath(t *testing.T) {
	got := string(renderTextWithCommands(`<i>Theory</i> <a href="https://example.com/a">word</a>`, "tok", 1, "n"))
	if strings.Contains(got, "&lt;i&gt;") {
		t.Fatalf("must not escape Telegram HTML: %s", got)
	}
	if !strings.Contains(got, "<i>Theory</i>") || !strings.Contains(got, "href=") {
		t.Fatalf("expected rendered HTML: %s", got)
	}
	if !strings.Contains(got, `class="msg-prose"`) {
		t.Fatalf("prose must be in msg-prose container: %s", got)
	}
}

func TestSanitizeTelegramHTML_keepsTheoryTable(t *testing.T) {
	in := `Intro<table class="theory-table borders-grid"><tr><th>Nom</th><td colspan="2">ես</td></tr></table>Outro`
	out := sanitizeTelegramHTML(in)
	if !strings.Contains(out, `<table class="theory-table borders-grid">`) || !strings.Contains(out, `colspan="2"`) {
		t.Fatalf("table markup lost: %s", out)
	}
	if strings.Contains(out, "<script") {
		t.Fatalf("unexpected script: %s", out)
	}
}

func TestRenderTextWithCommands_splitsTableContainers(t *testing.T) {
	in := `Before<table class="theory-table borders-grid"><tr><th>A</th></tr></table>After`
	got := string(renderTextWithCommands(in, "tok", 1, "n"))
	if strings.Count(got, `class="msg-prose"`) < 2 {
		t.Fatalf("want prose containers around table: %s", got)
	}
	if !strings.Contains(got, `class="msg-table-scroll"`) || !strings.Contains(got, "<table") {
		t.Fatalf("want scrollable HTML table container: %s", got)
	}
	if strings.Contains(got, "<img") || strings.Contains(got, "msg-media-scroll") {
		t.Fatalf("must not render tables as photos: %s", got)
	}
}

func TestSplitMessageContainers_trimsBlankLinesAroundTables(t *testing.T) {
	in := "Before prose.\n\n\n<table class=\"theory-table\"><tr><th>A</th></tr></table>\n\n\nAfter prose."
	got := splitMessageContainers(in, false)
	if strings.Contains(got, "msg-prose\">\n") || strings.Contains(got, "\n</div><div class=\"msg-table") {
		t.Fatalf("prose must not keep leading/trailing blank lines: %q", got)
	}
	if !strings.Contains(got, `class="msg-prose">Before prose.</div>`) {
		t.Fatalf("before prose trimmed: %q", got)
	}
	if !strings.Contains(got, `class="msg-prose">After prose.</div>`) {
		t.Fatalf("after prose trimmed: %q", got)
	}
	if !strings.Contains(got, `class="msg-table-scroll"`) {
		t.Fatalf("table wrapper missing: %q", got)
	}
}
