package linkimport

import (
	"bytes"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// Doc is what was read from a link: its title, its description, and its
// visible text.
type Doc struct {
	Title       string
	Description string
	Text        string
}

func (d Doc) empty() bool { return d.Title == "" && d.Description == "" && d.Text == "" }

// skipped are elements whose contents are never visible text.
var skipped = map[atom.Atom]bool{
	atom.Script: true, atom.Style: true, atom.Noscript: true, atom.Template: true,
	atom.Svg: true, atom.Iframe: true, atom.Object: true,
}

// readPage reads title, description and visible text from HTML without
// building a tree: social pages are large, and only text is wanted.
func readPage(body []byte) Doc {
	var (
		doc            Doc
		title, ogTitle string
		desc, ogDesc   string
		inTitle        bool
		depthSkipped   int
		text           strings.Builder
	)
	z := html.NewTokenizer(bytes.NewReader(body))
	for {
		tt := z.Next()
		switch tt {
		case html.ErrorToken:
			doc.Title = collapse(firstNonEmpty(ogTitle, title))
			doc.Description = collapse(firstNonEmpty(ogDesc, desc))
			doc.Text = collapse(text.String())
			return doc
		case html.StartTagToken, html.SelfClosingTagToken:
			tok := z.Token()
			switch {
			case tok.DataAtom == atom.Title:
				inTitle = tt == html.StartTagToken
			case tok.DataAtom == atom.Meta:
				key, content := metaPair(tok)
				switch key {
				case "og:title", "twitter:title":
					ogTitle = firstNonEmpty(ogTitle, content)
				case "og:description", "twitter:description":
					ogDesc = firstNonEmpty(ogDesc, content)
				case "description":
					desc = firstNonEmpty(desc, content)
				}
			case skipped[tok.DataAtom] && tt == html.StartTagToken:
				depthSkipped++
			default:
				// Block elements end a line of text.
				text.WriteByte(' ')
			}
		case html.EndTagToken:
			tok := z.Token()
			switch {
			case tok.DataAtom == atom.Title:
				inTitle = false
			case skipped[tok.DataAtom] && depthSkipped > 0:
				depthSkipped--
			}
		case html.TextToken:
			t := string(z.Text())
			switch {
			case inTitle:
				title += t
			case depthSkipped == 0:
				text.WriteString(t)
				text.WriteByte(' ')
			}
		}
	}
}

func metaPair(tok html.Token) (key, content string) {
	for _, a := range tok.Attr {
		switch strings.ToLower(a.Key) {
		case "property", "name":
			key = strings.ToLower(strings.TrimSpace(a.Val))
		case "content":
			content = a.Val
		}
	}
	return key, content
}

// collapse folds runs of whitespace into one space.
func collapse(s string) string { return strings.Join(strings.Fields(s), " ") }
