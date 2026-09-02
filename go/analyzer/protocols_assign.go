/*
 * protocols_assign.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/protocols.ts (pyright 1.1.412):
 * assignClassToProtocol, assignModuleToProtocol, assignToProtocolInternal,
 * createProtocolConstraints, getProtocolCompatibility, setProtocolCompatibility,
 * makeProtocolCompatibilityCacheClassKey, isConstraintTrackerSame.
 *
 * Structural typing: does this class have every member the protocol asks for,
 * with a compatible type? The check walks the protocol's MRO, and for each
 * declared symbol looks the same name up on the source and compares.
 *
 * Several things make it more than a member-by-member type check:
 *
 *   - BINDING. A protocol's `def f(self) -> int` and a class's are both
 *     unbound function types at this point, so both sides are bound to the
 *     source before comparing, or the `self` parameters would be compared
 *     against each other.
 *   - VARIANCE. A protocol member that is a mutable variable is INVARIANT --
 *     an attribute that can be written must match exactly, not merely be
 *     assignable. A method, or a Final variable, is covariant.
 *   - READ-ONLY. A read-only source member cannot satisfy a writable protocol
 *     member. Properties, Final variables and named-tuple fields all count as
 *     read-only, and the property case has to look for `__set__`/`__delete__`
 *     to decide whether the PROTOCOL side is writable.
 *   - ClassVar. The protocol and the class must agree on whether a member is a
 *     ClassVar, and the rule inverts when the source is a class object rather
 *     than an instance.
 *
 * Two conventions come from typeshed rather than from the spec, and are marked
 * as such: `__slots__` and `__class_getitem__` are never compared.
 *
 * The result is cached on the SOURCE class, keyed by the protocol. The cache
 * also records "always incompatible" -- established by re-running the check with
 * both sides self-specialized -- so a class that can never satisfy a protocol is
 * not re-tested for every specialization of it.
 */

package analyzer

import (
	"strconv"

	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/localization"
)

// maxProtocolCompatibilityCacheEntries corresponds to the constant of the same
// name.
const maxProtocolCompatibilityCacheEntries = 32

// defaultMaxDiagnosticDepth corresponds to the constant of the same name.
const defaultMaxDiagnosticDepth = 5

// protocolCompatibility corresponds to the interface of the same name.
type protocolCompatibility struct {
	DestType *ClassType

	// SrcType is nil where the original leaves it undefined, meaning the source
	// is incompatible however either side is specialized.
	SrcType *ClassType

	Flags           AssignTypeFlags
	PreConstraints  *ConstraintTracker
	PostConstraints *ConstraintTracker
	IsCompatible    bool
}

// protocolAssignmentStackEntry corresponds to the interface of the same name.
type protocolAssignmentStackEntry struct {
	SrcType  *ClassType
	DestType *ClassType
}

// protocolAssignmentStack corresponds to the module-level stack of the same
// name, which breaks recursion when a protocol refers to itself.
var protocolAssignmentStack []protocolAssignmentStackEntry

