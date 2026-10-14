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
}
