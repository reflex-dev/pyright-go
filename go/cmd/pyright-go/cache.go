/*
 * cache.go
 *
 * The --cachedir mode: a run-to-run diagnostic cache. No counterpart in
 * pyright, which re-checks everything on every CLI invocation; this is a
 * pyright-go extension, off unless the flag is given.
 *
 * The idea rests on the property the --threads port established: under
 * checkOnlyOpenFiles, a file's diagnostics are a function of its transitive
 * dependency closure -- the contents of everything it imports, typeshed and
 * site-packages included -- plus the configuration. So each tracked file is
 * fingerprinted over exactly that closure, computed from a parse-and-resolve
 * pass over the reachable import graph (~1s where checking is ~30s). A file
 * whose fingerprint matches the previous run replays its stored diagnostics;
 * the rest go through the same worker pool --threads uses. Import-resolution
 * changes are caught for free: resolution is recomputed every run, and a
 * different resolution changes the closure's membership.
 *
 * Because hits and misses alike carry per-file isolation semantics, the
 * cached mode's reference output is a --threads run, not a single-threaded
 * one; the difference is the checkOnlyOpenFiles isolation quirk documented as
 * UPSTREAM-BUGS.md #17.
 */

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/microsoft/pyright/go/analyzer"
	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/common/uri"
)

// cacheFormatVersion invalidates every entry when the format or anything
// about diagnostic production changes shape.
const cacheFormatVersion = 1

const cacheFileName = "pyright-go-cache.json"

// cachedRange mirrors common.Range with stable JSON names.
type cachedRange struct {
	StartLine int `json:"sl"`
	StartChar int `json:"sc"`
	EndLine   int `json:"el"`
	EndChar   int `json:"ec"`
}

// cachedDiag is the lossless-for-reporting subset of common.Diagnostic: the
// reporters read exactly the category, message, range and rule (addenda are
// already folded into the message text).
type cachedDiag struct {
	Category int         `json:"c"`
	Message  string      `json:"m"`
	Range    cachedRange `json:"r"`
	Rule     *string     `json:"u,omitempty"`
}

type cacheEntry struct {
	Fingerprint string       `json:"fp"`
	Diagnostics []cachedDiag `json:"d"`
}

type cacheFile struct {
	FormatVersion int                   `json:"formatVersion"`
	GlobalKey     string                `json:"globalKey"`
	Entries       map[string]cacheEntry `json:"entries"`
}

// runCached is the --cachedir counterpart of runMultiThreaded: same service
// setup, same worker pool, but only over the files whose closure fingerprint
// changed since the previous run.
func runCached(
	args *parsedArgs,
	options *analyzer.CommandLineOptions,
	cacheDir string,
	maxThreadCount int,
	service *analyzer.AnalyzerService,
	minSeverityLevel SeverityLevel,
	output common.ConsoleInterface,
	config workerConfig,
) ExitStatus {
	startTime := time.Now()

	// Same isolation semantics as --threads; see the file header.
	checkOnlyOpenFiles := true
	options.LanguageServerSettings.CheckOnlyOpenFiles = &checkOnlyOpenFiles
	service.SetOptions(options)
	if service.ConfigParseErrorOccurred() {
		return ExitConfigFileParseError
	}

	// Parse the reachable graph and fingerprint every tracked file's closure.
	reachable := service.Program().ParseReachableFilesForImportGraph()
	fingerprints, tracked, ok := fingerprintClosures(service, reachable, globalCacheKey(args, config), output)
	if !ok {
		return ExitFatalError
	}

	previous := loadCache(cacheDir, globalCacheKey(args, config))

	// Split into replayable hits and files that need checking.
	fileDiagnostics := []common.FileDiagnostics{}
	misses := []uri.Uri{}
	for _, info := range tracked {
		key := info.Uri().Key()
		entry, hit := previous.Entries[key]
		if hit && entry.Fingerprint == fingerprints[key] {
			fileDiagnostics = append(fileDiagnostics, common.FileDiagnostics{
				FileUri:     info.Uri(),
				Diagnostics: decodeDiagnostics(entry.Diagnostics),
			})
		} else {
			misses = append(misses, info.Uri())
		}
	}

	output.Info(fmt.Sprintf("Found %d files to analyze", len(tracked)))
	output.Info(fmt.Sprintf("Cache: %d unchanged, %d to check", len(tracked)-len(misses), len(misses)))
	output.Info(fmt.Sprintf("Using %d threads", min(maxThreadCount, len(misses))))

	missDiagnostics, status := checkFilesInIsolation(misses, maxThreadCount, config, output)
	if status != ExitNoErrors {
		return status
	}
	fileDiagnostics = append(fileDiagnostics, missDiagnostics...)

	// Store the new state: every tracked file's fingerprint, with the
	// diagnostics it produced or replayed. A checked file that produced no
	// FileDiagnostics entry gets an explicit empty one -- absence of an entry
	// must not read as "no diagnostics" next run.
	next := cacheFile{
		FormatVersion: cacheFormatVersion,
		GlobalKey:     globalCacheKey(args, config),
		Entries:       make(map[string]cacheEntry, len(tracked)),
	}
	diagsByKey := map[string][]cachedDiag{}
	for _, fileDiag := range fileDiagnostics {
		diagsByKey[fileDiag.FileUri.Key()] = encodeDiagnostics(fileDiag.Diagnostics)
	}
	for _, info := range tracked {
		key := info.Uri().Key()
		next.Entries[key] = cacheEntry{
			Fingerprint: fingerprints[key],
			Diagnostics: diagsByKey[key],
		}
	}
	if err := saveCache(cacheDir, &next); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to write cache: %v\n", err)
	}

	return reportPooledDiagnostics(args, fileDiagnostics, len(tracked),
		startTime, minSeverityLevel, output)
}

