// Package scrape fetches web pages and reduces them to plain text.
package fetch

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/html"
)

// Defaults.
const (
	DefaultMaxChars    = 50000
	DefaultTimeout     = 10 * time.Second
	DefaultConcurrency = 4
	maxBodyBytes       = 8 << 20
	userAgent          = "Mozilla/5.0 (compatible; skimmer/1.0; +https://github.com/dorkitude/skimmer)"
)

// Fetcher downloads pages and extracts their text.
type Fetcher struct {
	// Client defaults to one with DefaultTimeout and a cookie jar, so
	// cookies set by one fetch (e.g. after a Reddit challenge) apply to
	// the rest. A caller-supplied Client keeps its own Jar, if any.
	Client *http.Client
	// MaxChars caps the extracted text per page (runes). ≤0 means DefaultMaxChars.
	MaxChars int

	once          sync.Once
	defaultClient *http.Client
}

// Page is the outcome of fetching one URL.
type Page struct {
	URL     string
	Content string
	Err     error
	// PDF is set when the page was a PDF and Content is its text layer.
	PDF bool
}

func (f *Fetcher) client() *http.Client {
	if f.Client != nil {
		return f.Client
	}
	f.once.Do(func() {
		jar, _ := cookiejar.New(nil) // only errors on a bad PublicSuffixList; nil is fine
		f.defaultClient = &http.Client{Timeout: DefaultTimeout, Jar: jar}
	})
	return f.defaultClient
}

func (f *Fetcher) maxChars() int {
	if f.MaxChars <= 0 {
		return DefaultMaxChars
	}
	return f.MaxChars
}

// Fetch downloads url and returns its text content, truncated to MaxChars.
// HTML is converted to text; plain text is passed through; PDFs yield their
// text layer; other content types are rejected. A Reddit challenge page is
// solved and the real page fetched in its place.
func (f *Fetcher) Fetch(ctx context.Context, url string) (string, error) {
	text, _, err := f.FetchPage(ctx, url)
	return text, err
}

// FetchPage is Fetch plus whether the page was a PDF.
func (f *Fetcher) FetchPage(ctx context.Context, url string) (string, bool, error) {
	mediaType, body, err := f.get(ctx, url)
	if err != nil {
		return "", false, err
	}
	if isHTML(mediaType) && isRedditChallenge(body) {
		u, err := f.solveURL(url, body)
		if err != nil {
			return "", false, err
		}
		if mediaType, body, err = f.get(ctx, u); err != nil {
			return "", false, err
		}
	}

	var text string
	isPDF := IsPDF(mediaType, body)
	switch {
	case isPDF:
		if text, err = PDFToText(body); err != nil {
			return "", true, err
		}
	case isHTML(mediaType):
		if text, err = HTMLToText(bytes.NewReader(body)); err != nil {
			return "", false, err
		}
	case strings.HasPrefix(mediaType, "text/"), mediaType == "application/json":
		text = normalizeText(string(body))
	default:
		return "", false, fmt.Errorf("unsupported content type %q", mediaType)
	}
	if text == "" {
		return "", isPDF, errors.New("no text content")
	}
	return Truncate(text, f.maxChars()), isPDF, nil
}

