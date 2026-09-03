/*
 * args.go
 *
 * The command line, transliterated from packages/pyright/src/pyright.ts
 * (pyright 1.1.412): the option table, processArgs' validation, and the mapping
 * from arguments onto CommandLineOptions.
 *
 * Go's flag package is not usable here. pyright parses with `command-line-args`,
 * which accepts options and positional arguments interleaved and treats `files`
 * as the default option; Go's flag stops at the first non-flag argument, so
 * `pyright-go foo.py --outputjson` would silently ignore the flag. A tool meant
 * to stand in for another has to accept the other's command lines, so this
 * parser is written to `command-line-args`' rules rather than Go's.
 */

package main

import (
	"fmt"
	"io"
	"os"
	"strings"
)

// ExitStatus corresponds to the enum of the same name.
type ExitStatus int

const (
	ExitNoErrors ExitStatus = iota
	ExitErrorsReported
	ExitFatalError
	ExitConfigFileParseError
	ExitParameterError
)

// SeverityLevel corresponds to the type of the same name.
type SeverityLevel string

const (
	SeverityError       SeverityLevel = "error"
	SeverityWarning     SeverityLevel = "warning"
	SeverityInformation SeverityLevel = "information"
)

// optionKind says how many values an option takes.
type optionKind int

const (
	optionBool optionKind = iota
	optionString
	optionStringMultiple

	// optionOptionalString is `--threads` with its optional COUNT: present with
	// or without a value.
	optionOptionalString
)

type optionDef struct {
	name  string
	alias string
	kind  optionKind
}

// optionDefs is the original's optionDefinitions table, in its order.
var optionDefs = []optionDef{
	{name: "createstub", kind: optionString},
	{name: "dependencies", kind: optionBool},
	{name: "files", kind: optionStringMultiple},
	{name: "help", alias: "h", kind: optionBool},
	{name: "ignoreexternal", kind: optionBool},
	{name: "lib", kind: optionBool},
	{name: "level", kind: optionString},
	{name: "outputjson", kind: optionBool},
	{name: "project", alias: "p", kind: optionString},
	{name: "pythonpath", kind: optionString},
	{name: "pythonplatform", kind: optionString},
	{name: "pythonversion", kind: optionString},
	{name: "skipunannotated", kind: optionBool},
	{name: "stats", kind: optionBool},
	{name: "threads", kind: optionOptionalString},
	{name: "typeshed-path", kind: optionString},
	{name: "typeshedpath", alias: "t", kind: optionString},
	{name: "venv-path", kind: optionString},
	{name: "venvpath", alias: "v", kind: optionString},
	{name: "verifytypes", kind: optionString},
	{name: "verbose", kind: optionBool},
	{name: "version", kind: optionBool},
	{name: "warnings", kind: optionBool},
	{name: "watch", alias: "w", kind: optionBool},

	// Not pyright's. The original finds its bundled typeshed relative to the
	// running script; a Go binary has no such relationship to the reference
	// tree, so the location can be given explicitly. See resolveTypeshedRoot for
	// what happens when it is not.
	{name: "rootdir", kind: optionString},

	// Also not pyright's: an escape hatch for reproducing a run without letting
	// the analyzer execute a Python interpreter, and the two Go profilers.
	{name: "nointerpreter", kind: optionBool},
	{name: "cpuprofile", kind: optionString},
	{name: "memprofile", kind: optionString},
}

// parsedArgs holds what was on the command line. A nil pointer is an absent
// option, which is the distinction `args.x !== undefined` makes throughout.
type parsedArgs struct {
	values  map[string]*string
	present map[string]bool
	files   []string
}

func (a *parsedArgs) has(name string) bool { return a.present[name] }

func (a *parsedArgs) str(name string) string {
	if v := a.values[name]; v != nil {
		return *v
	}
	return ""
}

// truthy is JavaScript's `if (args.x)`, which an empty string does not satisfy.
// The distinction matters: the original tests `args.project` (truthy) in one
// place and `args.pythonpath !== undefined` (presence) in another.
func (a *parsedArgs) truthy(name string) bool {
	return a.has(name) && a.str(name) != ""
}

func lookupOption(name string) (optionDef, bool) {
	for _, def := range optionDefs {
		if def.name == name {
			return def, true
		}
	}
	return optionDef{}, false
}

func lookupAlias(alias string) (optionDef, bool) {
	for _, def := range optionDefs {
		if def.alias != "" && def.alias == alias {
			return def, true
		}
	}
	return optionDef{}, false
}

