/*
 * fullaccesshost.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Implementation of host where it is allowed to run external executables.
 *
 * Transliterated from common/fullAccessHost.ts (pyright 1.1.412): the two host
 * subclasses -- LimitedAccessHost and FullAccessHost -- and the interpreter
 * plumbing behind them.
 *
 * Layout note: the original is in common/, but it lands beside host.go for the
 * same reason host.go did -- it references PythonPlatform from configOptions
 * and PythonPathResult from analyzer/pythonPathUtils, both of which are in this
 * package.
 *
 * PARTIAL, and the boundary is the same one host.go draws: runScript, runSnippet
 * and spawnProcess are dropped. They are asynchronous, cancellable, and their
 * only callers are the language server and the type-stub generator. What is here
 * is the two synchronous interpreter queries the import resolver and
 * ConfigOptions actually make -- getPythonSearchPaths and getPythonVersion --
 * plus getPythonPlatform, which needs no interpreter at all.
 *
 * Why this matters more than its size suggests: without it the only host is
 * NoAccessHost, which answers "no paths, no version, no platform". That is
 * correct for a hermetic test, but against a real project it means the analyzer
 * never learns where the interpreter's site-packages live, so every third-party
 * import and every stdlib module absent from typeshed is unresolved. The
 * original's CLI builds a FullAccessHost; so, now, can this port.
 */

package analyzer

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"syscall"

	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/common/uri"
)

// removeCwdFromSysPath corresponds to the constant of the same name.
//
// The original's comment: preventLocalImports removes the working directory
// from sys.path. The -c flag adds it automatically, which can allow some stdlib
// modules (like json) to be overridden by other files (like json.py).
var removeCwdFromSysPath = []string{
	"import os, os.path, sys",
	"normalize = lambda p: os.path.normcase(os.path.normpath(p))",
	"cwd = normalize(os.getcwd())",
	`orig_sys_path = [p for p in sys.path if p != ""]`,
	`sys.path[:] = [p for p in sys.path if p != "" and normalize(p) != cwd]`,
}

// extractSys and extractVersion are the two snippets handed to the interpreter.
// They are `[...removeCwdFromSysPath, ...].join('; ')` in the original, and the
// exact text matters -- it is Python source, not a description of one.
var (
	extractSys = strings.Join(append(append([]string{}, removeCwdFromSysPath...),
		"import sys, json",
		"json.dump(dict(path=orig_sys_path, prefix=sys.prefix), sys.stdout)",
	), "; ")

	extractVersion = strings.Join(append(append([]string{}, removeCwdFromSysPath...),
		"import sys, json",
		"json.dump(tuple(sys.version_info), sys.stdout)",
	), "; ")
)

// isUnusableCwdSpawnError corresponds to the function of the same name.
//
// The original's comment: a failed chdir into a cwd that exists in the (virtual)
// FileSystem abstraction but not on the real OS surfaces as a spawn-level
// ENOENT: the child process never starts, so there is no exit `status`. A
// genuine interpreter error (process started, then exited non-zero) carries a
// numeric `status` and must not be treated as an unusable-cwd failure.
//
// The two conditions the original tests separately collapse into one here, and
// the reason is worth stating. In Node both failures arrive as the same thrown
// object and are told apart by whether `status` is set. In Go they arrive as
// different types: a process that ran and exited non-zero yields *exec.ExitError
// and nothing else does, so "has a status" is exactly "is an *exec.ExitError".
// What remains is to confirm the start failure was ENOENT rather than, say,
// EACCES -- os/exec reports a failed chdir as a *fs.PathError with Op "chdir",
// and a missing executable as *exec.Error wrapping exec.ErrNotFound.
func isUnusableCwdSpawnError(err error) bool {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return false
	}

	if errors.Is(err, syscall.ENOENT) || errors.Is(err, exec.ErrNotFound) {
		return true
	}

	// A *fs.PathError from the chdir carries ENOENT in Err, which errors.Is
	// above already unwraps. This is the belt-and-braces case where the error
	// text is all there is.
	var pathErr *fs.PathError
	return errors.As(err, &pathErr) && errors.Is(pathErr.Err, syscall.ENOENT)
}

// LimitedAccessHost corresponds to the class of the same name: a NoAccessHost
// that will still name the platform it is running on, which takes no
// interpreter to answer.
type LimitedAccessHost struct {
	NoAccessHost
}

var _ Host = (*LimitedAccessHost)(nil)

func NewLimitedAccessHost() *LimitedAccessHost { return &LimitedAccessHost{} }

func (h *LimitedAccessHost) Kind() HostKind { return HostKindLimitedAccess }

