package main

import (
	"errors"
	"fmt"
	"os"
)

func main() {
	args, err := parseArgs()
	if err != nil {
		if errors.Is(err, errHelpRequested) {
			return
		}
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	owner, repo, err := getOwnerRepo()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	if err := dispatch(owner, repo, args); err != nil {
		if errors.Is(err, errTimeout) {
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func dispatch(owner, repo string, args parsedArgs) error {
	switch args.cmd {
	case cmdExport:
		return runExport(owner, repo, args.export)
	case cmdResolve:
		return runResolve(owner, repo, args.resolve)
	case cmdPending:
		return runPending(owner, repo, args.pending)
	case cmdWait:
		return runWait(owner, repo, args.wait)
	case cmdSubmit:
		return runSubmit(owner, repo, args.submit)
	case cmdSubmitPending:
		return runSubmitPending(owner, repo, args.submitPending)
	case cmdViewed:
		return runViewed(owner, repo, args.viewed)
	default:
		return fmt.Errorf("unknown command %q", args.cmd)
	}
}
