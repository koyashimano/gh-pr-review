package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
)

type command string

const (
	cmdExport        command = "export"
	cmdResolve       command = "resolve"
	cmdPending       command = "pending"
	cmdWait          command = "wait"
	cmdSubmit        command = "submit"
	cmdSubmitPending command = "submit-pending"
)

type exportOptions struct {
	ctx             int
	includeResolved bool
	prNumber        *int
}

type resolveOptions struct {
	prNumber *int
}

type pendingOptions struct {
	ctx      int
	prNumber *int
}

type waitOptions struct {
	interval int
	timeout  int
	prNumber *int
}

type submitOptions struct {
	file     string
	pending  bool
	prNumber *int
}

type submitPendingOptions struct {
	event    string
	prNumber *int
}

type parsedArgs struct {
	cmd           command
	export        exportOptions
	resolve       resolveOptions
	pending       pendingOptions
	wait          waitOptions
	submit        submitOptions
	submitPending submitPendingOptions
}

var errHelpRequested = errors.New("help requested")

const shortUsage = "usage: gh-prr <export|resolve|pending|wait|submit|submit-pending> [args]"

const rootHelp = `gh-prr - Tools for working with GitHub pull request review threads

Usage:
  gh-prr <command> [flags] [pr_number]

Commands:
  export          Fetch review threads and print them as Markdown
  resolve         Resolve all unresolved review threads
  pending         Show your pending (unsubmitted) review comments
  wait            Poll a PR for new reviews and exit when one is detected
  submit          Submit a review from a Markdown file
  submit-pending  Submit your existing pending review

Global:
  -h, --help  Show this help

Run "gh-prr <command> -h" for command-specific help.
If pr_number is omitted, the PR for the current branch is used.
Prerequisite: the "gh" CLI must be installed and authenticated.`

const exportHelp = `Usage: gh-prr export [flags] [pr_number]

Fetch unresolved review threads on a pull request and print them as Markdown.
Resolved threads are skipped by default; pass --include-resolved to show them.

Flags:
  -c, --context int    Lines kept from the start and end of each diff hunk
                       (default 3, minimum 1)
      --include-resolved
                       Also include resolved threads in the output
  -h                   Show this help

If pr_number is omitted, the PR for the current branch is used.

Examples:
  gh-prr export
  gh-prr export 123
  gh-prr export -c 5 --include-resolved 123`

const resolveHelp = `Usage: gh-prr resolve [pr_number]

Resolve every unresolved review thread on the pull request.

Flags:
  -h  Show this help

If pr_number is omitted, the PR for the current branch is used.`

const pendingHelp = `Usage: gh-prr pending [flags] [pr_number]

Show your pending (unsubmitted) review comments as Markdown.

Flags:
  -c, --context int  Lines kept from the start and end of each diff hunk
                     (default 3, minimum 1)
  -h                 Show this help

If pr_number is omitted, the PR for the current branch is used.`

const waitHelp = `Usage: gh-prr wait [flags] [pr_number]

Poll the pull request until a new review is detected, then print its summary.

Flags:
  -i, --interval int  Polling interval in seconds (default 30, minimum 1)
  -t, --timeout int   Timeout in seconds (default 900 = 15 minutes, minimum 1)
  -h                  Show this help

Exits with status 1 if the timeout is reached before a new review appears.
If pr_number is omitted, the PR for the current branch is used.`

