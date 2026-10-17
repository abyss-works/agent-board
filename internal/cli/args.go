package cli

import (
	"flag"
	"fmt"
	"io"
	"strings"
)

// stringList is a repeatable string flag.
type stringList []string

func (l *stringList) String() string { return strings.Join(*l, ", ") }

func (l *stringList) Set(v string) error {
	*l = append(*l, v)
	return nil
}

// globals are the flags every command accepts, before or after the
// command name.
type globals struct {
	json       bool
	table      bool
	url        string
	actor      string
	sessionID  string
	clientHost string
	config     string
}

func (g *globals) addTo(fs *flag.FlagSet) {
	fs.BoolVar(&g.json, "json", g.json, "")
	fs.BoolVar(&g.table, "table", g.table, "")
	fs.StringVar(&g.url, "url", g.url, "")
	fs.StringVar(&g.actor, "actor", g.actor, "")
	fs.StringVar(&g.sessionID, "session-id", g.sessionID, "")
	fs.StringVar(&g.clientHost, "client-host", g.clientHost, "")
	fs.StringVar(&g.config, "config", g.config, "")
}

// newFlagSet builds a silent flag set; usage and errors are reported by
// the caller.
func newFlagSet(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	return fs
}

// parseArgs parses flags that may be interleaved with positional
// arguments. "--" ends flag parsing. It returns the positionals.
func parseArgs(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for i, a := range args {
		if a == "--" {
			pos = append(pos, args[i+1:]...)
			args = args[:i]
			break
		}
	}
	for len(args) > 0 {
		if err := fs.Parse(args); err != nil {
			if err == flag.ErrHelp {
				return nil, err
			}
			return nil, usageErrorf("%v", err)
		}
		rest := fs.Args()
		if len(rest) == 0 {
			break
		}
		pos = append(pos, rest[0])
		args = rest[1:]
	}
	return pos, nil
}

// usageError is a mistake in the command line or the configuration. It
// ends the process with exit code 2. help, when set, is printed after the
// message.
type usageError struct {
	msg  string
	help string
}

func (e *usageError) Error() string { return e.msg }

func usageErrorf(format string, args ...any) error {
	return &usageError{msg: fmt.Sprintf(format, args...)}
}

// isSet reports whether a flag was given on the command line.
func isSet(fs *flag.FlagSet, name string) bool {
	set := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == name {
			set = true
		}
	})
	return set
}
