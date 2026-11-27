package secret_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/vanvanni/plonkr/secret"
	"strings"
	"testing"
)

func TestRedaction(t *testing.T) {
	r := &secret.Redactor{}
	r.Add(secret.New("abc"))
	long := secret.New("abcdef")
	r.Add(long)
	if got := r.Text("abcdef abc"); got != "[sensitive] [sensitive]" {
		t.Fatal(got)
	}
	data, _ := json.Marshal(long)
	if strings.Contains(string(data), "abcdef") || strings.Contains(fmt.Sprintf("%+v %#v %s", long, long, long), "abcdef") {
		t.Fatal("format leak")
	}
	r.Clear()
	if long.Reveal() != strings.Repeat("\x00", 6) {
		t.Fatal("buffer not cleared")
	}
}

func TestStreamingRedaction(t *testing.T) {
	for _, value := range []string{"credential", "aba", "abc", "abcdef", "秘密"} {
		for split := 0; split <= len(value); split++ {
			r := &secret.Redactor{}
			r.Add(secret.New(value))
			var b bytes.Buffer
			w := secret.NewWriter(&b, r)
			w.Write([]byte("prompt: "))
			if b.String() != "prompt: " {
				t.Fatal("safe prompt delayed")
			}
			w.Write([]byte(value[:split]))
			w.Write([]byte(value[split:]))
			w.Flush()
			if b.String() != "prompt: [sensitive]" {
				t.Fatalf("%q split %d: %q", value, split, b.String())
			}
			r.Clear()
		}
	}
	r := &secret.Redactor{}
	r.Add(secret.New("abc"))
	r.Add(secret.New("abcdef"))
	defer r.Clear()
	var b bytes.Buffer
	w := secret.NewWriter(&b, r)
	w.Write([]byte("abc"))
	w.Write([]byte("def!"))
	w.Flush()
	if b.String() != "[sensitive]!" {
		t.Fatal(b.String())
	}
}
