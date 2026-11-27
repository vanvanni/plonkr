package provision

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/vanvanni/plonkr/profile"
)

type waitClock interface {
	Now() time.Time
	Sleep(context.Context, time.Duration) error
}

type realWaitClock struct{}

func (realWaitClock) Now() time.Time { return time.Now() }
func (realWaitClock) Sleep(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

type waitAttempt func(context.Context, string, time.Duration, io.Writer, io.Writer) error

// Keep only a bounded diagnostic tail; readiness commands can produce unlimited output.
type waitDiagnostic struct{ text string }

func (b *waitDiagnostic) Write(p []byte) (int, error) {
	const limit = 4096
	n := len(p)
	if len(p) > limit {
		p = p[len(p)-limit:]
	}
	b.text += string(p)
	if len(b.text) > limit {
		b.text = b.text[len(b.text)-limit:]
	}
	return n, nil
}

func executeWait(
	ctx context.Context,
	step profile.Step,
	out io.Writer,
	clock waitClock,
	attempt waitAttempt,
) error {
	if out == nil {
		out = io.Discard
	}
	validDuration := step.Duration >= time.Second && step.Duration <= time.Hour
	if !validDuration || step.Duration%time.Second != 0 {
		return fmt.Errorf("invalid wait duration")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	start := clock.Now()
	if step.Op == profile.OpWait {
		if err := clock.Sleep(ctx, step.Duration); err != nil {
			return err
		}
		_, err := fmt.Fprintf(out, "waited %s\n", step.Duration)
		return err
	}
	validInterval := step.Interval >= time.Second && step.Interval <= step.Duration
	if !validInterval || step.Interval%time.Second != 0 {
		return fmt.Errorf("invalid wait interval")
	}
	command, label := step.Command, "condition satisfied"
	if step.Op == profile.OpWaitSSH {
		command, label = "true", "reachable"
	}
	deadline := start.Add(step.Duration)
	lastReason, attempts := "", 0
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		remaining := deadline.Sub(clock.Now())
		if remaining <= 0 {
			return fmt.Errorf("%s timed out after %s (%d attempts): %s", step.Op, step.Duration, attempts, lastReason)
		}
		attemptStart := clock.Now()
		attemptCtx, cancel := context.WithTimeout(ctx, remaining)
		var diagnostic waitDiagnostic
		err := attempt(attemptCtx, command, remaining, io.Discard, &diagnostic)
		cancel()
		attempts++
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err == nil && clock.Now().Before(deadline) {
			_, writeErr := fmt.Fprintf(out, "%s after %s (%d attempts)\n", label, clock.Now().Sub(start).Round(time.Second), attempts)
			return writeErr
		}
		reason := strings.TrimSpace(diagnostic.text)
		if reason == "" {
			reason = "attempt exceeded timeout"
			if err != nil {
				reason = err.Error()
			}
		}
		lower := strings.ToLower(reason)
		verificationFailed := strings.Contains(lower, "host key verification failed")
		identityChanged := strings.Contains(lower, "remote host identification has changed")
		keyChanged := strings.Contains(lower, "host key has changed")
		if verificationFailed || identityChanged || keyChanged {
			return fmt.Errorf("SSH host key verification failed: %s; verify the target identity, "+
				"use StrictHostKeyChecking accept-new for trusted new lab addresses, "+
				"or remove a verified stale key with ssh-keygen -R HOST for a recreated machine", reason)
		}
		if reason != lastReason {
			if _, err := fmt.Fprintf(out, "attempt %d: %s\n", attempts, reason); err != nil {
				return err
			}
			lastReason = reason
		}
		// Schedule from the attempt's start, with no overlapping attempts.
		delay := min(step.Interval-clock.Now().Sub(attemptStart), deadline.Sub(clock.Now()))
		if delay > 0 {
			if err := clock.Sleep(ctx, delay); err != nil {
				return err
			}
		}
	}
}