const submitHelp = `Usage: gh-prr submit -f <file> [--pending] [pr_number]

Submit a review from a single Markdown file. Use "-" to read from stdin.

Flags:
  -f, --file string  Path to the review Markdown file (use "-" for stdin, required)
      --pending      Save the review as a pending (draft) without finalizing
  -h                 Show this help

If pr_number is omitted, the PR for the current branch is used.

File format
-----------
A review file has three sections, in order:
  1. Optional front matter block.
  2. Optional review body (summary).
  3. Zero or more inline comment sections.

Either a body or at least one inline comment is required for COMMENT and
REQUEST_CHANGES. APPROVE may have an empty body. --pending may be empty.

Front matter
  If the very first line is "---", lines up to the next "---" are parsed as
  a small subset of YAML: one "key: value" per line, blanks and "#" lines
  ignored, surrounding single/double quotes stripped. Unknown keys error.

  Recognised keys:
    event   APPROVE | REQUEST_CHANGES | COMMENT (default COMMENT).
            Ignored when --pending is passed.
    commit  Commit SHA the review applies to. Defaults to the PR's HEAD.

Review body
  Everything between the end of front matter (or start of file) and the
  first inline comment header. Sent verbatim as the review summary, so
  Markdown formatting is preserved. Leading/trailing blank lines trimmed.

Inline comment headers
  Each inline comment starts with one of these H2 lines:

    ## <path>:<line>
    ## <path>:<start_line>-<line>
    ## <path>:<line> [side=LEFT]
    ## <path>:<start_line>-<line> [side=LEFT, start_side=LEFT]
    ## file: <path>

  Rules:
    - <path> must not contain ":". Forward slashes only.
    - For a range, start_line < line.
    - Attributes are comma-separated key=value pairs in brackets:
        side        LEFT | RIGHT (default RIGHT). LEFT targets a deleted
                    line on the pre-change side of the diff.
        start_side  LEFT | RIGHT (default same as side). Range only.
    - "## file: <path>" attaches the comment to the whole file. The
      "file:" prefix is literal and requires whitespace after it.
      Attribute lists and backslashes in the path are rejected.
    - Other H2 lines (e.g. "## Notes") become part of the surrounding
      comment body. Lines starting with "## file:" that don't match the
      exact pattern error rather than being treated as body.

Comment body
  Everything after a header up to the next header (or EOF). Leading and
  trailing blank lines trimmed. Empty bodies are rejected.

Submission
  GitHub allows at most one pending review per viewer per PR. submit
  fails fast if you already have one (use "gh-prr pending" to inspect,
  "gh-prr submit-pending" to finalize, or delete it via the GitHub UI).
  When file-level comments are present, the review is created pending,
  each file-level comment is attached via GraphQL, then the review is
  finalized. If a later step fails the review stays pending.

Example
  ---
  event: COMMENT
  ---

  Looks good overall. Two small notes below.

  ## src/handler.go:42

  Consider returning early here to reduce nesting.

  ## README.md:10-12

  This section could use a short example.`

const submitPendingHelp = `Usage: gh-prr submit-pending [-e EVENT] [pr_number]

Submit your existing pending (draft) review.

Flags:
  -e, --event string  APPROVE, REQUEST_CHANGES, or COMMENT (default "COMMENT")
  -h                  Show this help

If pr_number is omitted, the PR for the current branch is used.`

func parsePRArg(args []string) (*int, error) {
	if len(args) > 1 {
		return nil, fmt.Errorf("unexpected arguments: %v", args[1:])
	}
	if len(args) == 0 {
		return nil, nil
	}
	prNumber, err := strconv.Atoi(args[0])
	if err != nil {
		return nil, fmt.Errorf("invalid PR number: %q", args[0])
	}
	return &prNumber, nil
}

func newFlagSet(name string) (*flag.FlagSet, *bytes.Buffer) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	var buf bytes.Buffer
	fs.SetOutput(&buf)
	fs.Usage = func() {}
	return fs, &buf
}

func parseFlagSet(fs *flag.FlagSet, buf *bytes.Buffer, args []string, help string) error {
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(os.Stdout, help)
			return errHelpRequested
		}
		msg := strings.TrimSpace(buf.String())
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("%s\nrun \"gh-prr %s -h\" for help", msg, fs.Name())
	}
	return nil
}

