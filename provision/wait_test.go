package provision

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/vanvanni/plonkr/profile"
)

type fakeWaitClock struct {
	now    time.Time
	cancel context.CancelFunc
}

func (c *fakeWaitClock) Now() time.Time { return c.now }
func (c *fakeWaitClock) Sleep(ctx context.Context, d time.Duration) error {
	c.now = c.now.Add(d)
	if c.cancel != nil {
		c.cancel()
		return ctx.Err()
	}
	return ctx.Err()
}

func TestWaitRetries(t *testing.T) {
	for _, tc := range []struct {
		name         string
		reasons      []string
		wantAttempts int
		wantError    string
	}{
		{"first", []string{""}, 1, ""},
		{"retry auth", []string{"Permission denied (publickey).", "Permission denied (publickey).", ""}, 3, ""},
		{"timeout", []string{"Connection refused"}, 3, "Connection refused"},
		{"changed reasons", []string{"refused", "denied", ""}, 3, ""},
		{"host key", []string{"Host key verification failed."}, 1, "ssh-keygen -R"},
		{"changed host key", []string{"REMOTE HOST IDENTIFICATION HAS CHANGED!"}, 1, "accept-new"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clock := &fakeWaitClock{}
			var out bytes.Buffer
			calls := 0
			err := executeWait(t.Context(), profile.Step{Op: profile.OpWaitSSH, Duration: 6 * time.Second, Interval: 2 * time.Second}, &out, clock,
				func(ctx context.Context, command string, remaining time.Duration, stdout, stderr io.Writer) error {
					if command != "true" || remaining != time.Duration(6-2*calls)*time.Second {
						t.Fatalf("bad attempt: %s %s", command, remaining)
					}
					if _, ok := ctx.Deadline(); !ok {
						t.Fatal("unbounded attempt")
					}
					reason := tc.reasons[min(calls, len(tc.reasons)-1)]
					calls++
					if reason == "" {
						return nil
					}
					fmt.Fprint(stderr, reason)
					return errors.New("exit status 255")
				})
			if calls != tc.wantAttempts {
				t.Fatalf("calls %d", calls)
			}
			if tc.wantError == "" {
				if err != nil || !strings.Contains(out.String(), "reachable after") {
					t.Fatalf("%v: %s", err, &out)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.wantError) {
				t.Fatalf("error: %v", err)
			}
			if tc.name == "retry auth" && strings.Count(out.String(), "Permission denied") != 1 {
				t.Fatal("repeated unchanged reason")
			}
		})
	}
}

func TestWaitCancellation(t *testing.T) {
	for _, during := range []string{"sleep", "attempt", "hard wait"} {
		t.Run(during, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			clock := &fakeWaitClock{}
			step := profile.Step{Op: profile.OpWaitUntil, Command: "false", Duration: 5 * time.Second, Interval: time.Second}
			if during != "attempt" {
				clock.cancel = cancel
			}
			if during == "hard wait" {
				step.Op = profile.OpWait
			}
			err := executeWait(ctx, step, nil, clock, func(ctx context.Context, command string, remaining time.Duration, _, _ io.Writer) error {
				if command != "false" {
					t.Fatal(command)
				}
				if during == "attempt" {
					cancel()
					<-ctx.Done()
				}
				return errors.New("not ready")
			})
			if !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
		})
	}
}

func TestHardWait(t *testing.T) {
	clock := &fakeWaitClock{}
	var out bytes.Buffer
	if err := executeWait(t.Context(), profile.Step{Op: profile.OpWait, Duration: time.Second}, &out, clock, nil); err != nil || out.String() != "waited 1s\n" {
		t.Fatalf("%v %s", err, &out)
	}
}

func TestWaitAttemptConsumesDeadline(t *testing.T) {
	clock := &fakeWaitClock{}
	step := profile.Step{Op: profile.OpWaitUntil, Command: "sleep 30", Duration: 5 * time.Second, Interval: time.Second}
	calls := 0
	err := executeWait(t.Context(), step, nil, clock,
		func(_ context.Context, _ string, remaining time.Duration, _, stderr io.Writer) error {
			calls++
			if remaining != 5*time.Second {
				t.Fatal(remaining)
			}
			clock.now = clock.now.Add(remaining)
			fmt.Fprint(stderr, "last reason")
			return context.DeadlineExceeded
		})
	if calls != 1 || err == nil || !strings.Contains(err.Error(), "last reason") {
		t.Fatalf("%d %v", calls, err)
	}
}

func TestWaitDiagnosticBounded(t *testing.T) {
	var diagnostic waitDiagnostic
	input := strings.Repeat("x", 100000) + "reason"
	if n, err := diagnostic.Write([]byte(input)); n != len(input) || err != nil {
		t.Fatalf("%d %v", n, err)
	}
	if len(diagnostic.text) != 4096 || !strings.HasSuffix(diagnostic.text, "reason") {
		t.Fatal("diagnostic tail lost or unbounded")
	}
}
