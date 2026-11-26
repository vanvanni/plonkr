package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"
	"golang.org/x/term"
	"plonk/profile"
	"plonk/provision"
	"plonk/runlog"
	"plonk/runner"
	"plonk/ui"
)

func newRunCmd() *cobra.Command {
	return &cobra.Command{
		Use:                "run HOST PROFILE [flags]",
		Short:              "Execute a profile against an existing machine over SSH",
		DisableFlagParsing: true,
		Long: `Execute an expanded profile against an existing machine over OpenSSH.

Built-in flags:
  --dry-run             Preview planned, skipped, and bypassed steps without SSH
  --tty                 Allocate a remote terminal for interactive prompts (disables TUI)
  --no-tui              Use plain streaming output instead of the terminal UI
  --profiles-dir DIR    Override the profiles base directory
  --bypass NAME         Bypass a named task (repeatable, exact match)

Profile flags discovered from flag("name", ...) are added dynamically.
Run "plonk run HOST PROFILE --help" to inspect flags for a specific profile.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
				return cmd.Help()
			}
			if len(args) < 2 {
				return errors.New("usage: plonk run HOST PROFILE [flags]")
			}
			host, profileRef := args[0], args[1]
			if err := runner.ValidateHost(host); err != nil {
				return err
			}
			profilesDir, explicitDir, err := scanProfilesDir(args[2:])
			if err != nil {
				return err
			}
			plan, err := loadProfileRef(profileRef, profilesDir, explicitDir)
			if err != nil {
				return err
			}
			return executeProfile(cmd.Context(), host, profileRef, plan, args[2:], cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
}

func scanProfilesDir(args []string) (string, bool, error) {
	var dir string
	var set bool
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			break
		}
		switch {
		case arg == "--profiles-dir":
			if i+1 >= len(args) {
				return "", false, errors.New("--profiles-dir requires a value")
			}
			dir = args[i+1]
			set = true
			i++
		case strings.HasPrefix(arg, "--profiles-dir="):
			dir = strings.TrimPrefix(arg, "--profiles-dir=")
			set = true
		}
	}
	if set && strings.TrimSpace(dir) == "" {
		return "", false, errors.New("--profiles-dir must not be empty")
	}
	return dir, set, nil
}

func executeProfile(ctx context.Context, host, profileRef string, plan *profile.Plan, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	var dryRun, tty, noTUI bool
	var profilesDir string
	var bypassList []string

	runCmd := &cobra.Command{
		Use:           "plonk run " + host + " " + profileRef + " [flags]",
		Short:         "Execute " + profileRef + " on " + host,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	runCmd.SetIn(stdin)
	runCmd.SetOut(stdout)
	runCmd.SetErr(stderr)
	runCmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview planned, skipped, and bypassed steps without connecting")
	runCmd.Flags().BoolVar(&tty, "tty", false, "Allocate a remote terminal for interactive SSH/sudo; disables TUI")
	runCmd.Flags().BoolVar(&noTUI, "no-tui", false, "Use plain streaming output")
	runCmd.Flags().StringVar(&profilesDir, "profiles-dir", "", "Override the profiles base directory")
	runCmd.Flags().StringArrayVar(&bypassList, "bypass", nil, "Bypass a named task (repeatable, case-sensitive)")

	flagNames := plan.FlagNames()
	for _, name := range flagNames {
		runCmd.Flags().Bool(name, false, `Enable steps gated by flag("`+name+`", ...)`)
	}

	runCmd.RunE = func(cmd *cobra.Command, _ []string) error {
		enabled := make(map[string]bool, len(flagNames))
		for _, name := range flagNames {
			enabled[name], _ = cmd.Flags().GetBool(name)
		}
		bypassTasks, err := validateBypassTasks(plan, bypassList)
		if err != nil {
			return err
		}

		keys := make(map[string][]byte)
		defer func() {
			for _, key := range keys {
				clear(key)
			}
		}()

		if !dryRun {
			needed := make(map[string]struct{})
			for _, task := range plan.Tasks {
				if task.Bypass || bypassTasks[task.Name] {
					continue
				}
				for _, step := range task.Steps {
					if step.Op != profile.OpRepo || len(step.MissingFlags(enabled)) > 0 {
						continue
					}
					repo, ok := plan.Repositories[step.Resource]
					if !ok {
						return fmt.Errorf("unknown repository %q", step.Resource)
					}
					if repo.Key != "" {
						needed[repo.Key] = struct{}{}
					}
				}
			}
			for _, keyPath := range slices.Sorted(maps.Keys(needed)) {
				key, err := ReadKeySource(keyPath)
				if err != nil {
					return fmt.Errorf("load deploy key %q: %w", keyPath, err)
				}
				keys[keyPath] = key
			}
		}

		logDir, err := profile.DefaultLogDir()
		if err != nil {
			return err
		}
		journal, err := runlog.Open(logDir, host, profileRef, plan)
		if err != nil {
			return err
		}

		ssh := runner.SSH{Host: host, TTY: tty}
		if tty {
			ssh.Stdin = stdin
		}
		executor := provision.Executor{SSH: ssh, Plan: plan, Keys: keys}
		opts := runner.Options{
			DryRun:      dryRun,
			Flags:       enabled,
			BypassTasks: bypassTasks,
		}

		work := func(workCtx context.Context, emit func(runner.Event)) error {
			workCtx, cancel := context.WithCancel(workCtx)
			defer cancel()

			var logErr error
			_, runErr := runner.Run(workCtx, plan, opts, executor.Execute, func(e runner.Event) {
				if logErr != nil {
					return
				}
				var clean runner.Event
				clean, logErr = journal.Event(e)
				if logErr != nil {
					cancel()
					return
				}
				if emit != nil {
					emit(clean)
				}
			})
			if logErr != nil {
				return errors.Join(runErr, logErr)
			}
			return runErr
		}

		var runErr error
		if !dryRun && !tty && !noTUI && terminalReader(stdin) && terminalWriter(stdout) {
			runErr = ui.Run(cmd.Context(), host, plan, work, tea.WithInput(stdin), tea.WithOutput(stdout), tea.WithAltScreen())
		} else {
			printer := plainPrinter{plan: plan, dryRun: dryRun, stdout: stdout, stderr: stderr, last: stdout, lineStart: true}
			runErr = work(cmd.Context(), printer.emit)
			printer.newline()
		}

		logErr := journal.Finish(runErr, dryRun)
		combined := errors.Join(runErr, logErr)
		fmt.Fprintln(stdout)
		fmt.Fprint(stdout, journal.Summary(combined, dryRun))
		return combined
	}

	runCmd.SetArgs(args)
	return runCmd.ExecuteContext(ctx)
}

type plainPrinter struct {
	plan           *profile.Plan
	dryRun         bool
	stdout, stderr io.Writer
	last           io.Writer
	lineStart      bool
}

func (p *plainPrinter) newline() {
	if !p.lineStart {
		fmt.Fprintln(p.last)
		p.lineStart = true
	}
}

func (p *plainPrinter) emit(e runner.Event) {
	if e.Kind == runner.Output {
		if e.Text == "" {
			return
		}
		target := p.stdout
		if e.Stderr {
			target = p.stderr
		}
		if !p.lineStart && p.last != target {
			p.newline()
		}
		p.last = target
		fmt.Fprint(target, e.Text)
		p.lineStart = strings.HasSuffix(e.Text, "\n")
		return
	}

	p.newline()
	switch e.Kind {
	case runner.TaskStarted:
		if e.Task < 0 || e.Task >= len(p.plan.Tasks) {
			return
		}
		task := p.plan.Tasks[e.Task]
		name := strings.Join(strings.Fields(e.Text), " ")
		if task.Baseline {
			fmt.Fprintf(p.stdout, "\n[config] %s\n", name)
		} else {
			fmt.Fprintf(p.stdout, "\n[%d] %s\n", task.Order, name)
		}
	case runner.StepStarted:
		if p.dryRun {
			return
		}
		if e.Task >= 0 && e.Task < len(p.plan.Tasks) && e.Step >= 0 && e.Step < len(p.plan.Tasks[e.Task].Steps) {
			if p.plan.Tasks[e.Task].Steps[e.Step].Op == profile.OpLog {
				return
			}
		}
		fmt.Fprintf(p.stdout, "  $ %s\n", e.Text)
	case runner.Message:
		fmt.Fprintln(p.stdout, ui.MessageText(e.Text, e.Color, terminalWriter(p.stdout)))
	case runner.StepDone:
		switch e.Status {
		case runner.StatusPlanned, runner.StatusSkipped, runner.StatusBypassed:
			if e.Reason != "" {
				fmt.Fprintf(p.stdout, "  [%s] (%s) %s\n", e.Status, e.Reason, e.Text)
			} else {
				fmt.Fprintf(p.stdout, "  [%s] %s\n", e.Status, e.Text)
			}
		}
	}
}

func terminalReader(r io.Reader) bool {
	f, ok := r.(*os.File)
	return ok && term.IsTerminal(int(f.Fd()))
}

func terminalWriter(w io.Writer) bool {
	f, ok := w.(*os.File)
	return ok && term.IsTerminal(int(f.Fd()))
}

// validateBypassTasks verifies that every --bypass argument matches a task name in plan.
func validateBypassTasks(plan *profile.Plan, names []string) (map[string]bool, error) {
	if len(names) == 0 {
		return nil, nil
	}
	known := make(map[string]bool, len(plan.Tasks))
	available := make([]string, 0, len(plan.Tasks))
	for _, task := range plan.Tasks {
		if !known[task.Name] {
			known[task.Name] = true
			available = append(available, fmt.Sprintf("%q", task.Name))
		}
	}
	bypass := make(map[string]bool, len(names))
	for _, name := range names {
		if !known[name] {
			return nil, fmt.Errorf("unknown --bypass task %q (available: %s)", name, strings.Join(available, ", "))
		}
		bypass[name] = true
	}
	return bypass, nil
}

// ExitCode maps an execution error to a conventional CLI process exit code.
func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	if errors.Is(err, context.Canceled) {
		return 130
	}
	if exitErr, ok := errors.AsType[*exec.ExitError](err); ok && exitErr.ExitCode() > 0 {
		return exitErr.ExitCode()
	}
	return 1
}
