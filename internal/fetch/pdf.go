package fetch

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"time"

	pdf "github.com/ledongthuc/pdf"
)

// ErrPDFNoText reports a valid PDF with no extractable text layer (a scan).
var ErrPDFNoText = errors.New("PDF contains no extractable text")

// pdftotextTimeout bounds the external converter.
const pdftotextTimeout = 20 * time.Second

// IsPDF reports whether a response is a PDF, by media type or by the file
// signature, since some hosts serve PDFs as octet-stream.
func IsPDF(mediaType string, body []byte) bool {
	return mediaType == "application/pdf" || bytes.HasPrefix(body, []byte("%PDF-"))
}

// PDFToText extracts the text layer of a PDF. Poppler's pdftotext is used
// when it is on PATH (best word spacing and column handling); otherwise the
// text is rebuilt from glyph positions with the pure-Go parser. Parser
// panics on malformed files are returned as errors.
func PDFToText(src []byte) (string, error) {
	if path, err := exec.LookPath("pdftotext"); err == nil {
		if text, err := pdftotext(path, src); err == nil {
			return text, nil
		}
	}
	return pdfToTextBuiltin(src)
}

// pdftotext runs poppler's converter on stdin, layout mode off so columns
// read in order.
func pdftotext(path string, src []byte) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), pdftotextTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "-enc", "UTF-8", "-", "-")
	cmd.Stdin = bytes.NewReader(src)
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("pdftotext: %w", err)
	}
	text := normalizeText(strings.ReplaceAll(out.String(), "\f", "\n\n"))
	if text == "" {
		return "", ErrPDFNoText
	}
	return text, nil
}

// pdfToTextBuiltin reads every page's glyph runs and joins them by
// position: a run that starts well to the right of where the previous one
// ended gets a space, a run on a lower line gets a newline, and a large
// vertical jump gets a paragraph break.
func pdfToTextBuiltin(src []byte) (text string, err error) {
	defer func() {
		if r := recover(); r != nil {
			text, err = "", fmt.Errorf("PDF extraction panicked: %v", r)
		}
	}()
	reader, err := pdf.NewReader(bytes.NewReader(src), int64(len(src)))
	if err != nil {
		return "", fmt.Errorf("open PDF: %w", err)
	}
	var b strings.Builder
	for i := 1; i <= reader.NumPage(); i++ {
		page := reader.Page(i)
		if page.V.IsNull() {
			continue
		}
		runs := page.Content().Text
		if len(runs) == 0 {
			continue
		}
		// Reading order: top to bottom (PDF y grows upward), then left to right.
		sort.SliceStable(runs, func(a, c int) bool {
			if d := runs[a].Y - runs[c].Y; d > 2 || d < -2 {
				return runs[a].Y > runs[c].Y
			}
			return runs[a].X < runs[c].X
		})
		var lastX, lastY, lastSize float64
		first := true
		for _, r := range runs {
			s := r.S
			if strings.TrimSpace(s) == "" {
				// A whitespace run is a space in its own right; fonts
				// without width tables (some Type1 standard fonts) give
				// every glyph the same X, so this is the only spacing
				// signal for them.
				if !first && !strings.HasSuffix(b.String(), " ") && !strings.HasSuffix(b.String(), "\n") {
					b.WriteString(" ")
				}
				continue
			}
			size := r.FontSize
			if size <= 0 {
				size = 10
			}
			switch {
			case first:
			case lastY-r.Y > size*1.8:
				b.WriteString("\n\n")
			case lastY-r.Y > 2 || r.Y-lastY > 2:
				b.WriteString("\n")
			case r.X-lastX > size*0.2:
				b.WriteString(" ")
			}
			b.WriteString(s)
			lastX, lastY, lastSize, first = r.X+r.W, r.Y, size, false
		}
		_ = lastSize
		b.WriteString("\n\n")
	}
	text = normalizeText(b.String())
	if text == "" {
		return "", ErrPDFNoText
	}
	return text, nil
}