// GetPythonPlatform returns "" where the original returns undefined.
//
// The original reads `process.platform`, which names the platform the analyzer
// itself is running on rather than anything about the configured interpreter.
// runtime.GOOS is the same fact under a different name, and its spellings for
// these four match Node's exactly.
func (h *LimitedAccessHost) GetPythonPlatform(failureLogger *ImportLogger) PythonPlatform {
	switch runtime.GOOS {
	case "darwin":
		return PythonPlatformDarwin
	case "linux":
		return PythonPlatformLinux
	case "windows":
		return PythonPlatformWindows
	case "android":
		return PythonPlatformAndroid
	}

	return ""
}

// FullAccessHost corresponds to the class of the same name: a host that answers
// by running the configured Python interpreter.
type FullAccessHost struct {
	LimitedAccessHost

	// fs and detector are what the original reaches through its
	// ServiceProvider: `serviceProvider.fs()` and
	// `serviceProvider.get(ServiceKeys.caseSensitivityDetector)`. The
	// ServiceProvider evaporates here as it has everywhere else in the port, so
	// the two things actually taken from it are taken directly.
	fs       uri.ReadOnlyFileSystem
	detector common.CaseSensitivityDetector
}

var _ Host = (*FullAccessHost)(nil)

// NewFullAccessHost corresponds to the constructor.
func NewFullAccessHost(fileSystem uri.ReadOnlyFileSystem, detector common.CaseSensitivityDetector) *FullAccessHost {
	if detector == nil {
		detector = uri.UriExDetector(true)
	}
	return &FullAccessHost{fs: fileSystem, detector: detector}
}

func (h *FullAccessHost) Kind() HostKind { return HostKindFullAccess }

// CreateHost corresponds to the static method of the same name. The original
// calls assertNever on an unrecognized kind; there is no such kind, and Go's
// switch has no exhaustiveness check to satisfy, so an unknown value gets the
// most restrictive host rather than a panic.
func CreateHost(kind HostKind, fileSystem uri.ReadOnlyFileSystem, detector common.CaseSensitivityDetector) Host {
	switch kind {
	case HostKindLimitedAccess:
		return NewLimitedAccessHost()
	case HostKindFullAccess:
		return NewFullAccessHost(fileSystem, detector)
	}

	return NewNoAccessHost()
}

// GetPythonSearchPaths corresponds to getPythonSearchPaths.
func (h *FullAccessHost) GetPythonSearchPaths(
	pythonPath uri.Uri, failureLogger *ImportLogger, cwd uri.Uri,
) PythonPathResult {
	result, _ := executePythonInterpreter(uriFilePath(pythonPath), func(p string) (*PythonPathResult, error) {
		return h.getSearchPathResultFromInterpreter(p, failureLogger, cwd), nil
	})

	if result == nil {
		result = &PythonPathResult{Paths: []uri.Uri{}, Prefix: nil}
	}

	failureLogger.Log("Received " + itoa(len(result.Paths)) + " paths from interpreter")
	for _, path := range result.Paths {
		failureLogger.Log("  " + path.String())
	}

	return *result
}

// GetPythonVersion corresponds to getPythonVersion. It returns nil where the
// original returns undefined, which every caller already tests for.
func (h *FullAccessHost) GetPythonVersion(pythonPath uri.Uri, failureLogger *ImportLogger) *common.PythonVersion {
	execOutput, err := executePythonInterpreter(uriFilePath(pythonPath), func(p string) (*string, error) {
		return h.executeCodeInInterpreter(p, []string{"-I"}, extractVersion, nil)
	})

	// The original's one `try` covers the interpreter run, the JSON parse and
	// the non-null assertion on execOutput together, and answers undefined for
	// all three. Splitting them here would be a distinction the original does
	// not make.
	if err != nil || execOutput == nil {
		failureLogger.Log("Unable to get Python version from interpreter")
		return nil
	}

	// The original declares `versionJson: any[]` and then checks isArray, so a
	// non-array parse is a format failure rather than a throw. Unmarshalling
	// into []any answers the same question: anything that is not a JSON array
	// fails here.
	var versionJson []any
	if json.Unmarshal([]byte(*execOutput), &versionJson) != nil {
		failureLogger.Log("Unable to get Python version from interpreter")
		return nil
	}

	if len(versionJson) < 5 {
		failureLogger.Log("Python version " + *execOutput + " from interpreter is unexpected format")
		return nil
	}

	// sys.version_info is (major, minor, micro, releaselevel, serial): three
	// numbers, a string, a number. The original indexes straight into `any[]`
	// and hands the values to PythonVersion.create, which does not check them
	// either -- a malformed tuple there produces a PythonVersion holding
	// whatever was in the array. Go cannot store a string in an int field, so
	// the shape has to be checked, and a mismatch takes the same
	// unexpected-format exit the length check does.
	major, majorOk := jsonInt(versionJson[0])
	minor, minorOk := jsonInt(versionJson[1])
	micro, microOk := jsonInt(versionJson[2])
	serial, serialOk := jsonInt(versionJson[4])
	releaseLevelStr, releaseLevelOk := versionJson[3].(string)

	if !majorOk || !minorOk || !microOk || !serialOk || !releaseLevelOk {
		failureLogger.Log("Python version " + *execOutput + " from interpreter is unexpected format")
		return nil
	}

	releaseLevel := common.PythonReleaseLevel(releaseLevelStr)
	version := common.NewPythonVersion(major, minor, &micro, &releaseLevel, &serial)

	// The original follows this with `if (version === undefined)` and logs
	// "is unsupported". PythonVersion.create is declared to return
	// PythonVersion, not PythonVersion | undefined, and its body is a bare
	// object literal -- so that branch cannot be taken and the message is
	// unreachable. It is recorded here rather than transliterated into a
	// condition Go would flag as always false.

	return &version
}

