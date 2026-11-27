package profile

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/zclconf/go-cty/cty"
)

// ParseDuration checks the grammar; this restricts its units to seconds, minutes and hours.
var waitDurationPattern = regexp.MustCompile(`^[+0-9.smh]+$`)

func waitAction(op Op, args []cty.Value) (action, error) {
	minArgs, maxArgs := 1, 2
	if op == OpWait {
		maxArgs = 1
	}
	if op == OpWaitUntil {
		minArgs, maxArgs = 2, 3
	}
	if len(args) < minArgs || len(args) > maxArgs {
		return action{}, fmt.Errorf("%s() requires %d to %d string arguments", op, minArgs, maxArgs)
	}
	for _, arg := range args {
		if !isType(arg, cty.String) {
			return action{}, fmt.Errorf("%s() requires string arguments", op)
		}
	}
	step := Step{Op: op}
	if op == OpWaitUntil {
		step.Command = args[0].AsString()
		if strings.TrimSpace(step.Command) == "" || strings.ContainsRune(step.Command, 0) {
			return action{}, fmt.Errorf("wait_until command must be non-empty and contain no NUL bytes")
		}
		args = args[1:]
	}
	var err error
	step.Duration, err = waitDuration(args[0].AsString())
	if err != nil {
		return action{}, err
	}
	step.Text = string(op) + " " + step.Duration.String()
	if op == OpWaitUntil {
		step.Text = string(op) + " " + step.Command
	}
	if op != OpWait {
		step.Interval = 5 * time.Second
		if len(args) == 2 {
			step.Interval, err = waitDuration(args[1].AsString())
			if err != nil {
				return action{}, err
			}
		}
		if step.Interval > step.Duration {
			return action{}, fmt.Errorf("%s interval must not exceed timeout", op)
		}
	}
	return action{step: step}, nil
}

func waitDuration(value string) (time.Duration, error) {
	d, err := time.ParseDuration(value)
	validRange := d >= time.Second && d <= time.Hour
	validSyntax := waitDurationPattern.MatchString(value) && d%time.Second == 0
	if err != nil || !validRange || !validSyntax {
		return 0, fmt.Errorf("invalid wait duration %q: use s, m or h, whole seconds from 1s to 1h", value)
	}
	return d, nil
}
