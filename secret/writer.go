package secret

import (
	"bytes"
	"io"
	"strings"
	"sync"
)

// Writer redacts across arbitrary write boundaries while streaming safe output.
// Only a suffix that could still become a secret is retained until the next write.
type Writer struct {
	mu       sync.Mutex
	out      io.Writer
	redactor *Redactor
	pending  []byte
}

func NewWriter(out io.Writer, r *Redactor) *Writer { return &Writer{out: out, redactor: r} }
func (w *Writer) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.pending = append(w.pending, p...)
	if err := w.flush(false); err != nil {
		return 0, err
	}
	return len(p), nil
}
func (w *Writer) Flush() error { w.mu.Lock(); defer w.mu.Unlock(); return w.flush(true) }
func (w *Writer) flush(final bool) error {
	var output strings.Builder
	r := w.redactor
	if r != nil {
		r.mu.Lock()
	}
	i := 0
	for i < len(w.pending) {
		rest := w.pending[i:]
		match := 0
		partial := false
		if r != nil {
			for _, v := range r.values {
				value := v.data
				if len(value) == 0 {
					continue
				}
				if !final && len(rest) < len(value) && bytes.HasPrefix(value, rest) {
					partial = true
					break
				}
				if match == 0 && bytes.HasPrefix(rest, value) {
					match = len(value)
				}
			}
		}
		if partial {
			break
		}
		if match > 0 {
			output.WriteString("[sensitive]")
			i += match
		} else {
			output.WriteByte(w.pending[i])
			i++
		}
	}
	if r != nil {
		r.mu.Unlock()
	}
	_, err := io.WriteString(w.out, output.String())
	remaining := append([]byte(nil), w.pending[i:]...)
	clear(w.pending)
	w.pending = remaining
	return err
}