// get performs one GET and returns the response media type and body
// (capped at maxBodyBytes). Non-2xx statuses are errors.
func (f *Fetcher) get(ctx context.Context, url string) (string, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/pdf,text/plain;q=0.9,*/*;q=0.5")

	resp, err := f.client().Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return "", nil, fmt.Errorf("timed out: %w", err)
		}
		return "", nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	mediaType, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return "", nil, fmt.Errorf("read body: %w", err)
	}
	return mediaType, body, nil
}

// solveURL turns a challenge page fetched from rawURL into the URL to fetch
// instead.
func (f *Fetcher) solveURL(rawURL string, body []byte) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("parse url: %w", err)
	}
	return solveRedditChallenge(u, body)
}

func isHTML(mediaType string) bool {
	return mediaType == "" || strings.Contains(mediaType, "html") || strings.Contains(mediaType, "xml")
}

// FetchAll fetches every URL with bounded concurrency. The returned slice is
// aligned with urls; per-URL failures are recorded in Page.Err.
func (f *Fetcher) FetchAll(ctx context.Context, urls []string, concurrency int) []Page {
	if concurrency <= 0 {
		concurrency = DefaultConcurrency
	}
	pages := make([]Page, len(urls))
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	for i, u := range urls {
		pages[i].URL = u
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, u string) {
			defer wg.Done()
			defer func() { <-sem }()
			pages[i].Content, pages[i].PDF, pages[i].Err = f.FetchPage(ctx, u)
		}(i, u)
	}
	wg.Wait()
	return pages
}

// skipElements are dropped along with their contents.
var skipElements = map[string]bool{
	"script": true, "style": true, "noscript": true, "head": true,
	"svg": true, "iframe": true, "canvas": true, "object": true, "embed": true,
}

// blockElements start on a new line; "paragraph" ones also end a paragraph.
var blockElements = map[string]bool{
	"address": true, "article": true, "aside": true, "blockquote": true, "br": true,
	"dd": true, "details": true, "dialog": true, "div": true, "dl": true, "dt": true,
	"fieldset": true, "figcaption": true, "figure": true, "footer": true, "form": true, "template": true,
	"h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true,
	"header": true, "hr": true, "li": true, "main": true, "nav": true, "ol": true,
	"p": true, "pre": true, "section": true, "table": true, "tbody": true, "td": true,
	"tfoot": true, "th": true, "thead": true, "tr": true, "ul": true, "summary": true,
}

var paragraphElements = map[string]bool{
	"article": true, "blockquote": true, "div": true, "figure": true, "footer": true,
	"h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true,
	"header": true, "hr": true, "li": true, "main": true, "nav": true, "p": true,
	"pre": true, "section": true, "table": true, "ul": true, "ol": true, "dl": true,
}

// HTMLToText strips tags, scripts, and styles, keeping block structure as
// line and paragraph breaks.
func HTMLToText(r io.Reader) (string, error) {
	doc, err := html.Parse(r)
	if err != nil {
		return "", fmt.Errorf("parse HTML: %w", err)
	}
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		switch n.Type {
		case html.TextNode:
			b.WriteString(n.Data)
			return
		case html.CommentNode, html.DoctypeNode:
			return
		case html.ElementNode:
			if skipElements[n.Data] {
				return
			}
			// MathML is pretty-printed, so its whitespace text nodes would
			// split a formula into one token per line, and its TeX
			// annotation would repeat it. The alttext is the whole formula
			// as compact TeX, so emit that inline instead.
			if n.Data == "math" {
				if tex := mathText(n); tex != "" {
					b.WriteString(" " + tex + " ")
				}
				return
			}
			// Void elements have no children, so emit their break once.
			switch n.Data {
			case "br":
				b.WriteString("\n")
				return
			case "hr":
				b.WriteString("\n\n")
				return
			}
			if blockElements[n.Data] {
				if paragraphElements[n.Data] {
					b.WriteString("\n\n")
				} else {
					b.WriteString("\n")
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
		if n.Type == html.ElementNode && blockElements[n.Data] {
			if paragraphElements[n.Data] {
				b.WriteString("\n\n")
			} else {
				b.WriteString("\n")
			}
		}
	}
	walk(doc)
	return normalizeText(b.String()), nil
}

// mathText returns a <math> element's formula: its alttext, else its TeX
// annotation, with MediaWiki's {\displaystyle ...} wrapper removed.
func mathText(n *html.Node) string {
	tex := ""
	for _, a := range n.Attr {
		if a.Key == "alttext" {
			tex = a.Val
		}
	}
	if tex == "" {
		var find func(*html.Node)
		find = func(c *html.Node) {
			if c.Type == html.ElementNode && c.Data == "annotation" && c.FirstChild != nil {
				tex = c.FirstChild.Data
				return
			}
			for k := c.FirstChild; k != nil && tex == ""; k = k.NextSibling {
				find(k)
			}
		}
		find(n)
	}
	tex = strings.Join(strings.Fields(tex), " ")
	if inner, ok := strings.CutPrefix(tex, "{\\displaystyle "); ok {
		tex = strings.TrimSuffix(inner, "}")
	}
	return tex
}

var (
	multiSpace   = regexp.MustCompile(`[ \t\r\f\v\x{00A0}]+`)
	spaceAroundN = regexp.MustCompile(` *\n *`)
	multiNewline = regexp.MustCompile(`\n{3,}`)
)

// normalizeText collapses runs of spaces, trims line edges, and limits blank
// runs to a single empty line so paragraphs stay distinguishable.
func normalizeText(s string) string {
	s = multiSpace.ReplaceAllString(s, " ")
	s = spaceAroundN.ReplaceAllString(s, "\n")
	s = multiNewline.ReplaceAllString(s, "\n\n")
	return strings.TrimSpace(s)
}

// Truncate cuts s to at most max runes, preferring a paragraph or line
// boundary in the final quarter of the budget and appending an ellipsis.
func Truncate(s string, max int) string {
	if max <= 0 {
		return s
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	cut := string(r[:max])
	floor := max * 3 / 4
	if i := strings.LastIndex(cut, "\n\n"); i >= floor {
		cut = cut[:i]
	} else if i := strings.LastIndex(cut, "\n"); i >= floor {
		cut = cut[:i]
	} else if i := strings.LastIndex(cut, " "); i >= floor {
		cut = cut[:i]
	}
	return strings.TrimSpace(cut) + "…"
}