func parseArgs() (parsedArgs, error) {
	var p parsedArgs

	if len(os.Args) < 2 {
		return p, errors.New(shortUsage + "\nrun \"gh-prr -h\" for help")
	}

	switch os.Args[1] {
	case string(cmdExport):
		fs, buf := newFlagSet("export")

		fs.IntVar(&p.export.ctx, "c", 3, "Number of lines to keep from the start/end of each diff hunk (alias: -context).")
		fs.IntVar(&p.export.ctx, "context", 3, "Alias of -c for specifying diff context lines.")
		fs.BoolVar(&p.export.includeResolved, "include-resolved", false, "Also include resolved threads (default skips them).")
		var legacyUnresolvedOnly bool
		fs.BoolVar(&legacyUnresolvedOnly, "unresolved-only", false, "Deprecated: now the default behavior.")

		if err := parseFlagSet(fs, buf, os.Args[2:], exportHelp); err != nil {
			return p, err
		}

		var unresolvedOnlySet bool
		fs.Visit(func(f *flag.Flag) {
			if f.Name == "unresolved-only" {
				unresolvedOnlySet = true
			}
		})
		if unresolvedOnlySet {
			return p, errors.New(`the --unresolved-only flag has been removed.
gh-prr export now skips resolved threads by default, so you can simply drop this flag.
to also include resolved threads in the output, use --include-resolved instead.
run "gh-prr export -h" for help`)
		}

		prArg, err := parsePRArg(fs.Args())
		if err != nil {
			return p, fmt.Errorf("%v\nrun \"gh-prr %s -h\" for help", err, fs.Name())
		}
		p.export.prNumber = prArg

		if p.export.ctx < 1 {
			p.export.ctx = 1
		}
		p.cmd = cmdExport
		return p, nil

	case string(cmdResolve):
		fs, buf := newFlagSet("resolve")

		if err := parseFlagSet(fs, buf, os.Args[2:], resolveHelp); err != nil {
			return p, err
		}

		prArg, err := parsePRArg(fs.Args())
		if err != nil {
			return p, fmt.Errorf("%v\nrun \"gh-prr %s -h\" for help", err, fs.Name())
		}
		p.resolve.prNumber = prArg

		p.cmd = cmdResolve
		return p, nil

	case string(cmdPending):
		fs, buf := newFlagSet("pending")

		fs.IntVar(&p.pending.ctx, "c", 3, "Number of lines to keep from the start/end of each diff hunk (alias: -context).")
		fs.IntVar(&p.pending.ctx, "context", 3, "Alias of -c for specifying diff context lines.")

		if err := parseFlagSet(fs, buf, os.Args[2:], pendingHelp); err != nil {
			return p, err
		}

		prArg, err := parsePRArg(fs.Args())
		if err != nil {
			return p, fmt.Errorf("%v\nrun \"gh-prr %s -h\" for help", err, fs.Name())
		}
		p.pending.prNumber = prArg

		if p.pending.ctx < 1 {
			p.pending.ctx = 1
		}
		p.cmd = cmdPending
		return p, nil

	case string(cmdWait):
		fs, buf := newFlagSet("wait")

		fs.IntVar(&p.wait.interval, "i", 30, "Polling interval in seconds.")
		fs.IntVar(&p.wait.interval, "interval", 30, "Alias for -i.")
		fs.IntVar(&p.wait.timeout, "t", 900, "Timeout in seconds (default 900 = 15 minutes).")
		fs.IntVar(&p.wait.timeout, "timeout", 900, "Alias for -t.")

		if err := parseFlagSet(fs, buf, os.Args[2:], waitHelp); err != nil {
			return p, err
		}

		prArg, err := parsePRArg(fs.Args())
		if err != nil {
			return p, fmt.Errorf("%v\nrun \"gh-prr %s -h\" for help", err, fs.Name())
		}
		p.wait.prNumber = prArg

		if p.wait.interval < 1 {
			p.wait.interval = 1
		}
		if p.wait.timeout < 1 {
			p.wait.timeout = 1
		}
		p.cmd = cmdWait
		return p, nil

	case string(cmdSubmit):
		fs, buf := newFlagSet("submit")

		fs.StringVar(&p.submit.file, "f", "", "Path to the review Markdown file. Use - for stdin. (alias: -file)")
		fs.StringVar(&p.submit.file, "file", "", "Alias for -f.")
		fs.BoolVar(&p.submit.pending, "pending", false, "Submit as a pending (draft) review without finalizing.")

		if err := parseFlagSet(fs, buf, os.Args[2:], submitHelp); err != nil {
			return p, err
		}

		if strings.TrimSpace(p.submit.file) == "" {
			return p, errors.New("submit requires -f <file> (use - for stdin)\nrun \"gh-prr submit -h\" for help")
		}

		prArg, err := parsePRArg(fs.Args())
		if err != nil {
			return p, fmt.Errorf("%v\nrun \"gh-prr %s -h\" for help", err, fs.Name())
		}
		p.submit.prNumber = prArg

		p.cmd = cmdSubmit
		return p, nil

	case string(cmdSubmitPending):
		fs, buf := newFlagSet("submit-pending")

		fs.StringVar(&p.submitPending.event, "e", "COMMENT", "Event to submit with: APPROVE, REQUEST_CHANGES, or COMMENT. (alias: -event)")
		fs.StringVar(&p.submitPending.event, "event", "COMMENT", "Alias for -e.")

		if err := parseFlagSet(fs, buf, os.Args[2:], submitPendingHelp); err != nil {
			return p, err
		}

		ev := strings.ToUpper(strings.TrimSpace(p.submitPending.event))
		switch ev {
		case "APPROVE", "REQUEST_CHANGES", "COMMENT":
			p.submitPending.event = ev
		default:
			return p, fmt.Errorf("invalid event %q (expected APPROVE, REQUEST_CHANGES, or COMMENT)\nrun \"gh-prr submit-pending -h\" for help", p.submitPending.event)
		}

		prArg, err := parsePRArg(fs.Args())
		if err != nil {
			return p, fmt.Errorf("%v\nrun \"gh-prr %s -h\" for help", err, fs.Name())
		}
		p.submitPending.prNumber = prArg

		p.cmd = cmdSubmitPending
		return p, nil

	case "-h", "--help":
		fmt.Fprintln(os.Stdout, rootHelp)
		return p, errHelpRequested
	default:
		return p, fmt.Errorf("unknown command %q\nrun \"gh-prr -h\" for available commands", os.Args[1])
	}
}