// parseArgs reads the command line the way `command-line-args` does: options
// anywhere, `--name=value` or `--name value`, single-dash aliases, and every
// remaining bare word collected into files (the default option). A lone "-" is
// a file argument, meaning "read the list from stdin".
func parseArgs(argv []string) (*parsedArgs, error) {
	args := &parsedArgs{values: map[string]*string{}, present: map[string]bool{}}

	take := func(def optionDef, inlineValue *string, rest []string) ([]string, error) {
		args.present[def.name] = true

		switch def.kind {
		case optionBool:
			if inlineValue != nil {
				return rest, fmt.Errorf("option %s does not take a value", def.name)
			}
			return rest, nil

		case optionOptionalString:
			if inlineValue != nil {
				args.values[def.name] = inlineValue
				return rest, nil
			}
			// Consume a following value only when it does not look like another
			// option, which is how an optional-value option behaves.
			if len(rest) > 0 && !strings.HasPrefix(rest[0], "-") {
				value := rest[0]
				args.values[def.name] = &value
				return rest[1:], nil
			}
			return rest, nil

		case optionStringMultiple:
			if inlineValue != nil {
				args.files = append(args.files, *inlineValue)
				return rest, nil
			}
			for len(rest) > 0 && !strings.HasPrefix(rest[0], "--") {
				args.files = append(args.files, rest[0])
				rest = rest[1:]
			}
			return rest, nil

		default:
			if inlineValue != nil {
				args.values[def.name] = inlineValue
				return rest, nil
			}
			if len(rest) == 0 {
				return rest, fmt.Errorf("option %s requires a value", def.name)
			}
			value := rest[0]
			args.values[def.name] = &value
			return rest[1:], nil
		}
	}

	rest := argv
	for len(rest) > 0 {
		arg := rest[0]
		rest = rest[1:]

		switch {
		case arg == "-":
			// The stdin sentinel, which is a file argument rather than an option.
			args.files = append(args.files, arg)

		case strings.HasPrefix(arg, "--"):
			name := arg[2:]
			var inlineValue *string
			if i := strings.IndexByte(name, '='); i >= 0 {
				value := name[i+1:]
				name, inlineValue = name[:i], &value
			}

			def, ok := lookupOption(name)
			if !ok {
				return nil, unknownOptionError{name: "--" + name}
			}

			var err error
			if rest, err = take(def, inlineValue, rest); err != nil {
				return nil, err
			}

		case strings.HasPrefix(arg, "-"):
			name := arg[1:]
			var inlineValue *string
			if i := strings.IndexByte(name, '='); i >= 0 {
				value := name[i+1:]
				name, inlineValue = name[:i], &value
			}

			def, ok := lookupAlias(name)
			if !ok {
				// Long names are accepted after a single dash too, which is what
				// Go users will reach for out of habit.
				if def, ok = lookupOption(name); !ok {
					return nil, unknownOptionError{name: "-" + name}
				}
			}

			var err error
			if rest, err = take(def, inlineValue, rest); err != nil {
				return nil, err
			}

		default:
			args.files = append(args.files, arg)
		}
	}

	if len(args.files) > 0 {
		args.present["files"] = true
	}

	return args, nil
}

type unknownOptionError struct{ name string }

func (e unknownOptionError) Error() string { return "Unexpected option " + e.name }

// readFileListFromStdin is the original's getStdin path: newlines become spaces,
// then split on spaces with empties dropped.
func readFileListFromStdin() ([]string, error) {
	text, err := io.ReadAll(os.Stdin)
	if err != nil {
		return nil, err
	}

	replaced := strings.NewReplacer("\r", " ", "\n", " ").Replace(string(text))

	out := []string{}
	for _, field := range strings.Split(strings.TrimSpace(replaced), " ") {
		if trimmed := strings.TrimSpace(field); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out, nil
}

// parsePythonPlatform accepts exactly the five the original does.
func parsePythonPlatform(value string) (string, bool) {
	switch value {
	case "Darwin", "Linux", "Windows", "iOS", "Android":
		return value, true
	}
	return "", false
}

const usageText = `Usage: pyright-go [options] files...
  Options:
  --createstub <IMPORT>              Create type stub file(s) for import
  --dependencies                     Emit import dependency information
  -h,--help                          Show this help message
  --ignoreexternal                   Ignore external imports for --verifytypes
  --level <LEVEL>                    Minimum diagnostic level (error or warning)
  --outputjson                       Output results in JSON format
  -p,--project <FILE OR DIRECTORY>   Use the configuration file at this location
  --pythonplatform <PLATFORM>        Analyze for a specific platform (Darwin, Linux, Windows, iOS, Android)
  --pythonpath <FILE>                Path to the Python interpreter
  --pythonversion <VERSION>          Analyze for a specific version (3.3, 3.4, etc.)
  --skipunannotated                  Skip analysis of functions with no type annotations
  --stats                            Print detailed performance stats
  -t,--typeshedpath <DIRECTORY>      Use typeshed type stubs at this location
  --threads <optional COUNT>         Use separate threads to parallelize type checking
  -v,--venvpath <DIRECTORY>          Directory that contains virtual environments
  --verbose                          Emit verbose diagnostics
  --verifytypes <PACKAGE>            Verify type completeness of a py.typed package
  --version                          Print Pyright version and exit
  --warnings                         Use exit code of 1 if warnings are reported
  -w,--watch                         Continue to run and watch for changes
  -                                  Read files from stdin

  Additional to this port:
  --rootdir <DIRECTORY>              Directory holding typeshed-fallback
  --nointerpreter                    Never run a Python interpreter
  --cpuprofile <FILE>                Write a Go CPU profile
  --memprofile <FILE>                Write a Go heap profile
`
