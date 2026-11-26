package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"plonk/cmd"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := cmd.New(os.Stdin, os.Stdout, os.Stderr).ExecuteContext(ctx)
	stop()
	if err != nil {
		fmt.Fprintln(os.Stderr, "plonk:", err)
	}
	os.Exit(cmd.ExitCode(err))
}