// shouldUseShellToRunInterpreter corresponds to the method of the same name.
//
// The original's comment: Windows bat/cmd files must me executed with the shell
// due to the following breaking change:
// https://nodejs.org/en/blog/vulnerability/april-2024-security-releases-2#command-injection-via-args-parameter-of-child_processspawn-without-shell-option-enabled-on-windows-cve-2024-27980---high
func (h *FullAccessHost) shouldUseShellToRunInterpreter(interpreterPath string) bool {
	return runtime.GOOS == "windows" &&
		common.GetAnyExtensionFromPathIn(interpreterPath, []string{".bat", ".cmd"}, true) != ""
}

// getUsableCwdPath corresponds to the method of the same name. The bool is the
// original's `string | undefined`.
func (h *FullAccessHost) getUsableCwdPath(cwd uri.Uri) (string, bool) {
	return uri.GetUsableUriPath(h.fs, cwd)
}

// executePythonInterpreter corresponds to _executePythonInterpreter. It is a
// free function rather than a method because Go methods cannot take type
// parameters, and the original is generic in what `execute` answers.
//
// Two things about the control flow are easy to lose. First, `execute` has two
// distinct ways to fail -- returning undefined and throwing -- and both fall
// through to the 'python' attempt. Second, only the 'python3' attempt is inside
// the try: a throw from 'python' propagates to the caller, which is what lets
// getPythonVersion report "unable to get Python version" for a missing
// interpreter.
func executePythonInterpreter[T any](pythonPath string, execute func(string) (*T, error)) (*T, error) {
	if pythonPath != "" {
		return execute(pythonPath)
	}

	// The original's comment: on non-Windows platforms, always default to
	// python3 first. We want to avoid this on Windows because it might invoke a
	// script that displays a dialog box indicating that python can be downloaded
	// from the app store.
	if runtime.GOOS != "windows" {
		if result, err := execute("python3"); err == nil && result != nil {
			return result, nil
		}
	}

	// The original's comment: on some platforms, 'python3' might not exist. Try
	// 'python' instead.
	return execute("python")
}

// executeCodeInInterpreter corresponds to _executeCodeInInterpreter.
//
// The original's comment: executes a chunk of Python code via the provided
// interpreter and returns the output.
func (h *FullAccessHost) executeCodeInInterpreter(
	interpreterPath string, commandLineArgs []string, code string, cwd uri.Uri,
) (*string, error) {
	useShell := h.shouldUseShellToRunInterpreter(interpreterPath)
	if useShell {
		code = `"` + code + `"`
	}

	// `commandLineArgs.push('-c', code)` mutates the caller's array. Both call
	// sites pass a freshly built literal, so nothing observes the mutation, and
	// appending to a copy here is the same thing without the hazard.
	args := append(append([]string{}, commandLineArgs...), "-c", code)

	cwdPath, cwdOk := h.getUsableCwdPath(cwd)

	run := func(cwdToUse string) (*string, error) {
		return runInterpreter(interpreterPath, args, cwdToUse, useShell)
	}

	if !cwdOk {
		return run("")
	}

	output, err := run(cwdPath)
	if err == nil {
		return output, nil
	}

	// The original's comment: getUsableCwdPath validates the cwd against the
	// FileSystem abstraction, which can be a virtual/mapped filesystem (e.g. a
	// test harness that mounts a workspace root that does not exist on the real
	// OS). The real child process can only chdir into a directory that exists on
	// the real OS, so a missing cwd surfaces as a spawn-level ENOENT (the child
	// never starts). Only that case falls back to the inherited cwd. A genuine
	// interpreter failure (the process started and exited non-zero, e.g. a
	// cwd-relative sitecustomize.py/.pth that raises) must propagate: silently
	// retrying without the cwd would return inherited-cwd paths that callers
	// then cache under the workspace-cwd key, reintroducing the wrong-cwd
	// result.
	if !isUnusableCwdSpawnError(err) {
		return nil, err
	}

	return run("")
}

