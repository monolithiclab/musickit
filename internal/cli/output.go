package cli

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
)

// Printf writes a data line to stdout.
func (rt *Runtime) Printf(format string, a ...any) {
	fmt.Fprintf(rt.Out, format, a...)
}

// Row writes a tab-separated record to stdout.
func (rt *Runtime) Row(fields ...string) {
	fmt.Fprintln(rt.Out, strings.Join(fields, "\t"))
}

// JSON writes one JSON object per line to stdout. Collections stream as NDJSON
// rather than one array so they can be filtered line by line.
func (rt *Runtime) JSON(v any) error {
	enc := json.NewEncoder(rt.Out)
	return enc.Encode(v)
}

// Logf writes progress to stderr. Silenced by --quiet.
func (rt *Runtime) Logf(format string, a ...any) {
	if rt.G.Quiet {
		return
	}
	fmt.Fprintf(rt.Err, format+"\n", a...)
}

// Warnf writes to stderr regardless of --quiet: something needs attention.
func (rt *Runtime) Warnf(format string, a ...any) {
	fmt.Fprintf(rt.Err, format+"\n", a...)
}

// Confirm asks a yes/no question before something irreversible. --yes answers
// it in advance; with no terminal to ask on, the command refuses rather than
// guessing.
func (rt *Runtime) Confirm(format string, a ...any) error {
	if rt.G.Yes {
		return nil
	}
	if !rt.interactive() {
		return fmt.Errorf("%s: refusing to prompt with no terminal — pass --yes to confirm", fmt.Sprintf(format, a...))
	}
	fmt.Fprintf(rt.Err, "%s [y/N] ", fmt.Sprintf(format, a...))

	line, err := bufio.NewReader(rt.In).ReadString('\n')
	if err != nil && line == "" {
		return errors.New("cancelled")
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return nil
	default:
		return errors.New("cancelled")
	}
}

// interactive reports whether there is a terminal on both ends of the prompt.
func (rt *Runtime) interactive() bool {
	return isTerminal(rt.In) && isTerminal(rt.Err)
}

func isTerminal(stream any) bool {
	f, ok := stream.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}