// AssignClassToProtocol corresponds to assignClassToProtocol.
func AssignClassToProtocol(
	evaluator TypeEvaluator,
	destType *ClassType,
	srcType *ClassType,
	diag *common.DiagnosticAddendum,
	constraints *ConstraintTracker,
	flags AssignTypeFlags,
	recursionCount int,
) bool {
	// The original's comment: we assume that destType is an instantiable class
	// that is a protocol. The srcType can be an instantiable class or a class
	// instance.
	assert(IsInstantiableClass(destType) && ClassTypeIsProtocolClass(destType),
		"expected an instantiable protocol class")

	// The original's comment: a literal source type should never affect protocol
	// matching, so strip the literal type if it's present. This helps conserve on
	// cache entries.
	if srcType.Priv.LiteralValue != nil {
		if stripped, ok := evaluator.StripLiteralValue(srcType).(*ClassType); ok {
			srcType = stripped
		}
	}

	enforceInvariance := (flags & AssignTypeFlagsInvariant) != 0

	// The original's comment: use a stack of pending protocol class evaluations to
	// detect recursion. This can happen when a protocol class refers to itself.
	for _, entry := range protocolAssignmentStack {
		if IsTypeSame(entry.SrcType, srcType, TypeSameOptions{}, 0) &&
			IsTypeSame(entry.DestType, destType, TypeSameOptions{}, 0) {
			return !enforceInvariance
		}
	}

	// The original's comment: see if we've already determined that this class is
	// compatible with this protocol.
	compat := getProtocolCompatibility(destType, srcType, flags, constraints)

	if compat != nil {
		if compat.IsCompatible {
			if compat.PostConstraints != nil && constraints != nil {
				constraints.CopyFromClone(compat.PostConstraints)
			}
			return true
		}

		// The original's comment: if it's known not to be compatible and the caller
		// hasn't requested any detailed diagnostic information or we've already
		// exceeded the depth of diagnostic information that will be displayed, we can
		// return false immediately.
		if diag == nil || diag.GetNestLevel() > defaultMaxDiagnosticDepth {
			return false
		}
	}

	protocolAssignmentStack = append(protocolAssignmentStack,
		protocolAssignmentStackEntry{SrcType: srcType, DestType: destType})

	var clonedConstraints *ConstraintTracker
	if constraints != nil {
		clonedConstraints = constraints.Clone()
	}

	isCompatible := assignToProtocolInternal(evaluator, destType, srcType, diag,
		constraints, flags, recursionCount)

	protocolAssignmentStack = protocolAssignmentStack[:len(protocolAssignmentStack)-1]

	// The original's comment: cache the results for next time.
	if compat == nil {
		var postConstraints *ConstraintTracker
		if constraints != nil {
			postConstraints = constraints.Clone()
		}
		setProtocolCompatibility(evaluator, destType, srcType, flags,
			clonedConstraints, postConstraints, isCompatible, recursionCount)
	}

	return isCompatible
}

// AssignModuleToProtocol corresponds to assignModuleToProtocol.
func AssignModuleToProtocol(
	evaluator TypeEvaluator,
	destType *ClassType,
	srcType *ModuleType,
	diag *common.DiagnosticAddendum,
	constraints *ConstraintTracker,
	flags AssignTypeFlags,
	recursionCount int,
) bool {
	return assignToProtocolInternal(evaluator, destType, srcType, diag,
		constraints, flags, recursionCount)
}

// makeProtocolCompatibilityCacheClassKey corresponds to the function of the same
// name.
//
// Its comment: create a unique key based on the full name of the class and its
// type source ID, which is derived from the character offset of the class in the
// source file.
func makeProtocolCompatibilityCacheClassKey(classType *ClassType) string {
	// TypeSourceId is an integer in the port, so it is formatted rather than
	// converted -- string(int) would build a single rune.
	return classType.Shared.FullName + "." + strconv.Itoa(int(classType.Shared.TypeSourceID))
}

// getProtocolCompatibility corresponds to the function of the same name. It
// returns nil where the original returns undefined.
func getProtocolCompatibility(
	destType *ClassType, srcType *ClassType, flags AssignTypeFlags, constraints *ConstraintTracker,
) *protocolCompatibility {
	m, ok := srcType.Shared.ProtocolCompatibility.(map[string][]*protocolCompatibility)
	if !ok || m == nil {
		return nil
	}

	entries, found := m[makeProtocolCompatibilityCacheClassKey(destType)]
	if !found {
		return nil
	}

	sameOptions := TypeSameOptions{HonorIsTypeArgExplicit: true, HonorTypeForm: true}

	for _, entry := range entries {
		if entry.Flags != flags {
			continue
		}

		// A nil SrcType means the entry recorded that no specialization of the
		// source can satisfy this protocol.
		if entry.SrcType == nil {
			if ClassTypeIsSameGenericClass(entry.DestType, destType, 0) {
				return entry
			}
			continue
		}

		if IsTypeSame(entry.DestType, destType, sameOptions, 0) &&
			IsTypeSame(entry.SrcType, srcType, sameOptions, 0) &&
			isConstraintTrackerSame(constraints, entry.PreConstraints) {
			return entry
		}
	}

	return nil
}

