package mock

import (
	"bytes"
	"fmt"
	"html"
	"net/url"
	"strings"

	nethtml "golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// looksLikeTelegramHTML reports whether text likely carries Bot API HTML
// (parse_mode=HTML) rather than plain text that should be fully escaped.
func looksLikeTelegramHTML(text string) bool {
	lower := strings.ToLower(text)
	for _, tag := range []string{"<a ", "<a>", "<b>", "<strong>", "<i>", "<em>", "<u>", "<s>", "<strike>", "<del>", "<code>", "<pre>", "<br", "</"} {
		if strings.Contains(lower, tag) {
			return true
		}
	}
	return false
}

var allowedAtoms = map[atom.Atom]bool{
	atom.A: true, atom.B: true, atom.Strong: true, atom.I: true, atom.Em: true,
	atom.U: true, atom.S: true, atom.Strike: true, atom.Del: true,
	atom.Code: true, atom.Pre: true, atom.Br: true,
}

// sanitizeTelegramHTML keeps a Telegram Bot API HTML subset and escapes the rest.
// Allowed: a (http/https/tg href only), b/strong, i/em, u, s/strike/del, code, pre, br.
func sanitizeTelegramHTML(src string) string {
	nodes, err := nethtml.ParseFragment(strings.NewReader(src), &nethtml.Node{Type: nethtml.ElementNode, Data: "body", DataAtom: atom.Body})
	if err != nil {
		return html.EscapeString(src)
	}
	var buf bytes.Buffer
	for _, n := range nodes {
		writeSanitized(&buf, n)
	}
	return buf.String()
}

func writeSanitized(buf *bytes.Buffer, n *nethtml.Node) {
	switch n.Type {
	case nethtml.TextNode:
		buf.WriteString(html.EscapeString(n.Data))
	case nethtml.ElementNode:
		if n.DataAtom == atom.Br {
			buf.WriteString("<br>")
			return
		}
		if !allowedAtoms[n.DataAtom] {
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				writeSanitized(buf, c)
			}
			return
		}
		if n.DataAtom == atom.A {
			href := safeHref(n)
			if href == "" {
				for c := n.FirstChild; c != nil; c = c.NextSibling {
					writeSanitized(buf, c)
				}
				return
			}
			fmt.Fprintf(buf, `<a href="%s" target="_blank" rel="noopener noreferrer">`, html.EscapeString(href))
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				writeSanitized(buf, c)
			}
			buf.WriteString("</a>")
			return
		}
		tag := n.Data
		fmt.Fprintf(buf, "<%s>", tag)
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			writeSanitized(buf, c)
		}
		fmt.Fprintf(buf, "</%s>", tag)
	default:
		// drop comments / doctype / raw
	}
}

func safeHref(n *nethtml.Node) string {
	for _, a := range n.Attr {
		if !strings.EqualFold(a.Key, "href") {
			continue
		}
		u, err := url.Parse(strings.TrimSpace(a.Val))
		if err != nil || u.Scheme == "" {
			return ""
		}
		switch strings.ToLower(u.Scheme) {
		case "http", "https", "tg":
			return a.Val
		default:
			return ""
		}
	}
	return ""
}
