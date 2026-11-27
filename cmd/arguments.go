package cmd

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"runtime"
	"strings"
	"unicode/utf8"

	"github.com/muesli/cancelreader"
	"github.com/vanvanni/plonkr/profile"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/convert"
)

func parseArgumentOptions(program *profile.Program, options []string) (map[string]cty.Value, error) {
	declarations := map[string]profile.Argument{}
	for _, a := range program.Arguments() {
		declarations[a.Name] = a
	}
	values := map[string]cty.Value{}
	for _, option := range options {
		name, text, ok := strings.Cut(option, "=")
		if !ok {
			return nil, fmt.Errorf("--arg requires name=value")
		}
		a, ok := declarations[name]
		if !ok {
			return nil, fmt.Errorf("unknown profile argument %q", name)
		}
		if _, duplicate := values[name]; duplicate {
			return nil, fmt.Errorf("duplicate --arg %q", name)
		}
		value, err := a.ParseValue(text)
		if err != nil {
			return nil, err
		}
		values[name] = value
	}
	return values, nil
}

// collectArguments owns one cancellable reader for the entire prompt session.
// Its returned cleanup joins the cancellation watcher before closing the reader.
func collectArguments(ctx context.Context, program *profile.Program, values map[string]cty.Value,
	in io.Reader, out io.Writer, interactive bool,
) (io.Reader, func(), error) {
	noop := func() {}
	if err := ctx.Err(); err != nil {
		return in, noop, err
	}
	missing := []string{}
	unanswered := 0
	for _, a := range program.Arguments() {
		if _, ok := values[a.Name]; !ok {
			unanswered++
		}
		if _, ok := values[a.Name]; !ok && a.Default == nil {
			missing = append(missing, a.Name)
		}
	}
	if !interactive {
		if len(missing) > 0 {
			return in, noop, fmt.Errorf("missing profile arguments: %s; supply --arg name=value (no terminal)", strings.Join(missing, ", "))
		}
		return in, noop, nil
	}
	if unanswered == 0 {
		return in, noop, nil
	}
	reader, err := cancelreader.NewReader(in)
	if err != nil {
		return in, noop, err
	}
	done, joined := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(joined)
		select {
		case <-ctx.Done():
			reader.Cancel()
		case <-done:
		}
	}()
	cleanup := func() { close(done); <-joined; reader.Close() }
	buffer := &argumentInput{Reader: bufio.NewReader(singleByteInput{Reader: reader})}
	var echo io.Writer = io.Discard
	if runtime.GOOS == "windows" && terminalReader(in) {
		echo = out
	}
	for _, a := range program.Arguments() {
		if _, ok := values[a.Name]; ok {
			continue
		}
		prompt := sanitizeSingleLine(a.Prompt)
		if a.Default != nil {
			text, _ := convert.Convert(*a.Default, cty.String)
			prompt += " [" + sanitizeSingleLine(text.AsString()) + "]"
		}
		for {
			if _, err := fmt.Fprint(out, prompt+": "); err != nil {
				cleanup()
				return in, noop, err
			}
			answer, err := readArgumentAnswer(buffer, echo)
			if ctx.Err() != nil {
				err = ctx.Err()
			}
			if err != nil {
				cleanup()
				return in, noop, fmt.Errorf("argument %q: %w", a.Name, err)
			}
			if answer == "" && a.Default != nil {
				values[a.Name] = *a.Default
				break
			}
			value, err := a.ParseValue(answer)
			if err != nil {
				if _, writeErr := fmt.Fprintln(out, ErrorText(err)); writeErr != nil {
					cleanup()
					return in, noop, writeErr
				}
				continue
			}
			values[a.Name] = value
			break
		}
	}
	// Restore console mode and release cancellation resources before SSH/TUI.
	// Preserve any bytes read ahead, including input intended for --tty.
	pending, _ := buffer.Peek(buffer.Buffered())
	rest := bytes.Clone(pending)
	cleanup()
	if terminalReader(in) && len(rest) == 0 {
		// Keep the real terminal descriptor available to Bubble Tea and later
		// key-approval cancellation. Console Enter is CR on Windows and LF on Unix.
		return in, noop, nil
	}
	buffer.Reader = bufio.NewReader(io.MultiReader(bytes.NewReader(rest), in))
	return buffer, noop, nil
}

// A prompt must not consume typeahead intended for SSH, the UI or key approval.
type singleByteInput struct{ io.Reader }

func (r singleByteInput) Read(p []byte) (int, error) {
	if len(p) > 1 {
		p = p[:1]
	}
	return r.Reader.Read(p)
}

type argumentInput struct {
	*bufio.Reader
	skipLF bool
}

func (r *argumentInput) ReadByte() (byte, error) {
	b, err := r.Reader.ReadByte()
	if r.skipLF {
		r.skipLF = false
		if b == '\n' && err == nil {
			return r.Reader.ReadByte()
		}
	}
	return b, err
}

func (r *argumentInput) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if !r.skipLF {
		return r.Reader.Read(p)
	}
	b, err := r.ReadByte()
	if err != nil {
		return 0, err
	}
	p[0] = b
	return 1, nil
}

// io.Copy (including subprocess stdin) must use Read's CRLF handling rather
// than the embedded bufio.Reader's WriteTo, which would forward the pending LF.
func (r *argumentInput) WriteTo(w io.Writer) (int64, error) {
	return io.Copy(w, struct{ io.Reader }{Reader: r})
}

func readArgumentAnswer(reader *argumentInput, echo io.Writer) (string, error) {
	answer := []byte{}
	for len(answer) <= 64<<10 {
		b, err := reader.ReadByte()
		if err != nil {
			return "", err
		}
		if b == 3 {
			return "", context.Canceled
		}
		if b == 4 {
			return "", io.EOF
		}
		if b == '\n' || b == '\r' {
			reader.skipLF = b == '\r'
			if _, err := io.WriteString(echo, "\r\n"); err != nil {
				return "", err
			}
			return string(answer), nil
		}
		if b == 8 || b == 127 {
			if len(answer) > 0 {
				_, width := utf8.DecodeLastRune(answer)
				answer = answer[:len(answer)-width]
				if _, err := io.WriteString(echo, "\b \b"); err != nil {
					return "", err
				}
			}
			continue
		}
		answer = append(answer, b)
		if _, err := echo.Write([]byte{b}); err != nil {
			return "", err
		}
	}
	return "", fmt.Errorf("answer exceeds 64 KiB")
}
