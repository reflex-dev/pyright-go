/*
 * cacheutilsbridge.go
 *
 * The "symbolnameutils" and "typecacheutils" ops, which let pyright's own
 * symbolNameUtils.test.ts and typeCacheUtils.test.ts run unmodified against the
 * Go port.
 *
 * symbolNameUtils is eight pure string predicates, so its op is a plain
 * dispatch.
 *
 * typeCacheUtils rides on the same record-and-replay log as the type printer
 * bridge (see typebridge.go): the test constructs TypeVars, so the whole
 * construction log is replayed here before the cache logic runs. That means
 * this exercises the Go types.ts port and IsTypeSame as well as
 * typeCacheUtils.ts.
 *
 * The one deviation is documented in tools/ts-bridge/shim-typeCacheUtils.ts:
 * isEntryValid is a TypeScript closure and the protocol is unidirectional, so
 * it is evaluated on the Node side and shipped as a boolean array. That is
 * exact rather than approximate -- the original calls it for every entry as the
 * left operand of an `&&`, so nothing about it is short-circuited.
 */

package main

import (
	"encoding/json"
	"fmt"

	"github.com/microsoft/pyright/go/analyzer"
)

func handleSymbolNameUtils(req *request) (any, string) {
	switch req.Which {
	case "isPrivateName":
		return analyzer.IsPrivateName(req.Name), ""
	case "isProtectedName":
		return analyzer.IsProtectedName(req.Name), ""
	case "isPrivateOrProtectedName":
		return analyzer.IsPrivateOrProtectedName(req.Name), ""
	case "isDunderName":
		return analyzer.IsDunderName(req.Name), ""
	case "isSingleDunderName":
		return analyzer.IsSingleDunderName(req.Name), ""
	case "isConstantName":
		return analyzer.IsConstantName(req.Name), ""
	case "isTypeAliasName":
		return analyzer.IsTypeAliasName(req.Name), ""
	case "isPublicConstantOrTypeAlias":
		return analyzer.IsPublicConstantOrTypeAlias(req.Name), ""
	}
	return nil, "symbolnameutils: unknown function " + req.Which
}

// cacheUtilsRequest is the payload of the "typecacheutils" op.
type cacheUtilsRequest struct {
	Which string    `json:"which"`
	Log   []typeCmd `json:"log"`

	// matches
	Entry    *int `json:"entry"`
	Expected *int `json:"expected"`

	// add. Valid is nil when the test passes no isEntryValid.
	Entries  []*int `json:"entries"`
	Valid    []bool `json:"valid"`
	NewEntry *int   `json:"newEntry"`
}

// bridgeCacheEntry stands in for the test's TestCacheEntry. Only expectedType
// crosses the wire; the test's `value` field stays on the Node side, which
// reassembles the result from the returned indices.
type bridgeCacheEntry struct {
	ExpectedType analyzer.Type

	// Index is the entry's position in `[...entries, newEntry]`, which is what
	// the Node side maps back to its own objects.
	Index int
}

// GetExpectedType implements analyzer.ContextualTypeCacheEntry.
func (e *bridgeCacheEntry) GetExpectedType() analyzer.Type { return e.ExpectedType }

func handleTypeCacheUtils(payload json.RawMessage) (result any, errMsg string) {
	defer func() {
		if r := recover(); r != nil {
			result = nil
			errMsg = fmt.Sprint(r)
		}
	}()

	var req cacheUtilsRequest
	if err := json.Unmarshal(payload, &req); err != nil {
		return nil, "typecacheutils: " + err.Error()
	}

	b := &typeBridge{handles: map[int]any{}}
	for _, cmd := range req.Log {
		if err := b.replay(cmd); err != nil {
			return nil, err.Error()
		}
	}

	// resolve turns a handle (or the absence of one) into a Type, standing in
	// for `Type | undefined`.
	resolve := func(handle *int) (analyzer.Type, error) {
		if handle == nil {
			return nil, nil
		}
		target, ok := b.handles[*handle]
		if !ok {
			return nil, fmt.Errorf("typecacheutils: unknown handle %d", *handle)
		}
		t, ok := target.(analyzer.Type)
		if !ok {
			return nil, fmt.Errorf("typecacheutils: handle %d is not a type", *handle)
		}
		return t, nil
	}

	switch req.Which {
	case "matches":
		entryType, err := resolve(req.Entry)
		if err != nil {
			return nil, err.Error()
		}
		expectedType, err := resolve(req.Expected)
		if err != nil {
			return nil, err.Error()
		}
		entry := &bridgeCacheEntry{ExpectedType: entryType}
		return analyzer.ContextualTypeCacheEntryMatches(entry, expectedType), ""

	case "add":
		if req.Valid != nil && len(req.Valid) != len(req.Entries) {
			return nil, "typecacheutils: valid and entries have different lengths"
		}

		cacheEntries := make([]*bridgeCacheEntry, 0, len(req.Entries))
		for i, handle := range req.Entries {
			entryType, err := resolve(handle)
			if err != nil {
				return nil, err.Error()
			}
			cacheEntries = append(cacheEntries, &bridgeCacheEntry{ExpectedType: entryType, Index: i})
		}

		newEntryType, err := resolve(req.NewEntry)
		if err != nil {
			return nil, err.Error()
		}
		newEntry := &bridgeCacheEntry{ExpectedType: newEntryType, Index: len(req.Entries)}

		var isEntryValid func(entry *bridgeCacheEntry) bool
		if req.Valid != nil {
			valid := req.Valid
			isEntryValid = func(entry *bridgeCacheEntry) bool { return valid[entry.Index] }
		}

		survivors := analyzer.AddContextualTypeCacheEntry(cacheEntries, newEntry, isEntryValid)

		indices := make([]int, 0, len(survivors))
		for _, entry := range survivors {
			indices = append(indices, entry.Index)
		}
		return indices, ""
	}

	return nil, "typecacheutils: unknown function " + req.Which
}