// runInterpreter is child_process.execFileSync with `encoding: 'utf8'`: run to
// completion, answer stdout, and fail on a non-zero exit.
//
// Node inherits the parent's stderr for execFileSync unless told otherwise, so
// an interpreter that complains does so on the analyzer's own stderr. That is
// reproduced rather than swallowed -- a broken sitecustomize.py should be as
// visible here as it is upstream.
func runInterpreter(interpreterPath string, args []string, cwd string, useShell bool) (*string, error) {
	var cmd *exec.Cmd
	if useShell {
		// Node's `shell: true` on Windows runs the command through cmd.exe,
		// which is the whole point of the flag: a .bat or .cmd interpreter
		// shim cannot be executed directly. This branch is unreachable on any
		// other platform and is untested here.
		cmd = exec.Command("cmd", append([]string{"/c", interpreterPath}, args...)...)
	} else {
		cmd = exec.Command(interpreterPath, args...)
	}

	if cwd != "" {
		cmd.Dir = cwd
	}
	cmd.Stderr = os.Stderr

	stdout, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	output := string(stdout)
	return &output, nil
}

// getSearchPathResultFromInterpreter corresponds to
// _getSearchPathResultFromInterpreter. It returns nil where the original returns
// undefined; every failure inside, including the parse failure the inner catch
// rethrows, ends there.
func (h *FullAccessHost) getSearchPathResultFromInterpreter(
	interpreterPath string, failureLogger *ImportLogger, cwd uri.Uri,
) *PythonPathResult {
	result := &PythonPathResult{Paths: []uri.Uri{}, Prefix: nil}

	failureLogger.Log("Executing interpreter: '" + interpreterPath + "'")
	execOutput, err := h.executeCodeInInterpreter(interpreterPath, []string{}, extractSys, cwd)
	if err != nil || execOutput == nil {
		return nil
	}

	// The original's comment: parse the execOutput. It should be a JSON-encoded
	// array of paths.
	//
	// The original reads `execSplit.path` and `execSplit.prefix` off an untyped
	// parse and lets a missing key fail on its own -- `for...of undefined`
	// throws, and so does normalizePath(undefined) -- which its inner catch
	// turns into the "could not parse" log. The two fields are pointers here so
	// absent stays distinguishable from empty and takes that same exit.
	var parsed struct {
		Path   *[]string `json:"path"`
		Prefix *string   `json:"prefix"`
	}
	if json.Unmarshal([]byte(*execOutput), &parsed) != nil || parsed.Path == nil || parsed.Prefix == nil {
		failureLogger.Log("Could not parse output: '" + *execOutput + "'")
		return nil
	}

	for _, execSplitEntry := range *parsed.Path {
		execSplitEntry = strings.TrimSpace(execSplitEntry)
		if execSplitEntry == "" {
			continue
		}

		normalizedPath := common.NormalizePath(execSplitEntry)
		normalizedUri := uri.File(normalizedPath, h.detector, false)

		// The original's comment: skip non-existent paths and broken zips/eggs.
		if uri.IsUsableDirectory(h.fs, normalizedUri) {
			result.Paths = append(result.Paths, normalizedUri)
		} else {
			failureLogger.Log("Skipping '" + normalizedPath + "' because it is not a valid directory")
		}
	}

	result.Prefix = uri.File(*parsed.Prefix, h.detector, false)

	if len(result.Paths) == 0 {
		failureLogger.Log("Found no valid directories")
	}

	return result
}

// uriFilePath is `pythonPath?.getFilePath()`: "" stands for undefined, which is
// the test executePythonInterpreter makes.
func uriFilePath(u uri.Uri) string {
	if u == nil {
		return ""
	}
	return u.GetFilePath()
}

// jsonInt reads a JSON number that should be an integer. encoding/json decodes
// every number into float64 when the target is `any`, exactly as JSON.parse
// produces a JS number; this is the narrowing the original never has to write.
func jsonInt(value any) (int, bool) {
	number, ok := value.(float64)
	if !ok {
		return 0, false
	}
	return int(number), true
}
