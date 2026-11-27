package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/vanvanni/plonkr/profile"
	"github.com/vanvanni/plonkr/ui"
)

const maxProfileFileSize = 1 << 20

func newValidateCmd() *cobra.Command {
	var profilesDir string
	var arguments []string
	cmd := &cobra.Command{
		Use:   "validate PROFILE",
		Short: "Validate a profile and print its expanded plan",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			flagChanged := cmd.Flags().Changed("profiles-dir")
			if flagChanged && strings.TrimSpace(profilesDir) == "" {
				return errors.New("--profiles-dir must not be empty")
			}
			program, err := loadProgramRef(args[0], profilesDir, flagChanged)
			if err != nil {
				return err
			}
			values, err := parseArgumentOptions(program, arguments)
			if err != nil {
				return err
			}
			missing := []string{}
			for _, a := range program.Arguments() {
				if _, ok := values[a.Name]; !ok && a.Default == nil {
					missing = append(missing, a.Name)
				}
			}
			if len(missing) > 0 {
				_, err := fmt.Fprintf(cmd.OutOrStdout(), "Argument contracts checked. Unresolved inputs: %s\nSupply --arg name=value to validate the fully resolved plan.\n", strings.Join(missing, ", "))
				return err
			}
			plan, err := program.Bind(values)
			if err != nil {
				return err
			}
			return writePlanSummary(cmd.OutOrStdout(), plan)
		},
	}
	cmd.Flags().StringVar(&profilesDir, "profiles-dir", "", "Directory containing profiles, snippets, and unit files")
	cmd.Flags().StringArrayVar(&arguments, "arg", nil, "Supply a profile argument as name=value (repeatable)")
	return cmd
}

func loadProfileRef(ref, profilesDir string, explicitDir bool) (*profile.Plan, error) {
	program, err := loadProgramRef(ref, profilesDir, explicitDir)
	if err != nil {
		return nil, err
	}
	return program.Bind(nil)
}

func loadProgramRef(ref, profilesDir string, explicitDir bool) (*profile.Program, error) {
	if explicitDir {
		base, err := filepath.Abs(profilesDir)
		if err != nil {
			return nil, fmt.Errorf("resolve profiles directory %q: %w", profilesDir, err)
		}
		info, err := os.Stat(base)
		if err != nil {
			return nil, fmt.Errorf("open profiles directory %q: %w", base, err)
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("profiles directory %q is not a directory", base)
		}
		if isExplicitPath(ref) {
			return prepareProfileFile(ref, base)
		}
		return profile.PrepareProfile(ref, base)
	}

	if isExplicitPath(ref) {
		return prepareProfileFile(ref, filepath.Dir(ref))
	}

	base, err := profile.DefaultProfilesDir()
	if err != nil {
		return nil, err
	}
	return profile.PrepareProfile(ref, base)
}

func prepareProfileFile(path, base string) (*profile.Program, error) {
	source, err := readRegularProfile(path)
	if err != nil {
		return nil, err
	}
	return profile.Prepare(source, path, base)
}

func isExplicitPath(ref string) bool {
	return filepath.IsAbs(ref) || strings.Contains(ref, `\`) || strings.HasSuffix(ref, ".hcl") ||
		strings.HasPrefix(ref, "./") || strings.HasPrefix(ref, "../")
}

func parseProfileFile(path string) (*profile.Plan, error) {
	source, err := readRegularProfile(path)
	if err != nil {
		return nil, err
	}
	return profile.Parse(source, path)
}

func parseProfileFileWithBase(path, base string) (*profile.Plan, error) {
	source, err := readRegularProfile(path)
	if err != nil {
		return nil, err
	}
	return profile.ParseWithBase(source, path, base)
}

func readRegularProfile(path string) ([]byte, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("read profile %q: %w", path, err)
	}
	if !before.Mode().IsRegular() {
		return nil, fmt.Errorf("profile %q must be a regular file, not a symlink or directory", path)
	}
	if before.Size() > maxProfileFileSize {
		return nil, fmt.Errorf("profile %q exceeds 1 MiB limit", path)
	}
	file, err := openReadOnly(path)
	if err != nil {
		return nil, fmt.Errorf("read profile %q: %w", path, err)
	}
	defer file.Close()

	after, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat profile %q: %w", path, err)
	}
	if !os.SameFile(before, after) || !after.Mode().IsRegular() {
		return nil, fmt.Errorf("profile %q changed during read or is not a regular file", path)
	}
	if after.Size() > maxProfileFileSize {
		return nil, fmt.Errorf("profile %q exceeds 1 MiB limit", path)
	}
	data, err := io.ReadAll(io.LimitReader(file, maxProfileFileSize+1))
	if err != nil {
		return nil, fmt.Errorf("read profile %q: %w", path, err)
	}
	if len(data) > maxProfileFileSize {
		return nil, fmt.Errorf("profile %q exceeds 1 MiB limit", path)
	}
	return data, nil
}

func sanitizeSingleLine(s string) string {
	return strings.Join(strings.Fields(ui.MessageText(s, "", false)), " ")
}

func writePlanSummary(out io.Writer, plan *profile.Plan) error {
	name := sanitizeSingleLine(plan.Name)
	if name == "" {
		name = "(unnamed)"
	}
	if _, err := fmt.Fprintf(out, "Profile: %s\n", name); err != nil {
		return err
	}
	if flags := plan.FlagNames(); len(flags) > 0 {
		if _, err := fmt.Fprintf(out, "Flags: --%s\n", strings.Join(flags, ", --")); err != nil {
			return err
		}
	}
	for _, task := range plan.Tasks {
		label := fmt.Sprintf("[%d]", task.Order)
		if task.Baseline {
			label = "[config]"
		}
		suffix := ""
		if task.Bypass {
			suffix = " (bypass)"
		}
		if !task.Lock.IsZero() {
			suffix += " (lock)"
		}
		if _, err := fmt.Fprintf(out, "\n%s %s%s\n", label, sanitizeSingleLine(task.Name), suffix); err != nil {
			return err
		}
		for _, step := range task.Steps {
			flagSuffix := ""
			if len(step.Flags) > 0 {
				flagSuffix = fmt.Sprintf(" [flags: --%s]", strings.Join(step.Flags, ", --"))
			}
			if _, err := fmt.Fprintf(out, "  [%s] %s%s\n", step.Phase, sanitizeSingleLine(step.Text), flagSuffix); err != nil {
				return err
			}
		}
	}
	return nil
}
