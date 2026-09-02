/*
 * uribridge.go
 *
 * The "uri" and "urinormalize" ops, which let pyright's own uri.test.ts run
 * unmodified against the Go port. See tools/ts-bridge/shim-uri.ts for the
 * protocol and why it looks like this.
 *
 * A request carries a recipe -- how a Uri was constructed, plus the chain of
 * derivations applied to it -- and one method call to make on the result. This
 * process replays the recipe from scratch, because client.ts spawns a fresh
 * process per request and nothing survives between them.
 */

package main

import (
	"encoding/json"
	"fmt"

	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/common/uri"
)

type uriRootSpec struct {
	Kind          string `json:"kind"`
	Value         string `json:"value"`
	Name          string `json:"name"`
	Serial        int    `json:"serial"`
	CheckRelative bool   `json:"checkRelative"`
	Cwd           string `json:"cwd"`
	CaseSensitive bool   `json:"caseSensitive"`
}

type uriOp struct {
	Name string            `json:"name"`
	Args []json.RawMessage `json:"args"`
}

type uriRecipe struct {
	Root uriRootSpec `json:"root"`
	Ops  []uriOp     `json:"ops"`
}

type uriRequest struct {
	Recipe uriRecipe `json:"recipe"`
	Call   uriOp     `json:"call"`
}

type uriNormalizeRequest struct {
	Which         string `json:"which"`
	Value         string `json:"value"`
	CheckRelative bool   `json:"checkRelative"`
	Cwd           string `json:"cwd"`
}

// constantUris interns Uri.constant by the serial number the shim assigns.
// ConstantUri compares by reference, so a recipe replayed twice inside one
// request -- which is exactly what `a.equals(b)` does -- has to yield the same
// object for the same serial.
var constantUris = map[int]uri.Uri{}

// fixedDetector is a CaseSensitivityDetector whose answer was already decided
// on the Node side; see the shim's header.
type fixedDetector bool

func (d fixedDetector) IsCaseSensitive(string) bool { return bool(d) }

func buildUri(recipe uriRecipe) uri.Uri {
	var u uri.Uri

	switch recipe.Root.Kind {
	case "parse":
		u = uri.Parse(recipe.Root.Value, fixedDetector(recipe.Root.CaseSensitive))
	case "file":
		u = uriFileWithCwd(recipe.Root.Value, recipe.Root.CheckRelative, recipe.Root.Cwd,
			fixedDetector(recipe.Root.CaseSensitive))
	case "constant":
		if existing, ok := constantUris[recipe.Root.Serial]; ok {
			u = existing
		} else {
			u = uri.Constant(recipe.Root.Name)
			constantUris[recipe.Root.Serial] = u
		}
	case "empty":
		u = uri.Empty()
	default:
		panic("uri: unknown root kind " + recipe.Root.Kind)
	}

	for _, op := range recipe.Ops {
		u = applyUriOp(u, op)
	}
	return u
}

// uriFileWithCwd is uri.File with the working directory supplied rather than
// read from the process. The Node side has its own cwd and the two processes
// need not agree; the original reads process.cwd() at this point, so that is
// the value that has to be used.
func uriFileWithCwd(path string, checkRelative bool, cwd string, detector common.CaseSensitivityDetector) uri.Uri {
	if checkRelative && !common.IsRootedDiskPath(path) {
		path = common.CombinePaths(cwd, path)
	}
	return uri.File(path, detector, false)
}

func uriArgString(raw json.RawMessage) string {
	var v string
	if err := json.Unmarshal(raw, &v); err != nil {
		panic("uri: " + err.Error())
	}
	return v
}

func uriArgStrings(raw json.RawMessage) []string {
	var v []string
	if err := json.Unmarshal(raw, &v); err != nil {
		panic("uri: " + err.Error())
	}
	return v
}

func uriArgInt(raw json.RawMessage) int {
	var v int
	if err := json.Unmarshal(raw, &v); err != nil {
		panic("uri: " + err.Error())
	}
	return v
}

// uriArgUri decodes a nested recipe, which stands in for a `Uri | undefined`
// argument. A JSON null is the undefined.
func uriArgUri(raw json.RawMessage) uri.Uri {
	if string(raw) == "null" {
		return nil
	}
	var recipe uriRecipe
	if err := json.Unmarshal(raw, &recipe); err != nil {
		panic("uri: " + err.Error())
	}
	return buildUri(recipe)
}

