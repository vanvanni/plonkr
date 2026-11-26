package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/vanvanni/plonkr/cmd"
)

func main() {
	os.Exit(run())
}

// run owns every defer: os.Exit skips them, so main exits only after run returns.
func run() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	err := cmd.New(os.Stdin, os.Stdout, os.Stderr).ExecuteContext(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "plonk:", cmd.ErrorText(err))
	}
	return cmd.ExitCode(err)
}