// globalCacheKey hashes everything outside the file closures that can change
// a diagnostic: the binary version, the cache format, the typeshed root, and
// the semantics-affecting command line. The config file's *content* is part
// of every closure key instead (fingerprintClosures mixes it in), and
// interpreter-driven settings resolve into configOptions whose effects show
// up through resolution changes in the closures themselves.
func globalCacheKey(args *parsedArgs, config workerConfig) string {
	h := sha256.New()
	fmt.Fprintf(h, "v=%s;fmt=%d;typeshed=%s;", version, cacheFormatVersion, config.typeshedRoot)
	for _, name := range []string{
		"project", "pythonversion", "pythonplatform", "pythonpath",
		"venvpath", "venv-path", "typeshedpath", "typeshed-path",
		"skipunannotated", "nointerpreter",
	} {
		fmt.Fprintf(h, "%s=%s;", name, args.str(name))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// fingerprintClosures computes, for every tracked file, a hash of its
// transitive dependency closure: an order-independent combination of each
// member's (path, content) hash. Cycles need no special casing -- the
// closure sets are computed as a bitset fixpoint over the import graph, which
// converges with cycles present. The effective config file content and the
// global key are mixed into every fingerprint.
func fingerprintClosures(
	service *analyzer.AnalyzerService,
	reachable []*analyzer.SourceFileInfo,
	globalKey string,
	output common.ConsoleInterface,
) (map[string]string, []*analyzer.SourceFileInfo, bool) {
	fs := service.FS()

	// Index the reachable files and hash their contents.
	index := make(map[*analyzer.SourceFileInfo]int, len(reachable))
	for i, info := range reachable {
		index[info] = i
	}
	contentHashes := make([][32]byte, len(reachable))
	for i, info := range reachable {
		content, err := fs.ReadFileSync(info.Uri())
		if err != nil {
			// A reachable file that cannot be read (deleted mid-run?) makes
			// the fingerprints unreliable; fall back to a full check.
			output.Error(fmt.Sprintf("Cannot read %s for cache fingerprinting", info.Uri().String()))
			return nil, nil, false
		}
		h := sha256.New()
		fmt.Fprintf(h, "%s\x00", info.Uri().Key())
		h.Write(content)
		copy(contentHashes[i][:], h.Sum(nil))
	}

	// Closure bitsets by fixpoint: closure(i) = {i} ∪ closure(imports of i).
	// Converges in passes bounded by the longest acyclic import chain.
	words := (len(reachable) + 63) / 64
	closures := make([][]uint64, len(reachable))
	successors := make([][]int, len(reachable))
	for i, info := range reachable {
		closures[i] = make([]uint64, words)
		closures[i][i/64] |= 1 << (i % 64)

		neighbors := info.Imports()
		if builtins := info.BuiltinsImport(); builtins != nil {
			neighbors = append(neighbors[:len(neighbors):len(neighbors)], builtins)
		}
		if chained := info.ChainedSourceFile(); chained != nil {
			neighbors = append(neighbors[:len(neighbors):len(neighbors)], chained)
		}
		for _, neighbor := range neighbors {
			if j, ok := index[neighbor]; ok {
				successors[i] = append(successors[i], j)
			}
		}
	}
	for changed := true; changed; {
		changed = false
		for i := range reachable {
			for _, j := range successors[i] {
				for w := 0; w < words; w++ {
					if merged := closures[i][w] | closures[j][w]; merged != closures[i][w] {
						closures[i][w] = merged
						changed = true
					}
				}
			}
		}
	}

	// The effective config file's content is part of every fingerprint.
	configSalt := sha256.Sum256([]byte(globalKey + "\x00" + configFileContent(service, fs)))

	fingerprints := map[string]string{}
	tracked := []*analyzer.SourceFileInfo{}
	for i, info := range reachable {
		if !info.IsTracked() {
			continue
		}
		tracked = append(tracked, info)

		var combined [32]byte
		copy(combined[:], configSalt[:])
		memberCount := 0
		for j := 0; j < len(reachable); j++ {
			if closures[i][j/64]&(1<<(j%64)) != 0 {
				for b := 0; b < 32; b++ {
					combined[b] ^= contentHashes[j][b]
				}
				memberCount++
			}
		}
		fingerprints[info.Uri().Key()] = fmt.Sprintf("%s-%d", hex.EncodeToString(combined[:]), memberCount)
	}
	return fingerprints, tracked, true
}

// configFileContent reads the effective config file, if any, so that its
// content participates in every fingerprint.
func configFileContent(service *analyzer.AnalyzerService, fs uri.FileSystem) string {
	configOptions := service.GetConfigOptions()
	if configOptions == nil || configOptions.ConfigFileSource == nil {
		return ""
	}
	content, err := fs.ReadFileSync(configOptions.ConfigFileSource)
	if err != nil {
		return "unreadable"
	}
	return configOptions.ConfigFileSource.Key() + "\x00" + string(content)
}

func encodeDiagnostics(diags []*common.Diagnostic) []cachedDiag {
	out := make([]cachedDiag, 0, len(diags))
	for _, d := range diags {
		out = append(out, cachedDiag{
			Category: int(d.Category),
			Message:  d.Message,
			Range: cachedRange{
				StartLine: d.Range.Start.Line,
				StartChar: d.Range.Start.Character,
				EndLine:   d.Range.End.Line,
				EndChar:   d.Range.End.Character,
			},
			Rule: d.GetRule(),
		})
	}
	return out
}

func decodeDiagnostics(cached []cachedDiag) []*common.Diagnostic {
	out := make([]*common.Diagnostic, 0, len(cached))
	for _, c := range cached {
		d := common.NewDiagnostic(common.DiagnosticCategory(c.Category), c.Message, common.Range{
			Start: common.Position{Line: c.Range.StartLine, Character: c.Range.StartChar},
			End:   common.Position{Line: c.Range.EndLine, Character: c.Range.EndChar},
		})
		if c.Rule != nil {
			d.SetRule(*c.Rule)
		}
		out = append(out, d)
	}
	return out
}

// loadCache returns an empty cache when the file is missing, unreadable, from
// a different format, or from a different global configuration.
func loadCache(cacheDir string, globalKey string) *cacheFile {
	empty := &cacheFile{Entries: map[string]cacheEntry{}}
	content, err := os.ReadFile(filepath.Join(cacheDir, cacheFileName))
	if err != nil {
		return empty
	}
	loaded := &cacheFile{}
	if json.Unmarshal(content, loaded) != nil ||
		loaded.FormatVersion != cacheFormatVersion ||
		loaded.GlobalKey != globalKey ||
		loaded.Entries == nil {
		return empty
	}
	return loaded
}

func saveCache(cacheDir string, cache *cacheFile) error {
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return err
	}
	content, err := json.Marshal(cache)
	if err != nil {
		return err
	}
	tmp := filepath.Join(cacheDir, cacheFileName+".tmp")
	if err := os.WriteFile(tmp, content, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(cacheDir, cacheFileName))
}