// applyUriOp performs one Uri-returning derivation.
func applyUriOp(u uri.Uri, op uriOp) uri.Uri {
	switch op.Name {
	case "root":
		return u.Root()
	case "packageUri":
		return u.PackageUri()
	case "packageStubUri":
		return u.PackageStubUri()
	case "initPyUri":
		return u.InitPyUri()
	case "initPyiUri":
		return u.InitPyiUri()
	case "pytypedUri":
		return u.PytypedUri()
	case "addPath":
		return u.AddPath(uriArgString(op.Args[0]))
	case "getDirectory":
		return u.GetDirectory()
	case "resolvePaths":
		return u.ResolvePaths(uriArgStrings(op.Args[0])...)
	case "combinePaths":
		return u.CombinePaths(uriArgStrings(op.Args[0])...)
	case "combinePathsUnsafe":
		return u.CombinePathsUnsafe(uriArgStrings(op.Args[0])...)
	case "stripExtension":
		return u.StripExtension()
	case "stripAllExtensions":
		return u.StripAllExtensions()
	case "replaceExtension":
		return u.ReplaceExtension(uriArgString(op.Args[0]))
	case "addExtension":
		return u.AddExtension(uriArgString(op.Args[0]))
	case "withFragment":
		return u.WithFragment(uriArgString(op.Args[0]))
	case "withQuery":
		return u.WithQuery(uriArgString(op.Args[0]))

	// uriUtils' Uri-returning functions, which the shim treats as derivations.
	case "uriUtils.getWildcardRoot":
		return uri.GetWildcardRoot(u, uriArgString(op.Args[0]))
	}
	panic("uri: unknown derivation " + op.Name)
}

// alwaysMatch is the Regexp the "matchesRegexTarget" call passes in. The shim
// runs the real RegExp on the string this records, so what MatchesRegex is
// asked to match is what crosses the wire, not whether it matched.
type alwaysMatch struct{ seen *string }

func (m alwaysMatch) MatchString(s string) bool {
	*m.seen = s
	return true
}

func (m alwaysMatch) String() string { return "" }

// readUri performs one scalar-returning call.
func readUri(u uri.Uri, op uriOp) any {
	switch op.Name {
	case "key":
		return u.Key()
	case "scheme":
		return u.Scheme()
	case "fileName":
		return u.FileName()
	case "fileNameWithoutExtensions":
		return u.FileNameWithoutExtensions()
	case "lastExtension":
		return u.LastExtension()
	case "isCaseSensitive":
		return u.IsCaseSensitive()
	case "fragment":
		return u.Fragment()
	case "query":
		return u.Query()
	case "isEmpty":
		return u.IsEmpty()
	case "toString":
		return u.String()
	case "toUserVisibleString":
		return u.ToUserVisibleString()
	case "isRoot":
		return u.IsRoot()
	case "isLocal":
		return u.IsLocal()
	case "isUntitled":
		return u.IsUntitled()
	case "getRootPathLength":
		return u.GetRootPathLength()
	case "getPathLength":
		return u.GetPathLength()
	case "getPathComponents":
		return u.GetPathComponents()
	case "getPath":
		return u.GetPath()
	case "getFilePath":
		return u.GetFilePath()
	case "getShortenedFileName":
		return u.GetShortenedFileName(uriArgInt(op.Args[0]))
	case "pathStartsWith":
		return u.PathStartsWith(uriArgString(op.Args[0]))
	case "pathEndsWith":
		return u.PathEndsWith(uriArgString(op.Args[0]))
	case "pathIncludes":
		return u.PathIncludes(uriArgString(op.Args[0]))
	case "hasExtension":
		return u.HasExtension(uriArgString(op.Args[0]))
	case "containsExtension":
		return u.ContainsExtension(uriArgString(op.Args[0]))
	case "isChild":
		return u.IsChild(uriArgUri(op.Args[0]))
	case "equals":
		return u.Equals(uriArgUri(op.Args[0]))
	case "startsWith":
		return u.StartsWith(uriArgUri(op.Args[0]))
	case "getRelativePathComponents":
		return u.GetRelativePathComponents(uriArgUri(op.Args[0]))
	case "getRelativePath":
		// The TypeScript returns `string | undefined`; null becomes undefined
		// again in the shim.
		if result, ok := u.GetRelativePath(uriArgUri(op.Args[0])); ok {
			return result
		}
		return nil
	case "matchesRegexTarget":
		var seen string
		u.MatchesRegex(alwaysMatch{seen: &seen})
		return seen

	// uriUtils' scalar-returning functions.
	case "uriUtils.getWildcardRegexPattern":
		return uri.GetWildcardRegexPattern(u, uriArgString(op.Args[0]))
	}
	panic("uri: unknown method " + op.Name)
}

