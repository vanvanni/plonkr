package cmd

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	"github.com/vanvanni/plonkr/profile"
)

func TestKeyReplacementApproval(t *testing.T) {
	pipe, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer pipe.Close()
	defer writer.Close()
	if err := approveKeyReplacement(t.Context(), profile.Repository{}, pipe, &bytes.Buffer{}, false); err == nil {
		t.Fatal("nonterminal input must fail instead of waiting for approval")
	}
	for _, tc := range []struct {
		name, input   string
		auto, allowed bool
	}{
		{"yes", "yes\n", false, true}, {"no", "n\n", false, false},
		{"default no", "\n", false, false}, {"EOF", "", false, false},
		{"automatic", "", true, true},
		{"Windows enter", "yes\r", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			err := approveKeyReplacement(t.Context(), profile.Repository{Name: "app", User: "deploy"}, strings.NewReader(tc.input), &out, tc.auto)
			if (err == nil) != tc.allowed {
				t.Fatalf("allowed=%v err=%v", tc.allowed, err)
			}
			if !strings.Contains(out.String(), "Warning:") {
				t.Fatal("missing replacement warning")
			}
		})
	}
	input := strings.NewReader("yes\nno\n")
	if err := approveKeyReplacement(t.Context(), profile.Repository{}, input, &bytes.Buffer{}, false); err != nil {
		t.Fatal(err)
	}
	if err := approveKeyReplacement(t.Context(), profile.Repository{}, input, &bytes.Buffer{}, false); err == nil {
		t.Fatal("second approval consumed the wrong answer")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := approveKeyReplacement(ctx, profile.Repository{}, strings.NewReader("yes\n"), &bytes.Buffer{}, false); err != context.Canceled {
		t.Fatalf("cancelled approval: %v", err)
	}
}