// setProtocolCompatibility corresponds to the function of the same name.
func setProtocolCompatibility(
	evaluator TypeEvaluator,
	destType *ClassType,
	srcType *ClassType,
	flags AssignTypeFlags,
	preConstraints *ConstraintTracker,
	postConstraints *ConstraintTracker,
	isCompatible bool,
	recursionCount int,
) {
	m, _ := srcType.Shared.ProtocolCompatibility.(map[string][]*protocolCompatibility)
	if m == nil {
		m = map[string][]*protocolCompatibility{}
		srcType.Shared.ProtocolCompatibility = m
	}

	classKey := makeProtocolCompatibilityCacheClassKey(destType)
	entries := m[classKey]

	// The original's comment: see if the srcType is always incompatible regardless
	// of how it and the destType are specialized.
	isAlwaysIncompatible := false

	if !isCompatible && !hasGenericIncompatibilityEntry(entries, flags, destType) {
		genericDestType := destType
		if RequiresTypeArgs(destType) {
			genericDestType = SelfSpecializeClass(destType, &SelfSpecializeOptions{OverrideTypeArgs: true})
		}
		genericSrcType := srcType
		if RequiresTypeArgs(srcType) {
			genericSrcType = SelfSpecializeClass(srcType, &SelfSpecializeOptions{OverrideTypeArgs: true})
		}

		if !assignToProtocolInternal(evaluator, genericDestType, genericSrcType,
			nil, nil, flags, recursionCount) {
			isAlwaysIncompatible = true
		}
	}

	newEntry := &protocolCompatibility{
		DestType:        destType,
		Flags:           flags,
		PreConstraints:  preConstraints,
		PostConstraints: postConstraints,
		IsCompatible:    isCompatible,
	}
	if !isAlwaysIncompatible {
		newEntry.SrcType = srcType
	}

	entries = append(entries, newEntry)

	// The original's comment: make sure the cache doesn't grow too large.
	if len(entries) > maxProtocolCompatibilityCacheEntries {
		entries = entries[1:]
	}

	m[classKey] = entries
}

// hasGenericIncompatibilityEntry is the original's `entries.some(...)` guard.
func hasGenericIncompatibilityEntry(
	entries []*protocolCompatibility, flags AssignTypeFlags, destType *ClassType,
) bool {
	for _, entry := range entries {
		if entry.Flags == flags && ClassTypeIsSameGenericClass(entry.DestType, destType, 0) {
			return true
		}
	}
	return false
}

// isConstraintTrackerSame corresponds to the function of the same name.
func isConstraintTrackerSame(context1, context2 *ConstraintTracker) bool {
	if context1 == nil || context2 == nil {
		return context1 == context2
	}

	return context1.IsSame(context2)
}

// createProtocolConstraints corresponds to the function of the same name.
//
// Its comment: given a (possibly-specialized) destType and an optional
// constraint tracker, creates a new constraint tracker that combines the
// constraints from both the destType and the destConstraints.
func createProtocolConstraints(
	evaluator TypeEvaluator, destType *ClassType, constraints *ConstraintTracker,
) *ConstraintTracker {
	protocolConstraints := NewConstraintTracker()

	for index, typeParam := range destType.Shared.TypeParams {
		var entry *TypeVarConstraints
		if constraints != nil {
			entry = constraints.GetMainConstraintSet().GetTypeVar(typeParam)
		}

		if entry != nil {
			protocolConstraints.CopyBounds(entry)
			continue
		}

		if destType.Priv.TypeArgs == nil || index >= len(destType.Priv.TypeArgs) {
			continue
		}

		typeArg := destType.Priv.TypeArgs[index]
		var assignFlags AssignTypeFlags
		hasUnsolvedTypeVars := RequiresSpecialization(typeArg, nil, 0)

		// The original's comment: if the type argument has unsolved TypeVars, see if
		// they have solved values in the destConstraints.
		if hasUnsolvedTypeVars && constraints != nil {
			typeArg = evaluator.SolveAndApplyConstraints(typeArg, constraints, nil,
				&SolveConstraintsOptions{UseLowerBoundOnly: true})
			assignFlags = AssignTypeFlagsDefault
			hasUnsolvedTypeVars = RequiresSpecialization(typeArg, nil, 0)
		} else {
			assignFlags = AssignTypeFlagsPopulateExpectedType

			switch TypeVarTypeGetVariance(typeParam) {
			case VarianceInvariant:
				assignFlags |= AssignTypeFlagsInvariant
			case VarianceContravariant:
				assignFlags |= AssignTypeFlagsContravariant
			}
		}

		if !hasUnsolvedTypeVars {
			AssignTypeVar(evaluator, typeParam, typeArg, nil, protocolConstraints, assignFlags, 0)
		}
	}

	return protocolConstraints
}

// unusedLocalization keeps the import referenced from this file while the
// member-walk lives in protocols_members.go.
var _ = localization.LocAddendum