func handleUri(payload json.RawMessage) (result any, errMsg string) {
	defer func() {
		if r := recover(); r != nil {
			result = nil
			errMsg = uriPanicMessage(r)
		}
	}()

	var req uriRequest
	if err := json.Unmarshal(payload, &req); err != nil {
		return nil, "uri: " + err.Error()
	}

	return readUri(buildUri(req.Recipe), req.Call), ""
}

func handleUriNormalize(payload json.RawMessage) (result any, errMsg string) {
	defer func() {
		if r := recover(); r != nil {
			result = nil
			errMsg = uriPanicMessage(r)
		}
	}()

	var req uriNormalizeRequest
	if err := json.Unmarshal(payload, &req); err != nil {
		return nil, "urinormalize: " + err.Error()
	}

	switch req.Which {
	case "maybeUri":
		return uri.MaybeUri(req.Value), ""

	case "file":
		// Uri.file always consults the detector.
		var probe recordingDetector
		uriFileWithCwd(req.Value, req.CheckRelative, req.Cwd, &probe)
		return probe.value, ""

	case "parse":
		// Uri.parse consults it only when the normalized scheme is 'file'; an
		// empty string short-circuits to Uri.empty() before that.
		var probe recordingDetector
		uri.Parse(req.Value, &probe)
		if !probe.called {
			return nil, ""
		}
		return probe.value, ""
	}

	return nil, "urinormalize: unknown function " + req.Which
}

// recordingDetector captures the string the factory would have handed the real
// detector, so the Node side can call it with exactly that argument.
type recordingDetector struct {
	called bool
	value  string
}

func (d *recordingDetector) IsCaseSensitive(uriStr string) bool {
	d.called = true
	d.value = uriStr
	return true
}

// uriPanicMessage unwraps the vscode-uri validation errors, which the
// TypeScript raises as thrown Errors and uri.test.ts asserts on with
// assert.throws.
func uriPanicMessage(r any) string {
	if err, ok := r.(error); ok {
		return err.Error()
	}
	return fmt.Sprint(r)
}

// uriUtilsRequest is the payload of the "uriutils" op, which carries the
// uriUtils functions that take more than one Uri and so cannot ride on a single
// recipe.
type uriUtilsRequest struct {
	Which    string        `json:"which"`
	Folders  [][]uriRecipe `json:"folders"`
	Excludes []uriRecipe   `json:"excludes"`
}

func handleUriUtils(payload json.RawMessage) (result any, errMsg string) {
	defer func() {
		if r := recover(); r != nil {
			result = nil
			errMsg = uriPanicMessage(r)
		}
	}()

	var req uriUtilsRequest
	if err := json.Unmarshal(payload, &req); err != nil {
		return nil, "uriutils: " + err.Error()
	}

	switch req.Which {
	case "deduplicateFolders":
		// The survivors are reported as indices into the flattened input, so
		// the Node side can map them back to its own objects.
		indexOf := map[uri.Uri]int{}
		listOfFolders := make([][]uri.Uri, 0, len(req.Folders))
		next := 0
		for _, folders := range req.Folders {
			built := make([]uri.Uri, 0, len(folders))
			for _, recipe := range folders {
				u := buildUri(recipe)
				built = append(built, u)
				// Uris are interned, so the same path in two input lists is the
				// same object; the first index is the one deduplicateFolders
				// would have kept.
				if _, seen := indexOf[u]; !seen {
					indexOf[u] = next
				}
				next++
			}
			listOfFolders = append(listOfFolders, built)
		}

		excludes := make([]uri.Uri, 0, len(req.Excludes))
		for _, recipe := range req.Excludes {
			excludes = append(excludes, buildUri(recipe))
		}

		survivors := uri.DeduplicateFolders(listOfFolders, excludes)
		indices := make([]int, 0, len(survivors))
		for _, u := range survivors {
			index, ok := indexOf[u]
			if !ok {
				return nil, "uriutils: deduplicateFolders returned a folder that was not an input"
			}
			indices = append(indices, index)
		}
		return indices, ""
	}

	return nil, "uriutils: unknown function " + req.Which
}
