// Package main provides the unified flexagent binary with serve subcommands.
//
// Usage:
//
//	flexagent serve agent          — runs the agent loop server
//	flexagent serve sandbox-host   — runs the sandbox-host service
//	flexagent serve all            — runs agent + sandbox-host in-process
//	flexagent serve orchestrator   — runs the orchestrator control-plane
package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "serve":
		if len(os.Args) < 3 {
			fmt.Fprintln(os.Stderr, "error: 'serve' requires a subcommand: agent, sandbox-host, all, orchestrator")
			printUsage()
			os.Exit(1)
		}
		// Strip "flexagent serve" so the subcommand sees its own flags in os.Args.
		subArgs := os.Args[3:]
		switch os.Args[2] {
		case "agent":
			runServeAgent(subArgs)
		case "sandbox-host":
			runServeSandboxHost(subArgs)
		case "all":
			runServeAll(subArgs)
		case "orchestrator":
			runServeOrchestrator(subArgs)
		default:
			fmt.Fprintf(os.Stderr, "error: unknown serve subcommand %q\n", os.Args[2])
			printUsage()
			os.Exit(1)
		}
	case "help", "--help", "-h":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "error: unknown command %q\n", os.Args[1])
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Fprintln(os.Stderr, `Usage: flexagent <command> [flags]

Commands:
  serve agent          Start the agent loop RPC server
  serve sandbox-host   Start the sandbox-host service
  serve all            Start agent + sandbox-host in a single process
  serve orchestrator   Start the orchestrator

  help                 Show this help message

Run 'flexagent serve <subcommand> --help' for subcommand flags.`)
}
