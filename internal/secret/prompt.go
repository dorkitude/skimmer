package secret

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"
)

// ErrCancelled is returned when the user aborts input with Ctrl-C or Ctrl-D.
var ErrCancelled = errors.New("input cancelled")

// stdin is a single shared buffered reader. Prompts must share one reader:
// otherwise the first prompt's buffer swallows lines meant for later prompts
// when input is piped.
var stdin = bufio.NewReader(os.Stdin)

// PromptMasked prints label, then reads a line from stdin echoing "*" for each
// character typed. Backspace is honored; Ctrl-C/Ctrl-D cancel. When stdin is not
// a terminal (e.g. piped input) it falls back to a plain line read.
func PromptMasked(label string) (string, error) {
	return promptMasked(os.Stdin, os.Stdout, label)
}

func promptMasked(in *os.File, out io.Writer, label string) (string, error) {
	fmt.Fprintf(out, "%s🔒 ", label)

	fd := int(in.Fd())
	if !term.IsTerminal(fd) {
		r := stdin
		if in != os.Stdin {
			r = bufio.NewReader(in)
		}
		line, err := readLine(r)
		if err != nil {
			return "", err
		}
		fmt.Fprintln(out)
		return line, nil
	}

	oldState, err := term.MakeRaw(fd)
	if err != nil {
		return "", fmt.Errorf("enter raw terminal mode: %w", err)
	}
	defer func() {
		_ = term.Restore(fd, oldState)
		fmt.Fprintln(out)
	}()

	var buf []byte
	b := make([]byte, 1)
	for {
		n, err := in.Read(b)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return "", ErrCancelled
			}
			return "", err
		}
		if n == 0 {
			continue
		}
		switch c := b[0]; c {
		case '\r', '\n':
			return strings.TrimSpace(string(buf)), nil
		case 3, 4: // Ctrl-C, Ctrl-D
			return "", ErrCancelled
		case 127, 8: // backspace
			if len(buf) > 0 {
				buf = buf[:len(buf)-1]
				fmt.Fprint(out, "\b \b")
			}
		case 21: // Ctrl-U clears the line
			for range buf {
				fmt.Fprint(out, "\b \b")
			}
			buf = buf[:0]
		default:
			if c < 32 { // ignore other control chars / escape sequences
				continue
			}
			buf = append(buf, c)
			fmt.Fprint(out, "*")
		}
	}
}

// PromptLine prints label and reads a plain (unmasked) line from stdin.
func PromptLine(label string) (string, error) {
	fmt.Print(label)
	return readLine(stdin)
}

// readLine reads one trimmed line. EOF with no data is reported as ErrCancelled.
func readLine(r *bufio.Reader) (string, error) {
	line, err := r.ReadString('\n')
	if err != nil {
		if errors.Is(err, io.EOF) {
			if line == "" {
				return "", ErrCancelled
			}
			return strings.TrimSpace(line), nil
		}
		return "", err
	}
	return strings.TrimSpace(line), nil
}
