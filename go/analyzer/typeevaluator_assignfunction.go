/*
 * typeevaluator_assignfunction.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/typeEvaluator.ts (pyright 1.1.412):
 * assignFunction, assignParam, adjustSourceParamDetailsForDestVariadic.
 *
 * Whether one callable can stand in for another. Parameters are CONTRAVARIANT
 * and the return type is COVARIANT, so almost every assignment in this file runs
 * backwards relative to the ordinary direction: for `src` to be usable where
 * `dest` is expected, every parameter of `dest` must be acceptable to `src`.
 *
 * Names matter as much as types, because Python callers may pass by keyword. A
 * parameter that is positional-only in the source cannot satisfy one that is
 * addressable by name in the destination, and two parameters that are both
 * name-addressable must agree on the name. Only the positional-only marker
 * releases the source from that.
 *
 * Counts are checked in both directions and neither is symmetric. A source with
 * FEWER positional parameters is fine if it has `*args` to absorb the rest, or
 * if the missing ones have defaults. A source with MORE is fine if the extras
 * have defaults or are reachable by keyword. A destination with `*args` or
 * `**kwargs` demands the same of the source; the converse is allowed.
 *
 * PARAM SPECS make the whole thing sequence-shaped rather than pairwise. When
 * the destination ends in `*P.args, **P.kwargs`, the parameters the source has
 * left over after matching the explicitly-declared ones are collected into a
 * synthesized function and assigned to `P` as a unit -- which is how
 * `Concatenate` works.
 *
 * adjustSourceParamDetailsForDestVariadic runs before any of it, and rewrites
 * the source's parameter list: a destination `*args: *Ts` corresponds to a RUN
 * of source positionals, so they are packed into one synthesized tuple parameter
 * before matching begins.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/localization"
	"github.com/microsoft/pyright/go/parser"
)

// assignFunction corresponds to the function of the same name.
func (e *typeEvaluator) assignFunction(
	destType *FunctionType,
	srcType *FunctionType,
	diag *common.DiagnosticAddendum,
	constraints *ConstraintTracker,
	flags AssignTypeFlags,
	recursionCount int,
) bool {
	canAssign := true
	checkReturnType := (flags & AssignTypeFlagsSkipReturnTypeCheck) == 0
	isContra := (flags & AssignTypeFlagsContravariant) != 0
	flags &^= AssignTypeFlagsSkipReturnTypeCheck

	destParamSpec := FunctionTypeGetParamSpecFromArgsKwargs(destType)
	if destParamSpec != nil {
		destType = FunctionTypeCloneRemoveParamSpecArgsKwargs(destType, false)
	}

	srcParamSpec := FunctionTypeGetParamSpecFromArgsKwargs(srcType)
	if srcParamSpec != nil {
		srcType = FunctionTypeCloneRemoveParamSpecArgsKwargs(srcType, false)
	}

	detailsOptions := &ParamListDetailsOptions{
		DisallowExtraKwargsForTd: (flags & AssignTypeFlagsDisallowExtraKwargsForTd) != 0,
	}
	destParamDetails := GetParamListDetails(destType, detailsOptions)
	srcParamDetails := GetParamListDetails(srcType, detailsOptions)

	if isContra {
		e.adjustSourceParamDetailsForDestVariadic(destParamDetails, srcParamDetails)
	} else {
		e.adjustSourceParamDetailsForDestVariadic(srcParamDetails, destParamDetails)
	}

	targetIncludesParamSpec := destParamSpec != nil
	if isContra {
		targetIncludesParamSpec = srcParamSpec != nil
	}

	destPositionalCount := len(destParamDetails.Params)
	if destParamDetails.FirstKeywordOnlyIndex != nil {
		destPositionalCount = *destParamDetails.FirstKeywordOnlyIndex
	}
	srcPositionalCount := len(srcParamDetails.Params)
	if srcParamDetails.FirstKeywordOnlyIndex != nil {
		srcPositionalCount = *srcParamDetails.FirstKeywordOnlyIndex
	}

	a := &functionAssigner{
		e:                   e,
		destType:            destType,
		srcType:             srcType,
		diag:                diag,
		constraints:         constraints,
		flags:               flags,
		recursionCount:      recursionCount,
		destParamDetails:    destParamDetails,
		srcParamDetails:     srcParamDetails,
		destPositionalCount: destPositionalCount,
		srcPositionalCount:  srcPositionalCount,
		canAssign:           true,
	}

	a.matchPositionalParams(targetIncludesParamSpec)
	a.checkPositionOnlyCounts(targetIncludesParamSpec)
	a.matchPositionalCountMismatch(targetIncludesParamSpec)
	a.matchArgsParams(srcParamSpec)

	if !targetIncludesParamSpec {
		a.matchKeywordParams(srcParamSpec)
	}

	canAssign = a.canAssign

	if (flags & AssignTypeFlagsOverloadOverlap) != 0 {
		// The original's comment: if we're checking for full overlapping overloads
		// and the source is a gradual form, the dest must also be a gradual form.
		if FunctionTypeIsGradualCallableForm(srcType) && !FunctionTypeIsGradualCallableForm(destType) {
			canAssign = false
		}

		// The original's comment: if the src contains a ParamSpec the dest must also.
		if srcParamSpec != nil && destParamSpec == nil {
			canAssign = false
		}
	}

	// The original's comment: if the source and the dest are using the same
	// ParamSpec, any additional concatenated parameters must match.
	if targetIncludesParamSpec && nameWithScopeOf(srcParamSpec) == nameWithScopeOf(destParamSpec) {
		if len(srcParamDetails.Params) != len(destParamDetails.Params) {
			canAssign = false
		}
	}

	// The original's comment: are we assigning to a function with a ParamSpec?
	if targetIncludesParamSpec {
		if !a.assignRemainingToParamSpec(isContra, srcParamSpec, destParamSpec) {
			canAssign = false
		}
	}

	// The original's comment: match the return parameter.
	if checkReturnType && !a.matchReturnType(destType, srcType, diag, constraints, flags, recursionCount) {
		canAssign = false
	}

	return canAssign
}

// nameWithScopeOf is the original's `paramSpec?.priv.nameWithScope`, where two
// absent ParamSpecs compare equal.
func nameWithScopeOf(paramSpec *TypeVarType) string {
	if paramSpec == nil {
		return ""
	}
	return paramSpec.Priv.NameWithScope
}

// functionAssigner holds the state that the original keeps in closures over the
// body of assignFunction.
type functionAssigner struct {
	e              *typeEvaluator
	destType       *FunctionType
	srcType        *FunctionType
	diag           *common.DiagnosticAddendum
	constraints    *ConstraintTracker
	flags          AssignTypeFlags
	recursionCount int

	destParamDetails *ParamListDetails
	srcParamDetails  *ParamListDetails

	destPositionalCount int
	srcPositionalCount  int

	skippedPosParamIndices []int
	canAssign              bool
}

// matchPositionalParams is the original's "match positional parameters" loop.
func (a *functionAssigner) matchPositionalParams(targetIncludesParamSpec bool) {
	positionalsToMatch := a.destPositionalCount
	if a.srcPositionalCount < positionalsToMatch {
		positionalsToMatch = a.srcPositionalCount
	}

	for paramIndex := 0; paramIndex < positionalsToMatch; paramIndex++ {
		if paramIndex == 0 && a.destType.Shared.MethodClass != nil &&
			(a.flags&AssignTypeFlagsSkipSelfClsParamCheck) != 0 {
			if FunctionTypeIsInstanceMethod(a.destType) || FunctionTypeIsClassMethod(a.destType) {
				continue
			}
		}

		// The original's comment: skip over the *args parameter since it's handled
		// separately below.
		if a.destParamDetails.ArgsIndex != nil && paramIndex == *a.destParamDetails.ArgsIndex {
			if !IsUnpackedTypeVarTuple(a.destParamDetails.Params[*a.destParamDetails.ArgsIndex].Type) {
				a.skippedPosParamIndices = append(a.skippedPosParamIndices, paramIndex)
			}
			continue
		}

		destParam := a.destParamDetails.Params[paramIndex]
		srcParam := a.srcParamDetails.Params[paramIndex]

		if !a.checkParamNames(destParam, srcParam) {
			a.canAssign = false
		}

		if destParam.DefaultType != nil {
			if srcParam.DefaultType == nil &&
				(a.srcParamDetails.ArgsIndex == nil || paramIndex != *a.srcParamDetails.ArgsIndex) {
				a.addAddendum(localization.LocAddendum.FunctionParamDefaultMissing().Format(
					derefStr(srcParam.Param.Name)))
				a.canAssign = false
			}

			// The original's comment: if we're performing a partial overload match
			// and both the source and dest parameters provide defaults, assume that
			// there could be a match.
			if (a.flags&AssignTypeFlagsPartialOverloadOverlap) != 0 && srcParam.DefaultType != nil {
				continue
			}
		}

		// The original's comment: handle the special case of an overloaded __init__
		// method whose self parameter is annotated.
		if paramIndex == 0 && a.srcType.Shared.Name == "__init__" &&
			FunctionTypeIsInstanceMethod(a.srcType) && a.destType.Shared.Name == "__init__" &&
			FunctionTypeIsInstanceMethod(a.destType) && FunctionTypeIsOverloaded(a.destType) &&
			FunctionParamIsTypeDeclared(destParam.Param) {
			continue
		}

		a.assignOnePositional(destParam, srcParam, paramIndex)
	}
}

// checkParamNames is the original's name-compatibility block. Reports whether
// the names are acceptable.
func (a *functionAssigner) checkParamNames(destParam, srcParam *VirtualParamDetails) bool {
	destParamName := derefStr(destParam.Param.Name)
	srcParamName := derefStr(srcParam.Param.Name)

	if destParamName == "" {
		return true
	}

	isDestPositionalOnly := destParam.Kind == ParamKindPositional || destParam.Kind == ParamKindExpandedArgs
	if isDestPositionalOnly ||
		destParam.Param.Category == parser.ParamCategoryArgsList ||
		srcParam.Param.Category == parser.ParamCategoryArgsList {
		return true
	}

	if srcParam.Kind == ParamKindPositional || srcParam.Kind == ParamKindExpandedArgs {
		a.addAddendum(localization.LocAddendum.FunctionParamPositionOnly().Format(destParamName))
		return false
	}

	if destParamName != srcParamName {
		a.addAddendum(localization.LocAddendum.FunctionParamName().Format(srcParamName, destParamName))
		return false
	}

	return true
}

// assignOnePositional is the original's per-parameter assignment plus the
// keyword-reachability check that follows it.
func (a *functionAssigner) assignOnePositional(destParam, srcParam *VirtualParamDetails, paramIndex int) {
	if IsUnpacked(srcParam.Type) {
		a.canAssign = false
		return
	}

	if !a.e.assignParam(destParam.Type, srcParam.Type, &paramIndex,
		createAddendumOrNil(a.diag), a.constraints, a.flags, a.recursionCount) {
		// The original's comment: handle the special case where the source parameter
		// is a synthesized TypeVar for "self" or "cls".
		srcTypeVar, isTypeVar := srcParam.Type.(*TypeVarType)
		if (a.flags&AssignTypeFlagsSkipSelfClsTypeCheck) == 0 || !isTypeVar ||
			!srcTypeVar.Shared.IsSynthesized {
			a.canAssign = false
		}
		return
	}

	if destParam.Kind == ParamKindPositional || destParam.Kind == ParamKindExpandedArgs ||
		srcParam.Kind != ParamKindPositional || a.srcParamDetails.KwargsIndex != nil {
		return
	}

	// The destination parameter is reachable by keyword but the source's is not,
	// and the source has no **kwargs to absorb it -- unless some other source
	// parameter carries the same name as a keyword.
	for _, p := range a.srcParamDetails.Params {
		if p.Kind == ParamKindKeyword && p.Param.Category == parser.ParamCategorySimple &&
			derefStr(p.Param.Name) == derefStr(destParam.Param.Name) {
			return
		}
	}

	a.addMessage(localization.LocAddendum.NamedParamMissingInSource().Format(
		derefStr(destParam.Param.Name)))
	a.canAssign = false
}

// checkPositionOnlyCounts is the original's argsPositionOnly check.
func (a *functionAssigner) checkPositionOnlyCounts(targetIncludesParamSpec bool) {
	if FunctionTypeIsGradualCallableForm(a.destType) || targetIncludesParamSpec {
		return
	}

	if a.destParamDetails.FirstPositionOrKeywordIndex < a.srcParamDetails.PositionOnlyParamCount {
		a.addAddendum(localization.LocAddendum.ArgsPositionOnly().Format(
			a.srcParamDetails.PositionOnlyParamCount,
			a.destParamDetails.FirstPositionOrKeywordIndex))
		a.canAssign = false
	}
}

// matchPositionalCountMismatch handles the two directions in which the
// positional counts can disagree.
func (a *functionAssigner) matchPositionalCountMismatch(targetIncludesParamSpec bool) {
	if a.destPositionalCount < a.srcPositionalCount && !targetIncludesParamSpec {
		// The original's comment: add any remaining positional parameter indices to
		// the list that need to be validated.
		for i := a.destPositionalCount; i < a.srcPositionalCount; i++ {
			a.skippedPosParamIndices = append(a.skippedPosParamIndices, i)
		}

		a.checkSkippedPositionals()
		return
	}

	if a.srcPositionalCount >= a.destPositionalCount {
		return
	}

	if a.srcParamDetails.ArgsIndex != nil {
		a.matchDestPositionalsAgainstSrcArgs()
		return
	}

	if a.srcParamDetails.ParamSpec == nil {
		a.reportTooFewParams()
	}
}

// checkSkippedPositionals is the original's loop over skippedPosParamIndices.
func (a *functionAssigner) checkSkippedPositionals() {
	for _, i := range a.skippedPosParamIndices {
		// The original's comment: if the dest has an *args parameter, make sure it
		// can accept the remaining positional arguments in the source.
		if a.destParamDetails.ArgsIndex != nil {
			destArgsType := a.destParamDetails.Params[*a.destParamDetails.ArgsIndex].Type
			srcParamType := a.srcParamDetails.Params[i].Type
			index := i
			if !a.e.assignParam(destArgsType, srcParamType, &index,
				createAddendumOrNil(a.diag), a.constraints, a.flags, a.recursionCount) {
				a.canAssign = false
			}
			continue
		}

		srcParam := a.srcParamDetails.Params[i]

		// The original's comment: if the source parameter has a default value, it is
		// OK for the corresponding dest parameter to be missing.
		if srcParam.DefaultType != nil {
			// The original's comment: assign default arg value in case it is needed
			// for populating TypeVar constraints. Enforce invariance below because the
			// default arg value is constructed prior to the call, so its type is
			// already fixed.
			if !a.e.AssignType(srcParam.Type, srcParam.DefaultType,
				createAddendumOrNil(a.diag), a.constraints, a.flags, a.recursionCount) {
				if (a.flags & AssignTypeFlagsPartialOverloadOverlap) == 0 {
					a.canAssign = false
				}
			}
			continue
		}

		// The original's comment: if the source parameter is also addressable by
		// keyword, it is OK that there is no matching positional parameter in the
		// dest.
		if srcParam.Kind == ParamKindStandard {
			continue
		}

		// The original's comment: if the source parameter is a variadic, it is OK
		// that there is no matching positional parameter in the dest.
		if srcParam.Param.Category == parser.ParamCategoryArgsList {
			continue
		}

		nonDefaultSrcParamCount := 0
		for _, p := range a.srcParamDetails.Params {
			if p.Param.Name != nil && p.DefaultType == nil &&
				p.Param.Category == parser.ParamCategorySimple {
				nonDefaultSrcParamCount++
			}
		}

		a.addAddendum(localization.LocAddendum.FunctionTooManyParams().Format(
			a.destPositionalCount, nonDefaultSrcParamCount))
		a.canAssign = false
		break
	}
}

// matchDestPositionalsAgainstSrcArgs is the original's block whose comment reads:
// make sure the remaining dest parameters can be assigned to the source *args
// parameter type.
func (a *functionAssigner) matchDestPositionalsAgainstSrcArgs() {
	srcArgsType := a.srcParamDetails.Params[*a.srcParamDetails.ArgsIndex].Type

	for paramIndex := a.srcPositionalCount; paramIndex < a.destPositionalCount; paramIndex++ {
		if paramIndex == *a.srcParamDetails.ArgsIndex {
			continue
		}

		destParamType := a.destParamDetails.Params[paramIndex].Type
		if IsTypeVarTuple(destParamType) && !IsTypeVarTuple(srcArgsType) {
			a.addMessage(localization.LocAddendum.TypeVarTupleRequiresKnownLength())
			a.canAssign = false
			continue
		}

		index := paramIndex
		if !a.e.assignParam(destParamType, srcArgsType, &index,
			createAddendumOrNil(a.diag), a.constraints, a.flags, a.recursionCount) {
			a.canAssign = false
		}

		destParamKind := a.destParamDetails.Params[paramIndex].Kind
		if destParamKind != ParamKindPositional && destParamKind != ParamKindExpandedArgs &&
			a.srcParamDetails.KwargsIndex == nil {
			a.addMessage(localization.LocAddendum.NamedParamMissingInSource().Format(
				derefStr(a.destParamDetails.Params[paramIndex].Param.Name)))
			a.canAssign = false
		}
	}
}

// reportTooFewParams is the original's functionTooFewParams check.
func (a *functionAssigner) reportTooFewParams() {
	// The original's comment: if the dest contains a *args, remove it from the
	// positional count because it's OK for zero source args to match it.
	adjDestPositionalCount := a.destPositionalCount
	if a.destParamDetails.ArgsIndex != nil && *a.destParamDetails.ArgsIndex < a.destPositionalCount {
		adjDestPositionalCount--
	}

	// The original's comment: if we're doing a partial overload overlap check,
	// ignore dest positional params with default values.
	if (a.flags & AssignTypeFlagsPartialOverloadOverlap) != 0 {
		for adjDestPositionalCount > 0 &&
			a.destParamDetails.Params[adjDestPositionalCount-1].DefaultType != nil {
			adjDestPositionalCount--
		}
	}

	if a.srcPositionalCount < adjDestPositionalCount {
		a.addMessage(localization.LocAddendum.FunctionTooFewParams().Format(
			adjDestPositionalCount, a.srcPositionalCount))
		a.canAssign = false
	}
}

// matchArgsParams handles the two `*args` checks.
func (a *functionAssigner) matchArgsParams(srcParamSpec *TypeVarType) {
	// The original's comment: if both src and dest have an "*args" parameter, make
	// sure their types are compatible.
	if a.srcParamDetails.ArgsIndex != nil && a.destParamDetails.ArgsIndex != nil &&
		!FunctionTypeIsGradualCallableForm(a.destType) {
		destArgsType := a.destParamDetails.Params[*a.destParamDetails.ArgsIndex].Type
		srcArgsType := a.srcParamDetails.Params[*a.srcParamDetails.ArgsIndex].Type

		if !IsUnpacked(destArgsType) {
			destArgsType = MakeTupleObject(a.e,
				[]*TupleTypeArg{{Type: destArgsType, IsUnbounded: true}}, true)
		}

		if !IsUnpacked(srcArgsType) {
			srcArgsType = MakeTupleObject(a.e,
				[]*TupleTypeArg{{Type: srcArgsType, IsUnbounded: true}}, true)
		}

		index := a.destParamDetails.Params[*a.destParamDetails.ArgsIndex].Index
		if !a.e.assignParam(destArgsType, srcArgsType, &index,
			createAddendumOrNil(a.diag), a.constraints, a.flags, a.recursionCount) {
			a.canAssign = false
		}
	}

	// The original's comment: if the dest has an "*args" but the source doesn't,
	// report the incompatibility. The converse situation is OK.
	if !FunctionTypeIsGradualCallableForm(a.destType) &&
		a.srcParamDetails.ArgsIndex == nil && srcParamSpec == nil &&
		a.destParamDetails.ArgsIndex != nil && !a.destParamDetails.HasUnpackedTypeVarTuple {
		a.addAddendum(localization.LocAddendum.ArgsParamMissing().Format(
			derefStr(a.destParamDetails.Params[*a.destParamDetails.ArgsIndex].Param.Name)))
		a.canAssign = false
	}
}

// matchKeywordParams is the original's "handle matching of named (keyword)
// parameters" block.
func (a *functionAssigner) matchKeywordParams(srcParamSpec *TypeVarType) {
	// The original's comment: build a dictionary of named parameters in the dest.
	destParamMap := common.NewOrderedMap[string, *VirtualParamDetails]()

	if a.destParamDetails.FirstKeywordOnlyIndex != nil {
		for index, param := range a.destParamDetails.Params {
			if index < *a.destParamDetails.FirstKeywordOnlyIndex {
				continue
			}
			if param.Param.Name != nil && param.Param.Category == parser.ParamCategorySimple &&
				param.Kind != ParamKindPositional && param.Kind != ParamKindExpandedArgs {
				destParamMap.Set(*param.Param.Name, param)
			}
		}
	}

	// The original's comment: if the dest has fewer positional arguments than the
	// source, the remaining positional arguments in the source can be treated as
	// named arguments.
	srcStartOfNamed := len(a.srcParamDetails.Params)
	if a.srcParamDetails.FirstKeywordOnlyIndex != nil {
		srcStartOfNamed = *a.srcParamDetails.FirstKeywordOnlyIndex
	}
	if a.destPositionalCount < a.srcPositionalCount && a.destParamDetails.ArgsIndex == nil {
		srcStartOfNamed = a.destPositionalCount
	}

	if srcStartOfNamed >= 0 {
		for index, srcParamInfo := range a.srcParamDetails.Params {
			if index < srcStartOfNamed {
				continue
			}
			if srcParamInfo.Param.Name == nil ||
				srcParamInfo.Param.Category != parser.ParamCategorySimple ||
				srcParamInfo.Kind == ParamKindPositional {
				continue
			}

			a.matchOneKeywordParam(srcParamInfo, destParamMap)
		}
	}

	a.checkUnmatchedKeywordParams(destParamMap)

	// The original's comment: if both src and dest have a "**kwargs" parameter,
	// make sure their types are compatible.
	if a.srcParamDetails.KwargsIndex != nil && a.destParamDetails.KwargsIndex != nil {
		index := a.destParamDetails.Params[*a.destParamDetails.KwargsIndex].Index
		if !a.e.assignParam(
			a.destParamDetails.Params[*a.destParamDetails.KwargsIndex].Type,
			a.srcParamDetails.Params[*a.srcParamDetails.KwargsIndex].Type,
			&index, createAddendumOrNil(a.diag), a.constraints, a.flags, a.recursionCount) {
			a.canAssign = false
		}
	}

	// The original's comment: if the dest has a "**kwargs" but the source doesn't,
	// report the incompatibility. The converse situation is OK.
	if !FunctionTypeIsGradualCallableForm(a.destType) &&
		a.srcParamDetails.KwargsIndex == nil && srcParamSpec == nil &&
		a.destParamDetails.KwargsIndex != nil {
		a.addAddendum(localization.LocAddendum.KwargsParamMissing().Format(
			derefStr(a.destParamDetails.Params[*a.destParamDetails.KwargsIndex].Param.Name)))
		a.canAssign = false
	}
}

// matchOneKeywordParam is the body of the original's source-keyword loop.
func (a *functionAssigner) matchOneKeywordParam(
	srcParamInfo *VirtualParamDetails, destParamMap *common.OrderedMap[string, *VirtualParamDetails],
) {
	srcParamName := *srcParamInfo.Param.Name
	destParamInfo, found := destParamMap.Get(srcParamName)
	paramDiag := createAddendumOrNil(a.diag)
	srcParamType := srcParamInfo.Type

	if !found {
		a.matchUnknownKeywordParam(srcParamInfo, srcParamType, paramDiag)
		return
	}

	// The original's comment: if we're performing a partial overload match and both
	// the source and dest parameters provide defaults, assume that there could be a
	// match.
	if srcParamInfo.DefaultType != nil && destParamInfo.DefaultType != nil &&
		(a.flags&AssignTypeFlagsPartialOverloadOverlap) != 0 {
		destParamMap.Delete(srcParamName)
		return
	}

	specializedDestParamType := destParamInfo.Type
	if a.constraints != nil {
		specializedDestParamType = a.e.SolveAndApplyConstraints(destParamInfo.Type, a.constraints, nil, nil)
	}

	if !a.e.assignParam(destParamInfo.Type, srcParamType, nil,
		createAddendumOrNil(paramDiag), a.constraints, a.flags, a.recursionCount) {
		if paramDiag != nil {
			paramDiag.AddMessage(localization.LocAddendum.NamedParamTypeMismatch().Format(
				srcParamName,
				a.e.PrintType(specializedDestParamType, nil),
				a.e.PrintType(srcParamType, nil)))
		}
		a.canAssign = false
	}

	if destParamInfo.DefaultType != nil && srcParamInfo.DefaultType == nil {
		a.addAddendum(localization.LocAddendum.FunctionParamDefaultMissing().Format(srcParamName))
		a.canAssign = false
	}

	destParamMap.Delete(srcParamName)
}

// matchUnknownKeywordParam is the `!destParamInfo` arm: the source names a
// keyword the destination does not.
func (a *functionAssigner) matchUnknownKeywordParam(
	srcParamInfo *VirtualParamDetails, srcParamType Type, paramDiag *common.DiagnosticAddendum,
) {
	if a.destParamDetails.KwargsIndex == nil && srcParamInfo.DefaultType == nil {
		if paramDiag != nil {
			paramDiag.AddMessage(localization.LocAddendum.NamedParamMissingInDest().Format(
				*srcParamInfo.Param.Name))
		}
		a.canAssign = false
		return
	}

	if a.destParamDetails.KwargsIndex != nil {
		// The original's comment: make sure we can assign the type to the Kwargs.
		index := a.destParamDetails.Params[*a.destParamDetails.KwargsIndex].Index
		if !a.e.assignParam(
			a.destParamDetails.Params[*a.destParamDetails.KwargsIndex].Type,
			srcParamType, &index, createAddendumOrNil(a.diag),
			a.constraints, a.flags, a.recursionCount) {
			a.canAssign = false
		}
		return
	}

	if srcParamInfo.DefaultType != nil {
		// The original's comment: assign default arg values in case they are needed
		// for populating TypeVar constraints.
		if !a.e.AssignType(srcParamInfo.Type, srcParamInfo.DefaultType,
			createAddendumOrNil(a.diag), a.constraints, a.flags, a.recursionCount) {
			if (a.flags & AssignTypeFlagsPartialOverloadOverlap) == 0 {
				a.canAssign = false
			}
		}
	}
}

// checkUnmatchedKeywordParams is the original's block whose comment reads: see if
// there are any unmatched named parameters.
func (a *functionAssigner) checkUnmatchedKeywordParams(
	destParamMap *common.OrderedMap[string, *VirtualParamDetails],
) {
	// The original mutates the map while iterating it. The keys are snapshotted
	// here so the walk is well-defined, which the original gets from JavaScript's
	// Map iteration semantics.
	for _, paramName := range destParamMap.Keys() {
		destParamInfo, found := destParamMap.Get(paramName)
		if !found {
			continue
		}

		if a.srcParamDetails.KwargsIndex != nil && destParamInfo.Param.Name != nil {
			// The original's comment: make sure the src kwargs type is compatible.
			index := destParamInfo.Index
			if !a.e.assignParam(destParamInfo.Type,
				a.srcParamDetails.Params[*a.srcParamDetails.KwargsIndex].Type,
				&index, createAddendumOrNil(a.diag), a.constraints, a.flags, a.recursionCount) {
				a.canAssign = false
			}
			destParamMap.Delete(paramName)
			continue
		}

		a.addAddendum(localization.LocAddendum.NamedParamMissingInSource().Format(paramName))
		a.canAssign = false
	}
}

// assignRemainingToParamSpec is the original's ParamSpec block: whatever
// parameters the source has left over after the explicitly-declared ones are
// synthesized into a function and assigned to the destination's ParamSpec as a
// unit. That is how Concatenate works.
func (a *functionAssigner) assignRemainingToParamSpec(
	isContra bool, srcParamSpec, destParamSpec *TypeVarType,
) bool {
	effectiveSrcType, effectiveDestType := a.srcType, a.destType
	effectiveSrcParamSpec, effectiveDestParamSpec := srcParamSpec, destParamSpec
	if isContra {
		effectiveSrcType, effectiveDestType = a.destType, a.srcType
		effectiveSrcParamSpec, effectiveDestParamSpec = destParamSpec, srcParamSpec
	}

	if effectiveDestParamSpec == nil {
		return true
	}

	requiredMatchParamCount := 0
	for i, p := range effectiveDestType.Shared.Parameters {
		if p.Name == nil {
			continue
		}
		paramType := FunctionTypeGetParamType(effectiveDestType, i)
		if p.Category == parser.ParamCategorySimple && IsParamSpec(paramType) {
			continue
		}
		requiredMatchParamCount++
	}

	matchedParamCount := 0
	remainingParams := []FunctionParam{}

	// The original's comment: if there are parameters in the source that are not
	// matched to parameters in the dest, assume these are concatenated on to the
	// ParamSpec.
	for index, p := range effectiveSrcType.Shared.Parameters {
		if matchedParamCount < requiredMatchParamCount {
			if p.Name != nil {
				matchedParamCount++
			}

			// The original's comment: if this is a *args parameter, assume that it
			// provides the remaining positional parameters, but also assume that it is
			// not exhausted and can provide additional parameters.
			if p.Category != parser.ParamCategoryArgsList {
				continue
			}
		}

		if IsPositionOnlySeparator(p) && len(remainingParams) == 0 {
			// The original's comment: don't bother pushing a position-only separator
			// if it is the first remaining param.
			continue
		}

		remainingParams = append(remainingParams, FunctionParamCreate(
			p.Category,
			FunctionTypeGetParamType(effectiveSrcType, index),
			p.Flags,
			p.Name,
			FunctionTypeGetParamDefaultType(effectiveSrcType, index),
			p.DefaultExpr))
	}

	// The original's comment: if there are remaining parameters and the source and
	// dest do not contain the same ParamSpec, synthesize a function for the
	// remaining parameters.
	if len(remainingParams) == 0 && effectiveSrcParamSpec != nil &&
		IsTypeSame(effectiveSrcParamSpec, effectiveDestParamSpec, TypeSameOptions{IgnoreTypeFlags: true}, 0) {
		return true
	}

	effectiveSrcPosCount, effectiveDestPosCount := a.srcPositionalCount, a.destPositionalCount
	if isContra {
		effectiveSrcPosCount, effectiveDestPosCount = a.destPositionalCount, a.srcPositionalCount
	}

	// The original's comment: if the src and dest both have ParamSpecs but the src
	// has additional positional parameters that have not been matched to dest
	// positional parameters (probably due to a Concatenate), don't attempt to assign
	// the remaining parameters to the ParamSpec.
	if effectiveSrcParamSpec != nil && effectiveSrcPosCount < effectiveDestPosCount {
		return true
	}

	remainingFunction := FunctionTypeCreateInstance("", "", "",
		effectiveSrcType.Shared.Flags|FunctionTypeFlagsSynthesizedMethod,
		effectiveSrcType.Shared.DocString)
	remainingFunction.Shared.DeprecatedMessage = effectiveSrcType.Shared.DeprecatedMessage
	remainingFunction.Shared.TypeVarScopeID = effectiveSrcType.Shared.TypeVarScopeID
	remainingFunction.Priv.ConstructorTypeVarScopeID = effectiveSrcType.Priv.ConstructorTypeVarScopeID
	remainingFunction.Shared.MethodClass = effectiveSrcType.Shared.MethodClass
	for _, param := range remainingParams {
		FunctionTypeAddParam(remainingFunction, param)
	}
	if effectiveSrcParamSpec != nil {
		FunctionTypeAddParamSpecVariadics(remainingFunction,
			ConvertToInstance(effectiveSrcParamSpec, true).(*TypeVarType))
	}

	if a.e.AssignType(effectiveDestParamSpec, remainingFunction, nil, a.constraints, a.flags, 0) {
		return true
	}

	// The original's comment: if we couldn't assign the function to the ParamSpec,
	// see if we can assign only the ParamSpec. This is possible if there were no
	// remaining parameters.
	if len(remainingParams) > 0 || effectiveSrcParamSpec == nil {
		return false
	}

	return a.e.AssignType(ConvertToInstance(effectiveDestParamSpec, true),
		ConvertToInstance(effectiveSrcParamSpec, true), nil, a.constraints, a.flags, 0)
}

// matchReturnType is the original's "match the return parameter" block. The
// return is COVARIANT, so this is the one assignment in the function that runs in
// the ordinary direction.
func (a *functionAssigner) matchReturnType(
	destType, srcType *FunctionType,
	diag *common.DiagnosticAddendum,
	constraints *ConstraintTracker,
	flags AssignTypeFlags,
	recursionCount int,
) bool {
	destReturnType := a.e.getEffectiveReturnType(destType)
	if IsAnyOrUnknown(destReturnType) {
		return true
	}

	srcReturnType := a.e.SolveAndApplyConstraints(a.e.getEffectiveReturnType(srcType), constraints, nil, nil)
	returnDiag := createAddendumOrNil(diag)

	effectiveFlags := flags

	// The original's comment: if the source has a declared return type that
	// includes a literal in its annotation, assume that we will want the constraint
	// solver to retain literals.
	if srcType.Shared.DeclaredReturnType != nil &&
		ContainsLiteralType(srcType.Shared.DeclaredReturnType, true) {
		effectiveFlags |= AssignTypeFlagsRetainLiteralsForTypeVar
	}

	if a.e.AssignType(destReturnType, srcReturnType, createAddendumOrNil(returnDiag),
		constraints, effectiveFlags, recursionCount) {
		return true
	}

	// The original's comment: handle the special case where the return type is a
	// TypeGuard[T] or TypeIs[T]. This should also act as a bool, since that's its
	// type at runtime.
	if srcClass, ok := srcReturnType.(*ClassType); ok && IsClassInstance(srcReturnType) &&
		ClassTypeIsBuiltInNamed(srcClass, "TypeGuard", "TypeIs") &&
		a.e.prefetched != nil && a.e.prefetched.BoolClass != nil &&
		IsInstantiableClass(a.e.prefetched.BoolClass) {
		if a.e.AssignType(destReturnType,
			ClassTypeCloneAsInstance(a.e.prefetched.BoolClass.(*ClassType), false),
			createAddendumOrNil(returnDiag), constraints, flags, recursionCount) {
			return true
		}
	}

	if returnDiag != nil {
		returnDiag.AddMessage(localization.LocAddendum.FunctionReturnTypeMismatch().Format(
			a.e.PrintType(srcReturnType, nil), a.e.PrintType(destReturnType, nil)))
	}
	return false
}

// addAddendum is the original's `diag?.createAddendum().addMessage(...)`.
func (a *functionAssigner) addAddendum(message string) {
	if a.diag != nil {
		a.diag.CreateAddendum().AddMessage(message)
	}
}

// addMessage is the original's `diag?.addMessage(...)`.
func (a *functionAssigner) addMessage(message string) {
	if a.diag != nil {
		a.diag.AddMessage(message)
	}
}

// assignParam corresponds to the function of the same name: one parameter of the
// destination against the corresponding one of the source, CONTRAVARIANTLY --
// note that the two are passed to assignType in the reverse of the usual order.
func (e *typeEvaluator) assignParam(
	destType Type,
	srcType Type,
	paramIndex *int,
	diag *common.DiagnosticAddendum,
	constraints *ConstraintTracker,
	flags AssignTypeFlags,
	recursionCount int,
) bool {
	if IsTypeVarTuple(destType) && !IsUnpacked(srcType) {
		return false
	}

	specializedSrcType := srcType
	specializedDestType := destType
	doSpecializationStep := false

	if (flags & AssignTypeFlagsOverloadOverlap) == 0 {
		isFirstPass := (flags & AssignTypeFlagsArgAssignmentFirstPass) != 0

		if (flags & AssignTypeFlagsContravariant) == 0 {
			if !isFirstPass {
				specializedDestType = e.SolveAndApplyConstraints(destType, constraints, nil,
					&SolveConstraintsOptions{UseLowerBoundOnly: true})
			}
			doSpecializationStep = RequiresSpecialization(specializedDestType, nil, 0)
		} else {
			if !isFirstPass {
				specializedSrcType = e.SolveAndApplyConstraints(srcType, constraints, nil,
					&SolveConstraintsOptions{UseLowerBoundOnly: true})
			}
			doSpecializationStep = RequiresSpecialization(specializedSrcType, nil, 0)
		}
	}

	// The original's comment: is an additional specialization step required?
	if doSpecializationStep {
		if e.AssignType(specializedSrcType, specializedDestType, nil, constraints,
			(flags^AssignTypeFlagsContravariant)|AssignTypeFlagsRetainLiteralsForTypeVar,
			recursionCount) {
			specializedDestType = e.SolveAndApplyConstraints(destType, constraints, nil, nil)
		}
	}

	if !e.AssignType(specializedSrcType, specializedDestType, createAddendumOrNil(diag),
		constraints, flags, recursionCount) {
		if diag != nil && paramIndex != nil {
			diag.AddMessage(localization.LocAddendum.ParamAssignment().Format(
				*paramIndex+1, e.PrintType(destType, nil), e.PrintType(srcType, nil)))
		}

		return false
	}

	return true
}

// adjustSourceParamDetailsForDestVariadic corresponds to the function of the
// same name.
//
// Its comment: determines whether we need to pack some of the source positionals
// into a tuple that matches a variadic *args parameter in the destination.
func (e *typeEvaluator) adjustSourceParamDetailsForDestVariadic(
	srcDetails *ParamListDetails, destDetails *ParamListDetails,
) {
	// The original's comment: if there is no *args parameter in the dest, we have
	// nothing to do.
	if destDetails.ArgsIndex == nil {
		return
	}

	// The original's comment: if the *args parameter isn't an unpacked TypeVarTuple
	// or tuple, we have nothing to do.
	if !IsUnpacked(destDetails.Params[*destDetails.ArgsIndex].Type) {
		return
	}

	// The original's comment: if the source doesn't have enough positional
	// parameters, we have nothing to do.
	if len(srcDetails.Params) < *destDetails.ArgsIndex {
		return
	}

	srcLastToPackIndex := len(srcDetails.Params)
	for i, p := range srcDetails.Params {
		if i >= *destDetails.ArgsIndex && p.Kind == ParamKindKeyword {
			srcLastToPackIndex = i
			break
		}
	}

	// The original's comment: if both the source and dest have an *args parameter
	// but the dest's is in a later position, then we can't assign the source's
	// *args to the dest. Don't make any adjustment in this case.
	if srcDetails.ArgsIndex != nil && *destDetails.ArgsIndex > *srcDetails.ArgsIndex {
		return
	}

	destFirstNonPositional := len(destDetails.Params)
	if destDetails.FirstKeywordOnlyIndex != nil {
		destFirstNonPositional = *destDetails.FirstKeywordOnlyIndex
	}
	suffixLength := destFirstNonPositional - *destDetails.ArgsIndex - 1

	sliceEnd := srcLastToPackIndex - suffixLength
	if sliceEnd < *destDetails.ArgsIndex {
		sliceEnd = *destDetails.ArgsIndex
	}
	if sliceEnd > len(srcDetails.Params) {
		sliceEnd = len(srcDetails.Params)
	}
	srcPositionalsToPack := srcDetails.Params[*destDetails.ArgsIndex:sliceEnd]

	srcTupleTypes := []*TupleTypeArg{}
	for _, entry := range srcPositionalsToPack {
		if entry.Param.Category == parser.ParamCategoryArgsList {
			if IsUnpackedTypeVarTuple(entry.Type) {
				srcTupleTypes = append(srcTupleTypes, &TupleTypeArg{Type: entry.Type})
			} else if entryClass, ok := entry.Type.(*ClassType); ok && IsUnpackedClass(entry.Type) &&
				entryClass.Priv.TupleTypeArgs != nil {
				srcTupleTypes = append(srcTupleTypes, entryClass.Priv.TupleTypeArgs...)
			} else {
				srcTupleTypes = append(srcTupleTypes, &TupleTypeArg{Type: entry.Type, IsUnbounded: true})
			}
			continue
		}
		srcTupleTypes = append(srcTupleTypes, &TupleTypeArg{
			Type:       entry.Type,
			IsOptional: entry.DefaultType != nil,
		})
	}

	if len(srcTupleTypes) == 1 && IsTypeVarTuple(srcTupleTypes[0].Type) {
		return
	}

	srcPositionalsType := MakeTupleObject(e, srcTupleTypes, true)

	// The original's comment: snip out the portion of the source positionals that
	// map to the variadic dest parameter and replace it with a single parameter
	// that is typed as a tuple containing the individual types of the replaced
	// parameters.
	combinedName := "_arg_combined"
	combined := &VirtualParamDetails{
		Param: FunctionParamCreate(parser.ParamCategoryArgsList, srcPositionalsType,
			FunctionParamFlagsNameSynthesized|FunctionParamFlagsTypeDeclared, &combinedName, nil, nil),
		Type:         srcPositionalsType,
		DeclaredType: srcPositionalsType,
		Index:        -1,
		Kind:         ParamKindPositional,
	}

	tailStart := *destDetails.ArgsIndex + len(srcPositionalsToPack)
	if tailStart > len(srcDetails.Params) {
		tailStart = len(srcDetails.Params)
	}

	newParams := make([]*VirtualParamDetails, 0, len(srcDetails.Params))
	newParams = append(newParams, srcDetails.Params[:*destDetails.ArgsIndex]...)
	newParams = append(newParams, combined)
	newParams = append(newParams, srcDetails.Params[tailStart:]...)
	srcDetails.Params = newParams

	srcDetails.ArgsIndex = findVirtualParamIndex(srcDetails.Params, func(p *VirtualParamDetails) bool {
		return p.Param.Category == parser.ParamCategoryArgsList
	})
	srcDetails.KwargsIndex = findVirtualParamIndex(srcDetails.Params, func(p *VirtualParamDetails) bool {
		return p.Param.Category == parser.ParamCategoryKwargsDict
	})
	srcDetails.FirstKeywordOnlyIndex = findVirtualParamIndex(srcDetails.Params, func(p *VirtualParamDetails) bool {
		return p.Kind == ParamKindKeyword
	})

	positionOnlyCount := -1
	for i, p := range srcDetails.Params {
		if p.Kind != ParamKindPositional || p.Param.Category != parser.ParamCategorySimple ||
			p.DefaultType != nil {
			positionOnlyCount = i
			break
		}
	}
	if positionOnlyCount < 0 {
		positionOnlyCount = 0
	}
	srcDetails.PositionOnlyParamCount = positionOnlyCount
}

// findVirtualParamIndex is the original's `findIndex(...) >= 0 ? index : undefined`.
func findVirtualParamIndex(params []*VirtualParamDetails, predicate func(*VirtualParamDetails) bool) *int {
	for i, p := range params {
		if predicate(p) {
			index := i
			return &index
		}
	}
	return nil
}
