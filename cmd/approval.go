package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/muesli/cancelreader"
	"github.com/vanvanni/plonkr/profile"
)

func approveKeyReplacement(ctx context.Context, repo profile.Repository, in io.Reader, out io.Writer, auto bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(out, "Warning: repository %q has a different deploy key at user %q's ~/.ssh/plonkr-%s.key. Replacement keeps that filename.\n", repo.Name, repo.User, repo.Name); err != nil {
		return err
	}
	if auto {
		_, err := fmt.Fprintln(out, "Replacement approved by --auto-approve.")
		return err
	}
	if _, file := in.(*os.File); file && !terminalReader(in) {
		return errors.New("key replacement not approved; requires a terminal or --auto-approve")
	}
	if _, err := fmt.Fprint(out, "Replace this key? [y/N]: "); err != nil {
		return err
	}
	reader, err := cancelreader.NewReader(in)
	if err != nil {
		return fmt.Errorf("prepare approval input: %w", err)
	}
	defer reader.Close()
	done, joined := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(joined)
		select {
		case <-ctx.Done():
			reader.Cancel()
		case <-done:
		}
	}()
	// Read one byte at a time so one approval cannot consume the next answer
	// (or input intended for a later --tty command).
	var answer []byte
	var readErr error
	var one [1]byte
	for len(answer) <= 16 {
		var n int
		n, readErr = reader.Read(one[:])
		if n > 0 {
			if one[0] == 3 {
				readErr = context.Canceled
				break
			}
			if one[0] == 4 {
				readErr = io.EOF
				break
			}
			if one[0] == '\r' || one[0] == '\n' {
				break
			}
			if one[0] == 8 || one[0] == 127 {
				if len(answer) > 0 {
					answer = answer[:len(answer)-1]
				}
			} else {
				answer = append(answer, one[0])
			}
		}
		if readErr != nil || (n > 0 && one[0] == '\n') {
			break
		}
	}
	close(done)
	<-joined
	if _, err := fmt.Fprintln(out); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if readErr == context.Canceled {
		return readErr
	}
	if readErr != nil {
		return errors.New("key replacement not approved; supply an answer or use --auto-approve")
	}
	switch strings.ToLower(strings.TrimSpace(string(answer))) {
	case "y", "yes":
		return nil
	default:
		return errors.New("key replacement declined; target key was preserved")
	}
}
