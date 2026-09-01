/*
 * pathutilsbridge.go
 *
 * The "pathutils" op, which lets pyright's own pathUtils.test.ts run unmodified
 * against the Go port. See tools/ts-bridge/shim-pathUtils.ts.
 *
 * Everything in pathUtils.ts is a pure function of strings, so the payload is
 * just a function name and a positional argument list. Arguments arrive as
 * json.RawMessage and are decoded per case, because the list is heterogeneous
 * (strings, string arrays, booleans) and Go has no `any[]` that survives a
 * round trip without a type switch either way.
 */

package main

import (
	"encoding/json"
	"fmt"

	"github.com/microsoft/pyright/go/common"
)

type pathUtilsRequest struct {
	Which string            `json:"which"`
	Args  []json.RawMessage `json:"args"`
}

// pathUtilsArgs decodes the positional arguments of one call.
type pathUtilsArgs struct {
	raw []json.RawMessage
}

func (a *pathUtilsArgs) str(i int) string {
	var v string
	a.decode(i, &v)
	return v
}

func (a *pathUtilsArgs) strs(i int) []string {
	var v []string
	a.decode(i, &v)
	if v == nil {
		v = []string{}
	}
	return v
}

func (a *pathUtilsArgs) boolean(i int) bool {
	var v bool
	a.decode(i, &v)
	return v
}

func (a *pathUtilsArgs) decode(i int, out any) {
	if i >= len(a.raw) {
		panic(fmt.Sprintf("pathutils: missing argument %d", i))
	}
	if err := json.Unmarshal(a.raw[i], out); err != nil {
		panic("pathutils: " + err.Error())
	}
}

func handlePathUtils(payload json.RawMessage) (result any, errMsg string) {
	defer func() {
		if r := recover(); r != nil {
			result = nil
			errMsg = fmt.Sprint(r)
		}
	}()

	var req pathUtilsRequest
	if err := json.Unmarshal(payload, &req); err != nil {
		return nil, "pathutils: " + err.Error()
	}
	a := &pathUtilsArgs{raw: req.Args}

	switch req.Which {
	case "getPathComponents":
		return common.GetPathComponents(a.str(0)), ""
	case "reducePathComponents":
		return common.ReducePathComponents(a.strs(0)), ""
	case "combinePathComponents":
		return common.CombinePathComponents(a.strs(0)), ""
	case "combinePaths":
		return common.CombinePaths(a.str(0), a.strs(1)...), ""
	case "resolvePaths":
		return common.ResolvePaths(a.str(0), a.strs(1)...), ""
	case "normalizeSlashes":
		return common.NormalizeSlashes(a.str(0)), ""
	case "getRelativePath":
		// The TypeScript returns `string | undefined`; null becomes undefined
		// again in the shim.
		if result, ok := common.GetRelativePath(a.str(0), a.str(1)); ok {
			return result, ""
		}
		return nil, ""
	case "ensureTrailingDirectorySeparator":
		return common.EnsureTrailingDirectorySeparator(a.str(0)), ""
	case "hasTrailingDirectorySeparator":
		return common.HasTrailingDirectorySeparator(a.str(0)), ""
	case "stripTrailingDirectorySeparator":
		return common.StripTrailingDirectorySeparator(a.str(0)), ""
	case "getFileExtension":
		return common.GetFileExtension(a.str(0), a.boolean(1)), ""
	case "getFileName":
		return common.GetFileName(a.str(0)), ""
	case "stripFileExtension":
		return common.StripFileExtension(a.str(0), a.boolean(1)), ""
	case "getRootLength":
		return common.GetRootLength(a.str(0)), ""
	case "isRootedDiskPath":
		return common.IsRootedDiskPath(a.str(0)), ""
	case "isDiskPathRoot":
		return common.IsDiskPathRoot(a.str(0)), ""
	case "getWildcardRegexPattern":
		return common.GetWildcardRegexPattern(a.str(0), a.str(1)), ""
	case "isDirectoryWildcardPatternPresent":
		return common.IsDirectoryWildcardPatternPresent(a.str(0)), ""
	case "getWildcardRoot":
		return common.GetWildcardRoot(a.str(0), a.str(1)), ""
	case "containsPath":
		return common.ContainsPath(a.str(0), a.str(1), a.boolean(2)), ""
	case "containsPathIn":
		return common.ContainsPathIn(a.str(0), a.str(1), a.str(2), a.boolean(3)), ""
	case "getAnyExtensionFromPath":
		return common.GetAnyExtensionFromPath(a.str(0)), ""
	case "getAnyExtensionFromPathIn":
		return common.GetAnyExtensionFromPathIn(a.str(0), a.strs(1), a.boolean(2)), ""
	case "getBaseFileName":
		return common.GetBaseFileName(a.str(0)), ""
	case "getBaseFileNameIn":
		return common.GetBaseFileNameIn(a.str(0), a.strs(1), a.boolean(2)), ""
	}

	return nil, "pathutils: unknown function " + req.Which
}
