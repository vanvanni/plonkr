package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os/exec"
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"
	"github.com/vanvanni/plonkr/profile"
	"github.com/vanvanni/plonkr/provision"
	"github.com/vanvanni/plonkr/runlog"
	"github.com/vanvanni/plonkr/runner"
	"github.com/vanvanni/plonkr/ui"
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
  --force               Run tasks even when their lock would bypass them
  --auto-approve       Approve replacement of differing target deploy keys
  --arg NAME=VALUE      Supply a profile argument (repeatable)

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
			program, err := loadProgramRef(profileRef, profilesDir, explicitDir)
			if err != nil {
				return err
			}
			return executeProfile(cmd.Context(), host, profileRef, nil, args[2:], cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr(), program)
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

func executeProfile(ctx context.Context, host, profileRef string, plan *profile.Plan, args []string, stdin io.Reader, stdout, stderr io.Writer, programs ...*profile.Program) error {
	var dryRun, tty, noTUI, autoApprove, force bool
	var profilesDir string
	var bypassList []string
	var argumentOptions []string
	var program *profile.Program
	interactiveInput := terminalReader(stdin)
	if len(programs) > 0 {
		program = programs[0]
	}

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
	runCmd.Flags().BoolVar(&autoApprove, "auto-approve", false, "Approve replacement of differing target deploy keys without prompting")
	runCmd.Flags().BoolVar(&force, "force", false, "Run tasks even when their lock would bypass them")
	runCmd.Flags().StringVar(&profilesDir, "profiles-dir", "", "Override the profiles base directory")
	runCmd.Flags().StringArrayVar(&bypassList, "bypass", nil, "Bypass a named task (repeatable, case-sensitive)")
	runCmd.Flags().StringArrayVar(&argumentOptions, "arg", nil, "Supply a profile argument as name=value (repeatable)")

	flagNames := plan.FlagNames()
	if program != nil {
		flagNames = program.FlagNames()
	}
	for _, name := range flagNames {
		runCmd.Flags().Bool(name, false, `Enable steps gated by flag("`+name+`", ...)`)
	}

	runCmd.RunE = func(cmd *cobra.Command, _ []string) error {
		if program != nil {
			values, err := parseArgumentOptions(program, argumentOptions)
			if err != nil {
				return err
			}
			input, closeInput, err := collectArguments(cmd.Context(), program, values, stdin, stderr, terminalReader(stdin))
			if err != nil {
				return err
			}
			defer closeInput()
			stdin = input
			// Prompts finish before the execution UI. A buffered input is also kept
			// for --tty so a prompt cannot discard subsequent input.
			plan, err = program.Bind(values)
			if err != nil {
				return fmt.Errorf("preflight: %w; no SSH connection was made", err)
			}
		}
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
		executor := provision.Executor{SSH: ssh, Plan: plan, Keys: keys,
			ApproveKeyReplacement: func(ctx context.Context, repo profile.Repository) error {
				return approveKeyReplacement(ctx, repo, stdin, stderr, autoApprove)
			},
		}
		// Keep replacement warnings and prompts visible outside an alternate screen.
		if len(keys) > 0 {
			noTUI = true
		}
		opts := runner.Options{
			DryRun:      dryRun,
			Flags:       enabled,
			BypassTasks: bypassTasks,
			Force:       force,
			Locks:       executor,
		}

		work := func(parent context.Context, emit func(runner.Event)) error {
			workCtx, cancel := context.WithCancel(parent)
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
				// The cancellation was ours: report a log failure, not a user abort.
				if parent.Err() == nil && errors.Is(runErr, context.Canceled) {
					runErr = nil
				}
				return errors.Join(runErr, logErr)
			}
			return runErr
		}

		var runErr error
		if !dryRun && !tty && !noTUI && interactiveInput && terminalWriter(stdout) {
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

// ErrorText returns err's message with terminal control sequences removed.
// Errors can quote profile text, which must not be able to drive the terminal.
func ErrorText(err error) string {
	return ui.MessageText(err.Error(), "", false)
}
