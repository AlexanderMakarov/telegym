package mock

import (
	"html"
	"html/template"
	"net/url"
	"strings"

	nethtml "golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// renderTelegramHTML renders a parse_mode=HTML message body for the debug
// chat. It keeps the tags Telegram supports in that mode (see
// https://core.telegram.org/bots/api#html-style), maps synonyms to one
// form, and drops every other tag while keeping its text. Newlines are
// line breaks, as in Telegram; /commands outside links and code stay
// clickable.
func renderTelegramHTML(src, token string, chatID int64, name string) template.HTML {
	nodes, err := nethtml.ParseFragment(strings.NewReader(src), &nethtml.Node{
		Type: nethtml.ElementNode, Data: "body", DataAtom: atom.Body,
	})
	if err != nil {
		return renderTextWithCommands(src, token, chatID, name)
	}
	r := telegramHTMLRenderer{token: token, chatID: chatID, name: name}
	for _, n := range nodes {
		r.render(n, htmlContext{})
	}
	return template.HTML(r.b.String())
}

type htmlContext struct {
	pre     bool // inside <pre>: keep newlines as-is
	literal bool // inside <pre>, <code> or <a>: no command links
}

type telegramHTMLRenderer struct {
	b      strings.Builder
	token  string
	chatID int64
	name   string
	rich   bool // sendRichMessage html: block tags allowed, newlines are whitespace
}

// telegramHTMLTags maps each supported tag to the tag the debug chat emits.
var telegramHTMLTags = map[atom.Atom]string{
	atom.B: "b", atom.Strong: "b",
	atom.I: "i", atom.Em: "i",
	atom.U: "u", atom.Ins: "u",
	atom.S: "s", atom.Strike: "s", atom.Del: "s",
	atom.Code: "code", atom.Pre: "pre",
}

func (r *telegramHTMLRenderer) renderChildren(n *nethtml.Node, ctx htmlContext) {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		r.render(c, ctx)
	}
}

func (r *telegramHTMLRenderer) render(n *nethtml.Node, ctx htmlContext) {
	switch n.Type {
	case nethtml.TextNode:
		text := html.EscapeString(n.Data)
		if !ctx.literal {
			text = linkifyCommands(text, r.token, r.chatID, r.name)
		}
		if !ctx.pre && !r.rich {
			text = strings.ReplaceAll(text, "\n", "<br>")
		}
		r.b.WriteString(text)
		return
	case nethtml.ElementNode:
	default:
		return // comments, doctypes
	}
	if r.rich && r.renderRichElement(n, ctx) {
		return
	}

	open, closeTag := "", ""
	switch {
	case telegramHTMLTags[n.DataAtom] != "":
		tag := telegramHTMLTags[n.DataAtom]
		open, closeTag = "<"+tag+">", "</"+tag+">"
		if tag == "pre" || tag == "code" {
			ctx.literal = true
		}
		if tag == "pre" {
			ctx.pre = true
		}
	case n.DataAtom == atom.A:
		if href := telegramLinkHref(n); href != "" {
			open = `<a href="` + html.EscapeString(href) + `" target="_blank" rel="noopener noreferrer">`
			closeTag = "</a>"
			ctx.literal = true
		}
	case n.DataAtom == atom.Blockquote:
		open, closeTag = "<blockquote>", "</blockquote>"
		if hasAttr(n, "expandable") {
			open = `<blockquote class="expandable">`
		}
	case n.Data == "tg-spoiler" || (n.DataAtom == atom.Span && attrValue(n, "class") == "tg-spoiler"):
		open, closeTag = `<span class="tg-spoiler">`, "</span>"
	}
	// Unsupported tags (and tg-emoji / tg-time, which only decorate their
	// text) render their children only.
	r.b.WriteString(open)
	r.renderChildren(n, ctx)
	r.b.WriteString(closeTag)
}

// telegramLinkHref returns the href of an <a> if it is safe to render as a
// link (http, https, tg or mailto), or "" otherwise.
func telegramLinkHref(n *nethtml.Node) string {
	href := strings.TrimSpace(attrValue(n, "href"))
	u, err := url.Parse(href)
	if err != nil {
		return ""
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https", "tg", "mailto":
		return href
	}
	return ""
}

func attrValue(n *nethtml.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

func hasAttr(n *nethtml.Node, key string) bool {
	for _, a := range n.Attr {
		if a.Key == key {
			return true
		}
	}
	return false
}
