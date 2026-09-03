/*
 * typeevaluator_overloads.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/typeEvaluator.ts (pyright 1.1.412):
 * validateCallForOverloaded, validateOverloadedArgTypes, validateOverloadsWithExpandedTypes,
 * getBestOverloadForArgs, expandArgTypes, expandArgType, evaluateCastCall,
 * filterOverloadMatchesForUnpackedArgs, filterOverloadMatchesForAnyArgs,
 * getOverloadArgParamPairs, getEffectiveOverloadReturnType, getEffectiveInitSelfType,
 * areAllMaterializationsAssignable, areAllMaterializationsEquivalent,
 * combineMaterializationCoverage.
 *
 * Picking an overload. The first overload whose parameters accept the arguments
 * wins, per PEP 484 -- but "accept" is not a simple question when an argument is
 * `Any`, and most of this file is about that.
 *
 * The selection runs in three stages. First, arity: each overload is matched
 * against the argument list with no type checking, and those with the wrong
 * shape are discarded. Then, types: the survivors are checked in declaration
 * order and the first clean match wins. If none matches, UNION EXPANSION splits
 * a union-typed argument into its members and retries, which is what makes
 * `f(x)` work where `x: int | str` and the overloads take `int` and `str`
 * separately. That expansion is combinatoric, so it is capped.
 *
 * The hard case is an `Any` argument, where several overloads may match and
 * disagree about the return type. Pyright's answer is not "pick the first" but
 * "decide whether the disagreement is observable": if every candidate returns
 * the same type, the ambiguity does not matter; if they differ, the result is
 * Unknown carrying the candidates as possible types, or `Any` when one of the
 * candidates was `Any` and the author probably meant it.
 *
 * areAllMaterializationsAssignable is where PEP 484's step 5 lives, and its
 * tri-state return is the point. assignType asks whether ONE gradual source
 * type is acceptable; this asks whether EVERY materialization of it is. True
 * means proven covered, false means a counterexample exists, and nil means
 * unproven -- and unproven has to stay distinct from false, or an overload
 * would be eliminated on a guess.
 */

package analyzer

import (
	"strings"

	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/localization"
	"github.com/microsoft/pyright/go/parser"
)

// MatchedOverloadInfo corresponds to the interface of the same name.
type MatchedOverloadInfo struct {
	Overload                *FunctionType
	MatchResults            *MatchArgsToParamsResult
	Constraints             *ConstraintTracker
	ReturnType              Type
	ArgResults              []*ArgResult
	SpecializedInitSelfType Type
}

// materializationCoverage corresponds to the type alias of the same name.
//
// The original's comment: assignType tests one gradual source type, whereas
// overload step 5 asks whether every materialization of that source is covered.
// These helpers therefore use a conservative tri-state proof over supported
// nominal and tuple relationships: true means all materializations are covered,
// false identifies a counterexample, and undefined means coverage is unproven.
// New cases must preserve this invariant.
type materializationCoverage = *bool

// validateCallForOverloaded corresponds to the function of the same name.
func (e *typeEvaluator) validateCallForOverloaded(
	errorNode parser.ExpressionNode,
	argList []*Arg,
	expandedCallType *OverloadedType,
	isCallTypeIncomplete bool,
	constraints *ConstraintTracker,
	skipUnknownArgCheck bool,
	inferenceContext *InferenceContext,
) *CallResult {
	overloads := OverloadedTypeGetOverloads(expandedCallType)

	// The original's comment: handle the 'cast' call as a special case.
	if len(overloads) > 0 && len(argList) == 2 &&
		FunctionTypeIsBuiltIn(overloads[0], "typing.cast", "typing_extensions.cast") {
		return &CallResult{ReturnType: e.evaluateCastCall(argList, errorNode)}
	}

	callResult := e.validateOverloadedArgTypes(errorNode, argList,
		&TypeResult{Type: expandedCallType, IsIncomplete: isCallTypeIncomplete},
		constraints, skipUnknownArgCheck, inferenceContext)

	returnType := callResult.ReturnType
	if returnType == nil {
		returnType = UnknownTypeCreate(false)
	}
	isTypeIncomplete := callResult.IsTypeIncomplete
	argumentErrors := callResult.ArgumentErrors

	if !argumentErrors {
		// The original's comment: call the function transform logic to handle
		// special-cased functions.
		//
		// The original passes the OverloadedType here; applyFunctionTransform only
		// acts on a FunctionType, so an overload set never transforms. Reproduced by
		// skipping the call rather than by narrowing the parameter type.
		_ = overloads
	}

	return &CallResult{
		ReturnType:              returnType,
		IsTypeIncomplete:        isTypeIncomplete,
		ArgumentErrors:          argumentErrors,
		OverloadsUsedForCall:    callResult.OverloadsUsedForCall,
		SpecializedInitSelfType: callResult.SpecializedInitSelfType,
	}
}

// validateOverloadedArgTypes corresponds to the function of the same name.
func (e *typeEvaluator) validateOverloadedArgTypes(
	errorNode parser.ExpressionNode,
	argList []*Arg,
	typeResult *TypeResult,
	constraints *ConstraintTracker,
	skipUnknownArgCheck bool,
	inferenceContext *InferenceContext,
) *CallResult {
	filteredMatchResults := []*MatchArgsToParamsResult{}
	var contextFreeArgTypes []Type
	isTypeIncomplete := typeResult.IsIncomplete
	overloadedType := typeResult.Type.(*OverloadedType)
	speculativeNode := getSpeculativeNodeForCall(errorNode)

	// The original's comment: start by evaluating the types of the arguments
	// without any expected type. Also, filter the list of overloads based on the
	// number of positional and keyword arguments that are present. We do all of
	// this speculatively because we don't want to record any types in the type
	// cache or record any diagnostics at this stage.
	e.UseSpeculativeMode(speculativeNode, func() {
		// The original's comment: consider only the functions that have the
		// @overload decorator, not the final function that omits the overload. This
		// is the intended behavior according to PEP 484.
		for overloadIndex, overload := range OverloadedTypeGetOverloads(overloadedType) {
			matchResults := e.matchArgsToParams(errorNode, argList,
				&TypeResult{Type: overload, IsIncomplete: typeResult.IsIncomplete}, overloadIndex)

			if !matchResults.ArgumentErrors {
				filteredMatchResults = append(filteredMatchResults, matchResults)
			}
		}
	}, nil)

	// The original's comment: if there are no possible arg/param matches among the
	// overloads, emit an error that includes the argument types.
	if len(filteredMatchResults) == 0 {
		e.reportNoOverloadArity(errorNode, argList, overloadedType)
		return &CallResult{ArgumentErrors: true, IsTypeIncomplete: isTypeIncomplete,
			OverloadsUsedForCall: []*FunctionType{}}
	}

	// The original's comment: if there is only one possible arg/param match among
	// the overloads, use the normal type matching mechanism because it is faster
	// and will provide a clearer error message.
	if len(filteredMatchResults) == 1 {
		return e.evaluateUsingBestMatchingOverload(errorNode, filteredMatchResults,
			constraints, inferenceContext, false, false)
	}

	expandedArgTypes := [][]Type{make([]Type, len(argList))}

	for {
		callResult := e.validateOverloadsWithExpandedTypes(errorNode, expandedArgTypes,
			filteredMatchResults, constraints, skipUnknownArgCheck, inferenceContext)

		if callResult.IsTypeIncomplete {
			isTypeIncomplete = true
		}

		if !callResult.ArgumentErrors {
			return callResult
		}

		// The original's comment: we didn't find an overload match. Try to expand
		// the next union argument type into individual types and retry with the
		// expanded types.
		if contextFreeArgTypes == nil {
			contextFreeArgTypes = e.evaluateContextFreeArgTypes(errorNode, argList)
		}

		expandedArgTypes = e.expandArgTypes(contextFreeArgTypes, expandedArgTypes)

		// The original's comment: check for combinatoric explosion and break out of
		// loop.
		if expandedArgTypes == nil || len(expandedArgTypes) > maxTotalOverloadArgTypeExpansionCount {
			break
		}
	}

	// The original's comment: we couldn't find any valid overloads. Skip the error
	// message if we're in speculative mode because it's very expensive, and we're
	// going to suppress the diagnostic anyway.
	if !e.canSkipDiagnosticForNode(errorNode) && !isTypeIncomplete {
		result := e.evaluateUsingBestMatchingOverload(errorNode, filteredMatchResults,
			constraints, inferenceContext, true, true)

		// The original's comment: replace the result with an unknown type since we
		// don't know what overload should have been used.
		copied := *result
		copied.ReturnType = UnknownTypeCreate(false)
		copied.ArgumentErrors = true
		return &copied
	}

	return &CallResult{ArgumentErrors: true, IsTypeIncomplete: isTypeIncomplete,
		OverloadsUsedForCall: []*FunctionType{}}
}

// reportNoOverloadArity is the original's zero-match diagnostic, which lists the
// argument types because "no overload matches" alone is not actionable.
func (e *typeEvaluator) reportNoOverloadArity(
	errorNode parser.ExpressionNode, argList []*Arg, overloadedType *OverloadedType,
) {
	// The original's comment: skip the error message if we're in speculative mode
	// because it's very expensive, and we're going to suppress the diagnostic
	// anyway.
	if e.canSkipDiagnosticForNode(errorNode) {
		return
	}

	overloads := OverloadedTypeGetOverloads(overloadedType)
	functionName := "<anonymous function>"
	if len(overloads) > 0 && overloads[0].Shared.Name != "" {
		functionName = overloads[0].Shared.Name
	}

	diagAddendum := common.NewDiagnosticAddendum()
	argTypes := make([]string, len(argList))
	for i, arg := range argList {
		typeString := e.PrintType(e.GetTypeOfArg(arg, nil).Type, nil)

		switch arg.ArgCategory {
		case parser.ArgCategoryUnpackedList:
			typeString = "*" + typeString
		case parser.ArgCategoryUnpackedDictionary:
			typeString = "**" + typeString
		}

		argTypes[i] = typeString
	}

	diagAddendum.AddMessage(localization.LocAddendum.ArgumentTypes().Format(strings.Join(argTypes, ", ")))
	e.AddDiagnostic(DiagnosticRuleReportCallIssue,
		localization.LocMessage.NoOverload().Format(functionName)+diagAddendum.GetString(),
		errorNode, nil)
}

// evaluateUsingBestMatchingOverload corresponds to the closure of the same name.
//
// Its comment: find the match with the smallest argument match score. If there
// are more than one with the same score, use the one with the largest index.
// Later overloads tend to be more general.
func (e *typeEvaluator) evaluateUsingBestMatchingOverload(
	errorNode parser.ExpressionNode,
	filteredMatchResults []*MatchArgsToParamsResult,
	constraints *ConstraintTracker,
	inferenceContext *InferenceContext,
	skipUnknownArgCheck bool,
	emitNoOverloadFoundError bool,
) *CallResult {
	bestMatch := filteredMatchResults[0]
	for _, current := range filteredMatchResults[1:] {
		if current.ArgumentMatchScore == bestMatch.ArgumentMatchScore {
			if current.OverloadIndex > bestMatch.OverloadIndex {
				bestMatch = current
			}
			continue
		}
		if current.ArgumentMatchScore < bestMatch.ArgumentMatchScore {
			bestMatch = current
		}
	}

	// The original's comment: if there is more than one filtered match, report
	// that no match was possible and emit a diagnostic that provides the most
	// likely.
	if emitNoOverloadFoundError {
		functionName := bestMatch.Overload.Shared.Name
		if functionName == "" {
			functionName = "<anonymous function>"
		}
		diagnostic := e.AddDiagnostic(DiagnosticRuleReportCallIssue,
			localization.LocMessage.NoOverload().Format(functionName), errorNode, nil)

		overrideDecl := bestMatch.Overload.Shared.Declaration
		if diagnostic != nil && overrideDecl != nil {
			diagnostic.AddRelatedInfo(
				localization.LocAddendum.OverloadIndex().Format(bestMatch.OverloadIndex+1),
				overrideDecl.DeclBase().Uri, overrideDecl.DeclBase().Range)
		}
	}

	effectiveConstraints := constraints
	if effectiveConstraints == nil {
		effectiveConstraints = NewConstraintTracker()
	}

	return e.validateArgTypesWithContext(errorNode, bestMatch, effectiveConstraints,
		skipUnknownArgCheck, inferenceContext)
}

// evaluateContextFreeArgTypes is the original's block whose comment reads:
// evaluate the types of each argument expression without regard to the context.
// We'll use this to determine whether we need to do union expansion.
func (e *typeEvaluator) evaluateContextFreeArgTypes(
	errorNode parser.ExpressionNode, argList []*Arg,
) []Type {
	result := make([]Type, len(argList))

	e.UseSpeculativeMode(getSpeculativeNodeForCall(errorNode), func() {
		for i, arg := range argList {
			if arg.TypeResult != nil {
				result[i] = arg.TypeResult.Type
				continue
			}

			if arg.ValueExpression != nil {
				valueExpressionNode := arg.ValueExpression
				var argType Type
				e.UseSpeculativeMode(valueExpressionNode, func() {
					argType = e.GetTypeOfExpression(valueExpressionNode, EvalFlagsNone, nil).Type
				}, nil)
				result[i] = argType
				continue
			}

			result[i] = AnyTypeCreate(false)
		}
	}, nil)

	return result
}

// expandArgTypes corresponds to the function of the same name.
//
// Its comment: replaces each item in the expandedArgTypes with n items where n
// is the number of subtypes in a union or other expandable type. The
// contextFreeArgTypes parameter represents the types of the arguments evaluated
// with no bidirectional type inference (i.e. without the help of the
// corresponding parameter's expected type). If the function returns undefined,
// that indicates that all types have been expanded, and no more expansion is
// possible.
func (e *typeEvaluator) expandArgTypes(
	contextFreeArgTypes []Type, expandedArgTypes [][]Type,
) [][]Type {
	// The original's comment: find the rightmost already-expanded argument.
	indexToExpand := len(contextFreeArgTypes) - 1
	for indexToExpand >= 0 && expandedArgTypes[0][indexToExpand] == nil {
		indexToExpand--
	}

	// The original's comment: move to the next candidate for expansion.
	indexToExpand++

	if indexToExpand >= len(contextFreeArgTypes) {
		return nil
	}

	var expandedTypes []Type
	for indexToExpand < len(contextFreeArgTypes) {
		// The original's comment: is this a union type? If so, we can expand it.
		expandedTypes = e.expandArgType(contextFreeArgTypes[indexToExpand])
		if expandedTypes != nil {
			break
		}
		indexToExpand++
	}

	// The original's comment: we have nothing left to expand.
	if expandedTypes == nil {
		return nil
	}

	// The original's comment: expand entry indexToExpand.
	newExpandedArgTypes := [][]Type{}

	for _, preExpandedTypes := range expandedArgTypes {
		for _, subtype := range expandedTypes {
			expanded := append([]Type{}, preExpandedTypes...)
			expanded[indexToExpand] = subtype
			newExpandedArgTypes = append(newExpandedArgTypes, expanded)
		}
	}

	return newExpandedArgTypes
}

// expandArgType corresponds to the function of the same name. It returns nil
// where the original returns undefined, meaning "not expandable".
func (e *typeEvaluator) expandArgType(t Type) []Type {
	expandedTypes := []Type{}

	// The original's comment: expand any top-level type variables with
	// constraints.
	t = e.MakeTopLevelTypeVarsConcrete(t, false)

	DoForEachSubtype(t, func(subtype Type, _ int, _ []Type) {
		if subtypeClass, ok := subtype.(*ClassType); ok && IsClassInstance(subtype) {
			// The original's comment: expand any bool or Enum literals.
			expandedLiteralTypes := EnumerateLiteralsForType(e, subtypeClass)
			if len(expandedLiteralTypes) > 0 &&
				len(expandedLiteralTypes) <= maxSingleOverloadArgTypeExpansionCount {
				for _, literal := range expandedLiteralTypes {
					expandedTypes = append(expandedTypes, literal)
				}
				return
			}

			// The original's comment: expand any fixed-size tuples.
			if expandedTuples := ExpandTuple(subtypeClass,
				maxSingleOverloadArgTypeExpansionCount); expandedTuples != nil {
				expandedTypes = append(expandedTypes, expandedTuples...)
				return
			}
		}

		expandedTypes = append(expandedTypes, subtype)
	})

	if len(expandedTypes) > 1 {
		return expandedTypes
	}
	return nil
}

// evaluateCastCall corresponds to the function of the same name. `cast` is not
// checked for soundness -- that is the point of it -- so the only diagnostic is
// the one for a cast that does nothing.
func (e *typeEvaluator) evaluateCastCall(argList []*Arg, errorNode parser.ExpressionNode) Type {
	if argList[0].ArgCategory != parser.ArgCategorySimple && argList[0].ValueExpression != nil {
		e.AddDiagnostic(DiagnosticRuleReportInvalidTypeForm,
			localization.LocMessage.UnpackInAnnotation(), argList[0].ValueExpression, nil)
	}

	// The original's comment: verify that the cast is necessary.
	castToType := e.getTypeOfArgExpectingType(argList[0],
		&ExpectedTypeOptions{TypeExpression: true}).Type

	liveScopeIds := GetTypeVarScopesForNode(errorNode)
	castToType = MakeTypeVarsBound(castToType, liveScopeIds, true)

	castFromType := e.GetTypeOfArg(argList[1], nil).Type

	if props := castFromType.Base().Props; props != nil && props.SpecialForm != nil {
		castFromType = props.SpecialForm
	}

	if castToType.Base().IsInstantiable() && !IsUnknown(castToType) {
		if IsTypeSame(ConvertToInstance(castToType, true), castFromType,
			TypeSameOptions{IgnorePseudoGeneric: true}, 0) {
			e.AddDiagnostic(DiagnosticRuleReportUnnecessaryCast,
				localization.LocMessage.UnnecessaryCast().Format(e.PrintType(castFromType, nil)),
				errorNode, nil)
		}
	}

	return ConvertToInstance(castToType, true)
}

// GetBestOverloadForArgs corresponds to getBestOverloadForArgs.
func (e *typeEvaluator) GetBestOverloadForArgs(
	errorNode parser.ExpressionNode, typeResult *TypeResult, argList []*Arg,
) *FunctionType {
	matches := []*MatchArgsToParamsResult{}
	speculativeNode := getSpeculativeNodeForCall(errorNode)

	callNode, _ := errorNode.(*parser.CallNode)
	collect := func() *TypeResult {
		// The original's comment: create a list of potential overload matches based
		// on arguments.
		for overloadIndex, overload := range OverloadedTypeGetOverloads(typeResult.Type.(*OverloadedType)) {
			e.UseSpeculativeMode(speculativeNode, func() {
				matchResults := e.matchArgsToParams(errorNode, argList,
					&TypeResult{Type: overload, IsIncomplete: typeResult.IsIncomplete}, overloadIndex)

				if !matchResults.ArgumentErrors {
					matches = append(matches, matchResults)
				}
			}, nil)
		}
		return nil
	}

	if callNode != nil {
		e.useSignatureTracker(callNode, collect)
	} else {
		collect()
	}

	for _, match := range matches {
		var won bool
		e.UseSpeculativeMode(speculativeNode, func() {
			callResult := e.validateArgTypes(errorNode, match, NewConstraintTracker(), true)
			if callResult != nil && !callResult.ArgumentErrors {
				won = true
			}
		}, nil)

		if won {
			return match.Overload
		}
	}

	return nil
}

// validateOverloadsWithExpandedTypes corresponds to the function of the same
// name: check the surviving overloads against one set of expanded argument
// lists, and decide what to do when several match.
func (e *typeEvaluator) validateOverloadsWithExpandedTypes(
	errorNode parser.ExpressionNode,
	expandedArgTypes [][]Type,
	argParamMatches []*MatchArgsToParamsResult,
	constraints *ConstraintTracker,
	skipUnknownArgCheck bool,
	inferenceContext *InferenceContext,
) *CallResult {
	returnTypes := []Type{}
	specializedInitSelfTypes := []Type{}
	matchedOverloads := []*MatchedOverloadInfo{}
	isTypeIncomplete := false
	overloadsUsedForCall := []*FunctionType{}
	isDefinitiveMatchFound := false
	hasInitSelfMaterializationAmbiguity := false
	hasEffectiveInitSelfType := false
	speculativeNode := getSpeculativeNodeForCall(errorNode)

	for _, argTypeOverride := range expandedArgTypes {
		overloadsUsedStartIndex := len(overloadsUsedForCall)
		var matchedOverload *FunctionType
		var effectiveInitSelfType Type
		possibleMatchResults := []*MatchedOverloadInfo{}
		possibleMatchInvolvesIncompleteUnknown := false
		isDefinitiveMatchFound = false

		hasArgTypeOverride := false
		for _, a := range argTypeOverride {
			if a != nil {
				hasArgTypeOverride = true
				break
			}
		}

		for _, baseMatch := range argParamMatches {
			overload := baseMatch.Overload

			matchResults := baseMatch
			if hasArgTypeOverride {
				matchResults = applyArgTypeOverride(baseMatch, argTypeOverride)
			}

			// The original's comment: clone the constraints so we don't modify the
			// original.
			effectiveConstraints := NewConstraintTracker()
			if constraints != nil {
				effectiveConstraints = constraints.Clone()
			}

			// The original's comment: use speculative mode so we don't output any
			// diagnostics or record any final types in the type cache.
			var callResult *CallResult
			e.UseSpeculativeMode(speculativeNode, func() {
				callResult = e.validateArgTypesWithContext(errorNode, matchResults,
					effectiveConstraints, true, inferenceContext)
			}, nil)

			if callResult.IsTypeIncomplete {
				isTypeIncomplete = true
			}

			if callResult.ArgumentErrors || callResult.ReturnType == nil {
				continue
			}

			overloadsUsedForCall = append(overloadsUsedForCall, overload)

			matchedOverload = overload
			matchedOverloadInfo := &MatchedOverloadInfo{
				Overload:                matchedOverload,
				MatchResults:            matchResults,
				Constraints:             effectiveConstraints,
				ReturnType:              callResult.ReturnType,
				ArgResults:              callResult.ArgResults,
				SpecializedInitSelfType: callResult.SpecializedInitSelfType,
			}
			matchedOverloads = append(matchedOverloads, matchedOverloadInfo)

			if callResult.AnyOrUnknownArg != nil || matchResults.UnpackedArgOfUnknownLength ||
				len(possibleMatchResults) > 0 {
				possibleMatchResults = append(possibleMatchResults, matchedOverloadInfo)

				if callResult.AnyOrUnknownArg != nil && IsIncompleteUnknown(callResult.AnyOrUnknownArg) {
					possibleMatchInvolvesIncompleteUnknown = true
				}
				continue
			}

			returnTypes = append(returnTypes, callResult.ReturnType)
			effectiveInitSelfType = e.getEffectiveInitSelfType(matchedOverloadInfo)
			isDefinitiveMatchFound = true
			break
		}

		// The original's comment: if we didn't find a definitive match that doesn't
		// depend on an Any or Unknown argument, fall back on the possible match. If
		// there were multiple possible matches, evaluate the type as Unknown, but
		// include the "possible types" to allow for completion suggestions.
		if !isDefinitiveMatchFound && len(possibleMatchResults) > 0 {
			possibleMatchResults = filterOverloadMatchesForUnpackedArgs(possibleMatchResults)
			possibleMatchResults = e.filterOverloadMatchesForAnyArgs(possibleMatchResults)

			// The original's comment: keep diagnostic bookkeeping aligned with the
			// candidates that remain possible. This applies to both top-level gradual
			// arguments and nested materialization, so deprecation diagnostics are
			// reported only for retained overloads.
			retained := make([]*FunctionType, len(possibleMatchResults))
			for i, result := range possibleMatchResults {
				retained[i] = result.Overload
			}
			overloadsUsedForCall = append(overloadsUsedForCall[:overloadsUsedStartIndex], retained...)

			// The original's comment: did the filtering produce a single result? If
			// so, we're done.
			if len(possibleMatchResults) == 1 {
				returnTypes = append(returnTypes, possibleMatchResults[0].ReturnType)
				effectiveInitSelfType = e.getEffectiveInitSelfType(possibleMatchResults[0])
				matchedOverloads = []*MatchedOverloadInfo{possibleMatchResults[0]}
			} else {
				ambiguous := e.resolveAmbiguousOverloadMatches(possibleMatchResults,
					possibleMatchInvolvesIncompleteUnknown)

				if ambiguous.IsInitSelfMaterializationAmbiguity {
					// The original's comment: overloaded __init__ methods normally
					// return None. Preserve that ordinary return as a placeholder while
					// union-expanded calls are combined, and carry the effective
					// constructed types separately. validateInitMethod consumes
					// specializedInitSelfType to reconstruct the constructor result after
					// call validation is complete.
					effectiveInitSelfType = ambiguous.ReturnType
					hasInitSelfMaterializationAmbiguity = true
					returnTypes = append(returnTypes, possibleMatchResults[0].ReturnType)
				} else {
					returnTypes = append(returnTypes, ambiguous.ReturnType)
				}
			}
		}

		if effectiveInitSelfType != nil {
			specializedInitSelfTypes = append(specializedInitSelfTypes, effectiveInitSelfType)
			hasEffectiveInitSelfType = true
		}

		if matchedOverload == nil {
			return &CallResult{ArgumentErrors: true, IsTypeIncomplete: isTypeIncomplete,
				OverloadsUsedForCall: overloadsUsedForCall}
		}
	}

	// The original's comment: union expansion requires one constructor handoff per
	// expanded argument list. Materialization ambiguity requires a combined handoff
	// even when there is only one argument list because multiple overload candidates
	// contribute to its result.
	shouldCombineInitSelfTypes := hasInitSelfMaterializationAmbiguity ||
		(len(expandedArgTypes) > 1 && hasEffectiveInitSelfType)

	// The original's comment: we found a match for all of the expanded argument
	// lists. Copy the resulting type var context back into the caller's type var
	// context. Use the type var context from the last matched overload because it
	// includes the type var solutions for all earlier matched overloads.
	if constraints != nil && isDefinitiveMatchFound {
		constraints.CopyFromClone(matchedOverloads[len(matchedOverloads)-1].Constraints)
	}

	// The original's comment: and run through the first expanded argument list one
	// more time to populate the type cache.
	finalConstraints := matchedOverloads[0].Constraints
	if !shouldCombineInitSelfTypes && constraints != nil {
		finalConstraints = constraints
	}
	finalCallResult := e.validateArgTypesWithContext(errorNode, matchedOverloads[0].MatchResults,
		finalConstraints, skipUnknownArgCheck, inferenceContext)

	if finalCallResult.IsTypeIncomplete {
		isTypeIncomplete = true
	}

	specializedInitSelfType := finalCallResult.SpecializedInitSelfType
	if len(specializedInitSelfTypes) > 0 && shouldCombineInitSelfTypes {
		specializedInitSelfType = CombineTypes(specializedInitSelfTypes, nil)
	}

	return &CallResult{
		ArgumentErrors:          finalCallResult.ArgumentErrors,
		AnyOrUnknownArg:         finalCallResult.AnyOrUnknownArg,
		ReturnType:              CombineTypes(returnTypes, nil),
		IsTypeIncomplete:        isTypeIncomplete,
		SpecializedInitSelfType: specializedInitSelfType,
		OverloadsUsedForCall:    overloadsUsedForCall,
	}
}

// applyArgTypeOverride is the original's spread-copy of a match result with
// precomputed argument types substituted in.
func applyArgTypeOverride(
	baseMatch *MatchArgsToParamsResult, argTypeOverride []Type,
) *MatchArgsToParamsResult {
	matchResults := *baseMatch
	argParams := make([]*ValidateArgTypeParams, len(baseMatch.ArgParams))

	for argIndex, argParam := range baseMatch.ArgParams {
		if argIndex >= len(argTypeOverride) || argTypeOverride[argIndex] == nil {
			argParams[argIndex] = argParam
			continue
		}
		argParamCopy := *argParam
		argParamCopy.ArgType = argTypeOverride[argIndex]
		argParams[argIndex] = &argParamCopy
	}

	matchResults.ArgParams = argParams
	return &matchResults
}

// ambiguousOverloadResult is what resolveAmbiguousOverloadMatches decides.
type ambiguousOverloadResult struct {
	ReturnType                         Type
	IsInitSelfMaterializationAmbiguity bool
}

// resolveAmbiguousOverloadMatches is the original's else-branch when filtering
// leaves more than one candidate: decide whether the disagreement between their
// return types is observable, and what to report if it is.
func (e *typeEvaluator) resolveAmbiguousOverloadMatches(
	possibleMatchResults []*MatchedOverloadInfo,
	possibleMatchInvolvesIncompleteUnknown bool,
) ambiguousOverloadResult {
	firstArgParamPairs := getOverloadArgParamPairs(possibleMatchResults[0])
	ambiguousMatchIncludesNestedAny := false
	ambiguousMatchIncludesNestedUnknown := false
	ambiguousMatchIncludesTopLevelAnyOrUnknown := false

	for index, pair := range firstArgParamPairs {
		paramTypes := make([]Type, len(possibleMatchResults))
		for i, match := range possibleMatchResults {
			argParamPairs := getOverloadArgParamPairs(match)
			if index < len(argParamPairs) {
				paramTypes[i] = argParamPairs[index].ParamType
			} else {
				paramTypes[i] = UnknownTypeCreate(false)
			}
		}

		if AreTypesSame(paramTypes, TypeSameOptions{TreatAnySameAsUnknown: true}) {
			continue
		}

		if IsAnyOrUnknown(pair.ArgType) {
			ambiguousMatchIncludesTopLevelAnyOrUnknown = true
			continue
		}

		anyOrUnknown := e.getAnyOrUnknownInInvariantPosition(pair.ArgType, 0)
		if anyOrUnknown != nil && IsAny(anyOrUnknown) {
			ambiguousMatchIncludesNestedAny = true
		} else if anyOrUnknown != nil && IsUnknown(anyOrUnknown) {
			ambiguousMatchIncludesNestedUnknown = true
		}
	}

	isInitSelfMaterializationAmbiguity := false
	if ambiguousMatchIncludesNestedAny || ambiguousMatchIncludesNestedUnknown {
		for _, result := range possibleMatchResults {
			if result.SpecializedInitSelfType != nil {
				isInitSelfMaterializationAmbiguity = true
				break
			}
		}
	}

	// The original's comment: eliminate any return types that are subsumed by
	// other return types.
	dedupedMatchResults, dedupedResultsIncludeAny := e.dedupeOverloadReturnTypes(
		possibleMatchResults, isInitSelfMaterializationAmbiguity)

	combinedTypes := CombineTypes(dedupedMatchResults, nil)

	returnType := combinedTypes
	switch {
	case ambiguousMatchIncludesNestedUnknown:
		returnType = UnknownTypeCreatePossibleType(combinedTypes, possibleMatchInvolvesIncompleteUnknown)

	case ambiguousMatchIncludesNestedAny && !ambiguousMatchIncludesTopLevelAnyOrUnknown:
		returnType = AnyTypeCreate(false)

	case len(dedupedMatchResults) > 1:
		// The original's comment: if one or more of the deduped types is Any or
		// contains Any, we will assume that the person who defined the overload
		// really wanted Any rather than Unknown. In cases where the deduped types
		// simply contains conflicting results without an Any, we'll use an
		// UnknownType.
		if dedupedResultsIncludeAny {
			returnType = AnyTypeCreate(false)
		} else {
			returnType = UnknownTypeCreatePossibleType(combinedTypes, possibleMatchInvolvesIncompleteUnknown)
		}
	}

	return ambiguousOverloadResult{
		ReturnType:                         returnType,
		IsInitSelfMaterializationAmbiguity: isInitSelfMaterializationAmbiguity,
	}
}

// dedupeOverloadReturnTypes is the original's subsumption pass over the
// candidates' return types.
func (e *typeEvaluator) dedupeOverloadReturnTypes(
	possibleMatchResults []*MatchedOverloadInfo, isInitSelfMaterializationAmbiguity bool,
) ([]Type, bool) {
	dedupedMatchResults := []Type{}
	dedupedResultsIncludeAny := false

	for _, result := range possibleMatchResults {
		resultType := result.ReturnType
		if isInitSelfMaterializationAmbiguity {
			resultType = e.getEffectiveOverloadReturnType(result)
		}
		isSubtypeSubsumed := false

		for dedupedIndex := 0; dedupedIndex < len(dedupedMatchResults); dedupedIndex++ {
			if e.AssignType(dedupedMatchResults[dedupedIndex], resultType, nil, nil,
				AssignTypeFlagsDefault, 0) {
				anyOrUnknown := ContainsAnyOrUnknown(dedupedMatchResults[dedupedIndex], false)
				if anyOrUnknown == nil {
					isSubtypeSubsumed = true
				} else if IsAny(anyOrUnknown) {
					dedupedResultsIncludeAny = true
				}
				break
			}

			if e.AssignType(resultType, dedupedMatchResults[dedupedIndex], nil, nil,
				AssignTypeFlagsDefault, 0) {
				anyOrUnknown := ContainsAnyOrUnknown(resultType, false)
				if anyOrUnknown == nil {
					dedupedMatchResults[dedupedIndex] = NeverTypeCreateNever()
				} else if IsAny(anyOrUnknown) {
					dedupedResultsIncludeAny = true
				}
				break
			}
		}

		if !isSubtypeSubsumed {
			dedupedMatchResults = append(dedupedMatchResults, resultType)
		}
	}

	filtered := dedupedMatchResults[:0]
	for _, t := range dedupedMatchResults {
		if !IsNever(t) {
			filtered = append(filtered, t)
		}
	}

	return filtered, dedupedResultsIncludeAny
}

// overloadArgParamPair is the original's `{ argType, paramType }` shape.
type overloadArgParamPair struct {
	ArgType   Type
	ParamType Type
}

// getOverloadArgParamPairs corresponds to the function of the same name.
//
// Its comment: argResults and argParams share validation order: supplied
// arguments in caller order, followed by synthesized defaults. Preserve that
// order so the same index across overloads represents the same supplied
// argument, even for keyword calls.
func getOverloadArgParamPairs(match *MatchedOverloadInfo) []overloadArgParamPair {
	pairs := []overloadArgParamPair{}

	for index, argResult := range match.ArgResults {
		var argParam *ValidateArgTypeParams
		if index < len(match.MatchResults.ArgParams) {
			argParam = match.MatchResults.ArgParams[index]
		}
		if argParam != nil && argParam.IsDefaultArg {
			continue
		}

		paramType := Type(UnknownTypeCreate(false))
		if argParam != nil {
			paramType = argParam.ParamType
		}
		pairs = append(pairs, overloadArgParamPair{ArgType: argResult.ArgType, ParamType: paramType})
	}

	if match.Overload.Priv.BoundToType != nil && match.Overload.Priv.StrippedFirstParamType != nil {
		pairs = append([]overloadArgParamPair{{
			ArgType:   match.Overload.Priv.BoundToType,
			ParamType: match.Overload.Priv.StrippedFirstParamType,
		}}, pairs...)
	}

	return pairs
}

// getEffectiveOverloadReturnType corresponds to the function of the same name.
func (e *typeEvaluator) getEffectiveOverloadReturnType(match *MatchedOverloadInfo) Type {
	if t := e.getEffectiveInitSelfType(match); t != nil {
		return t
	}
	return match.ReturnType
}

// getEffectiveInitSelfType corresponds to the function of the same name.
func (e *typeEvaluator) getEffectiveInitSelfType(match *MatchedOverloadInfo) Type {
	if match.SpecializedInitSelfType != nil {
		return match.SpecializedInitSelfType
	}

	boundToType := match.Overload.Priv.BoundToType
	if match.Overload.Shared.Name == "__init__" && boundToType != nil && IsClassInstance(boundToType) {
		return e.SolveAndApplyConstraints(boundToType, match.Constraints, &ApplyTypeVarOptions{
			ReplaceUnsolved: &ReplaceUnsolvedOptions{
				ScopeIDs:       GetTypeVarScopeIDs(boundToType),
				TupleClassType: e.GetTupleClassType(),
			},
		}, nil)
	}

	return nil
}

// filterOverloadMatchesForUnpackedArgs corresponds to the function of the same
// name.
//
// Its comment: determines whether one or more overloads can be eliminated
// because they rely on an unpacked argument of unknown length when there is at
// least one overload that doesn't because it maps to an *args parameter.
func filterOverloadMatchesForUnpackedArgs(matches []*MatchedOverloadInfo) []*MatchedOverloadInfo {
	if len(matches) < 2 {
		return matches
	}

	// The original's comment: is there at least one overload that relies on
	// unpacked args for a match?
	unpackedArgsOverloads := []*MatchedOverloadInfo{}
	for _, match := range matches {
		if match.MatchResults.UnpackedArgMapsToVariadic {
			unpackedArgsOverloads = append(unpackedArgsOverloads, match)
		}
	}

	if len(unpackedArgsOverloads) == len(matches) || len(unpackedArgsOverloads) == 0 {
		return matches
	}

	return unpackedArgsOverloads
}

// filterOverloadMatchesForAnyArgs corresponds to the function of the same name.
//
// Its comment: determines whether multiple incompatible overloads match due to
// an Any or Unknown argument type.
func (e *typeEvaluator) filterOverloadMatchesForAnyArgs(
	matches []*MatchedOverloadInfo,
) []*MatchedOverloadInfo {
	if len(matches) < 2 {
		return matches
	}

	firstArgParamPairs := getOverloadArgParamPairs(matches[0])
	hasInvariantAnyOrUnknownArg := false
	for _, pair := range firstArgParamPairs {
		if e.getAnyOrUnknownInInvariantPosition(pair.ArgType, 0) != nil {
			hasInvariantAnyOrUnknownArg = true
			break
		}
	}

	// The original's comment: if all of the effective return types match, select
	// the first one.
	effectiveReturns := make([]Type, len(matches))
	for i, match := range matches {
		if hasInvariantAnyOrUnknownArg {
			effectiveReturns[i] = e.getEffectiveOverloadReturnType(match)
		} else {
			effectiveReturns[i] = match.ReturnType
		}
	}
	if AreTypesSame(effectiveReturns, TypeSameOptions{TreatAnySameAsUnknown: true}) {
		return matches[:1]
	}

	// The original's comment: apply overload step 5 to arguments that contain a
	// gradual type in an invariant position. An overload that covers all
	// materializations eliminates only the overloads that follow it.
	if hasInvariantAnyOrUnknownArg {
		var filtered []*MatchedOverloadInfo
		filtered, firstArgParamPairs = e.applyMaterializationCoverage(matches, firstArgParamPairs)
		if filtered != nil {
			return filtered
		}
		matches = matches[:len(matches)]
	}

	foundAmbiguousAnyArg := false
	for i, pair := range firstArgParamPairs {
		// The original's comment: if the arg is Any or Unknown, see if the
		// corresponding parameter types differ in any way.
		if !IsAnyOrUnknown(pair.ArgType) {
			continue
		}

		paramTypes := make([]Type, len(matches))
		for m, match := range matches {
			argParamPairs := getOverloadArgParamPairs(match)
			if i < len(argParamPairs) {
				paramTypes[m] = argParamPairs[i].ParamType
			} else {
				paramTypes[m] = UnknownTypeCreate(false)
			}
		}
		if !AreTypesSame(paramTypes, TypeSameOptions{TreatAnySameAsUnknown: true}) {
			foundAmbiguousAnyArg = true
		}
	}

	// The original's comment: if the first overload has a different number of
	// effective arguments than latter overloads, don't filter any of them. This
	// typically means that one of the arguments is an unpacked iterator, and it maps
	// to an indeterminate number of parameters, which means that the overload
	// selection is ambiguous.
	if foundAmbiguousAnyArg {
		return matches
	}
	for _, match := range matches {
		if len(getOverloadArgParamPairs(match)) != len(firstArgParamPairs) {
			return matches
		}
	}

	return matches[:1]
}

// applyMaterializationCoverage is the original's PEP 484 step-5 block. A non-nil
// first return is a finished answer; otherwise the (possibly re-read) first
// argument/parameter pairs come back for the caller's remaining checks.
func (e *typeEvaluator) applyMaterializationCoverage(
	matches []*MatchedOverloadInfo, firstArgParamPairs []overloadArgParamPair,
) ([]*MatchedOverloadInfo, []overloadArgParamPair) {
	materializationCheckSupported := true

	for matchIndex := 0; matchIndex < len(matches); matchIndex++ {
		argParamPairs := getOverloadArgParamPairs(matches[matchIndex])

		var coverage materializationCoverage
		if len(argParamPairs) == len(firstArgParamPairs) {
			results := make([]materializationCoverage, len(argParamPairs))
			for index, pair := range argParamPairs {
				results[index] = e.areAllMaterializationsAssignable(
					pair.ParamType, firstArgParamPairs[index].ArgType, 0)
			}
			coverage = combineMaterializationCoverage(results)
		} else {
			coverage = coverageFalse()
		}

		if coverage == nil {
			materializationCheckSupported = false
			break
		}

		if *coverage {
			matches = matches[:matchIndex+1]
			break
		}
	}

	if !materializationCheckSupported {
		return nil, firstArgParamPairs
	}

	if len(matches) < 2 {
		return matches, firstArgParamPairs
	}

	effectiveReturns := make([]Type, len(matches))
	for i, match := range matches {
		effectiveReturns[i] = e.getEffectiveOverloadReturnType(match)
	}
	if AreTypesSame(effectiveReturns, TypeSameOptions{TreatAnySameAsUnknown: true}) {
		return matches[:1], firstArgParamPairs
	}

	firstArgParamPairs = getOverloadArgParamPairs(matches[0])
	for i, pair := range firstArgParamPairs {
		if e.getAnyOrUnknownInInvariantPosition(pair.ArgType, 0) == nil {
			continue
		}

		paramTypes := make([]Type, len(matches))
		for m, match := range matches {
			argParamPairs := getOverloadArgParamPairs(match)
			if i < len(argParamPairs) {
				paramTypes[m] = argParamPairs[i].ParamType
			} else {
				paramTypes[m] = UnknownTypeCreate(false)
			}
		}

		if !AreTypesSame(paramTypes, TypeSameOptions{TreatAnySameAsUnknown: true}) {
			return matches, firstArgParamPairs
		}
	}

	return nil, firstArgParamPairs
}

// coverageTrue, coverageFalse and coverageUnproven spell out the tri-state.
func coverageTrue() materializationCoverage  { v := true; return &v }
func coverageFalse() materializationCoverage { v := false; return &v }

// combineMaterializationCoverage corresponds to the function of the same name:
// false wins over unproven, and unproven wins over true.
func combineMaterializationCoverage(results []materializationCoverage) materializationCoverage {
	sawUnproven := false
	for _, result := range results {
		if result == nil {
			sawUnproven = true
			continue
		}
		if !*result {
			return coverageFalse()
		}
	}

	if sawUnproven {
		return nil
	}
	return coverageTrue()
}

// areAllMaterializationsAssignable corresponds to the function of the same name.
// It answers whether EVERY materialization of a gradual source type is
// assignable to the destination, where assignType answers only whether the
// gradual type itself is.
func (e *typeEvaluator) areAllMaterializationsAssignable(
	destType, srcType Type, recursionCount int,
) materializationCoverage {
	if recursionCount > MaxTypeRecursionCount {
		return nil
	}
	recursionCount++

	if ContainsAnyOrUnknown(srcType, true) == nil {
		if e.AssignType(destType, srcType, nil, nil, AssignTypeFlagsDefault, 0) {
			return coverageTrue()
		}
		return coverageFalse()
	}

	if IsAnyOrUnknown(destType) {
		return coverageTrue()
	}

	if IsTypeSame(destType, srcType, TypeSameOptions{TreatAnySameAsUnknown: true}, 0) {
		return coverageTrue()
	}

	if IsTypeVar(destType) || IsUnion(destType) || IsFunction(destType) || IsOverloaded(destType) {
		return nil
	}
	if destClass, ok := destType.(*ClassType); ok && IsClass(destType) &&
		ClassTypeIsProtocolClass(destClass) {
		return nil
	}

	if IsAnyOrUnknown(srcType) {
		if e.AssignType(destType, e.GetObjectType(), nil, nil, AssignTypeFlagsDefault, 0) {
			return coverageTrue()
		}
		return coverageFalse()
	}

	if IsUnion(srcType) || IsUnion(destType) || IsFunction(srcType) || IsFunction(destType) ||
		IsOverloaded(srcType) || IsOverloaded(destType) {
		return nil
	}

	destClass, destOk := destType.(*ClassType)
	srcClass, srcOk := srcType.(*ClassType)
	if !destOk || !srcOk || !IsClass(destType) || !IsClass(srcType) {
		return coverageFalse()
	}

	if IsTupleClass(destClass) && IsTupleClass(srcClass) {
		return e.tupleMaterializationsAssignable(destClass, srcClass, recursionCount)
	}

	return e.classMaterializationsAssignable(destClass, srcClass, recursionCount)
}

// tupleMaterializationsAssignable is the original's tuple arm.
func (e *typeEvaluator) tupleMaterializationsAssignable(
	destClass, srcClass *ClassType, recursionCount int,
) materializationCoverage {
	destTupleTypeArgs := destClass.Priv.TupleTypeArgs
	srcTupleTypeArgs := srcClass.Priv.TupleTypeArgs
	if destTupleTypeArgs == nil || srcTupleTypeArgs == nil {
		return nil
	}

	if len(destTupleTypeArgs) == 1 && destTupleTypeArgs[0].IsUnbounded {
		results := make([]materializationCoverage, len(srcTupleTypeArgs))
		for i, srcTypeArg := range srcTupleTypeArgs {
			results[i] = e.areAllMaterializationsAssignable(
				destTupleTypeArgs[0].Type, srcTypeArg.Type, recursionCount)
		}
		return combineMaterializationCoverage(results)
	}

	if len(destTupleTypeArgs) != len(srcTupleTypeArgs) {
		return coverageFalse()
	}
	for index, destTypeArg := range destTupleTypeArgs {
		if destTypeArg.IsUnbounded != srcTupleTypeArgs[index].IsUnbounded {
			return coverageFalse()
		}
	}

	results := make([]materializationCoverage, len(srcTupleTypeArgs))
	for index, srcTypeArg := range srcTupleTypeArgs {
		results[index] = e.areAllMaterializationsAssignable(
			destTupleTypeArgs[index].Type, srcTypeArg.Type, recursionCount)
	}
	return combineMaterializationCoverage(results)
}

// classMaterializationsAssignable is the original's nominal arm, which
// specializes the source for the destination's base class and then compares type
// arguments according to variance.
func (e *typeEvaluator) classMaterializationsAssignable(
	destClass, srcClass *ClassType, recursionCount int,
) materializationCoverage {
	var specializedSrcType *ClassType

	if ClassTypeIsSameGenericClass(destClass, srcClass, 0) {
		specializedSrcType = srcClass
	} else {
		instantiableDestType := destClass
		if IsClassInstance(destClass) {
			instantiableDestType = ClassTypeCloneAsInstantiable(destClass, true)
		}
		for _, mroClass := range srcClass.Shared.Mro {
			if mroClassType, ok := mroClass.(*ClassType); ok && IsClass(mroClass) &&
				ClassTypeIsSameGenericClass(instantiableDestType, mroClassType, 0) {
				specializedSrcType = SpecializeForBaseClass(srcClass, mroClassType)
				break
			}
		}
	}

	if specializedSrcType == nil {
		if ClassTypeIsBuiltInNamed(destClass, "object") {
			return coverageTrue()
		}
		return nil
	}

	typeParams := ClassTypeGetTypeParams(destClass)
	if len(typeParams) == 0 {
		return coverageTrue()
	}

	destTypeArgs := destClass.Priv.TypeArgs
	srcTypeArgs := specializedSrcType.Priv.TypeArgs
	if destTypeArgs == nil {
		return coverageTrue()
	}
	if srcTypeArgs == nil {
		return coverageFalse()
	}

	e.InferVarianceForClass(destClass)

	results := make([]materializationCoverage, len(srcTypeArgs))
	for index, srcTypeArg := range srcTypeArgs {
		destTypeArg := Type(UnknownTypeCreate(false))
		if index < len(destTypeArgs) {
			destTypeArg = destTypeArgs[index]
		}

		variance := VarianceInvariant
		if index < len(typeParams) {
			variance = TypeVarTypeGetVariance(typeParams[index])
		}

		switch variance {
		case VarianceCovariant:
			results[index] = e.areAllMaterializationsAssignable(destTypeArg, srcTypeArg, recursionCount)
		case VarianceInvariant:
			results[index] = e.areAllMaterializationsEquivalent(destTypeArg, srcTypeArg, recursionCount)
		default:
			results[index] = nil
		}
	}

	return combineMaterializationCoverage(results)
}

// areAllMaterializationsEquivalent corresponds to the function of the same name:
// the invariant counterpart, which requires sameness rather than assignability.
func (e *typeEvaluator) areAllMaterializationsEquivalent(
	destType, srcType Type, recursionCount int,
) materializationCoverage {
	if recursionCount > MaxTypeRecursionCount {
		return nil
	}
	recursionCount++

	if ContainsAnyOrUnknown(srcType, true) == nil {
		if IsTypeSame(destType, srcType, TypeSameOptions{TreatAnySameAsUnknown: true}, 0) {
			return coverageTrue()
		}
		return coverageFalse()
	}

	if IsAnyOrUnknown(destType) {
		return coverageTrue()
	}

	if IsTypeSame(destType, srcType, TypeSameOptions{TreatAnySameAsUnknown: true}, 0) {
		return coverageTrue()
	}

	if IsTypeVar(destType) || IsUnion(destType) || IsFunction(destType) || IsOverloaded(destType) {
		return nil
	}
	if destClass, ok := destType.(*ClassType); ok && IsClass(destType) &&
		ClassTypeIsProtocolClass(destClass) {
		return nil
	}

	if IsAnyOrUnknown(srcType) {
		return coverageFalse()
	}

	if IsUnion(srcType) || IsUnion(destType) || IsFunction(srcType) || IsFunction(destType) {
		return nil
	}

	destClass, destOk := destType.(*ClassType)
	srcClass, srcOk := srcType.(*ClassType)
	if !destOk || !srcOk || !IsClass(destType) || !IsClass(srcType) {
		return coverageFalse()
	}

	if !ClassTypeIsSameGenericClass(destClass, srcClass, 0) {
		return coverageFalse()
	}

	if destClass.Priv.TupleTypeArgs != nil || srcClass.Priv.TupleTypeArgs != nil {
		destTupleTypeArgs := destClass.Priv.TupleTypeArgs
		srcTupleTypeArgs := srcClass.Priv.TupleTypeArgs
		if destTupleTypeArgs == nil || srcTupleTypeArgs == nil ||
			len(destTupleTypeArgs) != len(srcTupleTypeArgs) {
			return coverageFalse()
		}
		for index, destTypeArg := range destTupleTypeArgs {
			if destTypeArg.IsUnbounded != srcTupleTypeArgs[index].IsUnbounded {
				return coverageFalse()
			}
		}

		results := make([]materializationCoverage, len(srcTupleTypeArgs))
		for index, srcTypeArg := range srcTupleTypeArgs {
			results[index] = e.areAllMaterializationsEquivalent(
				destTupleTypeArgs[index].Type, srcTypeArg.Type, recursionCount)
		}
		return combineMaterializationCoverage(results)
	}

	destTypeArgs := destClass.Priv.TypeArgs
	srcTypeArgs := srcClass.Priv.TypeArgs
	if destTypeArgs == nil || srcTypeArgs == nil || len(destTypeArgs) != len(srcTypeArgs) {
		return coverageFalse()
	}

	results := make([]materializationCoverage, len(srcTypeArgs))
	for index, srcTypeArg := range srcTypeArgs {
		results[index] = e.areAllMaterializationsEquivalent(
			destTypeArgs[index], srcTypeArg, recursionCount)
	}
	return combineMaterializationCoverage(results)
}

// ExpandTuple corresponds to the tuples.ts function of the same name: a
// fixed-size tuple whose elements are unions expands into the cross product of
// those unions, so overload selection can try each combination.
func ExpandTuple(tupleType *ClassType, maxExpansion int) []Type {
	if !IsTupleClass(tupleType) || tupleType.Priv.TupleTypeArgs == nil {
		return nil
	}
	for _, typeArg := range tupleType.Priv.TupleTypeArgs {
		if typeArg.IsUnbounded || IsTypeVarTuple(typeArg.Type) {
			return nil
		}
	}

	typesToCombine := []*ClassType{tupleType}

	for index := 0; index < len(tupleType.Priv.TupleTypeArgs); index++ {
		elemType := tupleType.Priv.TupleTypeArgs[index].Type
		if IsUnion(elemType) {
			newTypesToCombine := []*ClassType{}

			for _, typeToCombine := range typesToCombine {
				DoForEachSubtype(elemType, func(subtype Type, _ int, _ []Type) {
					newTypeArgs := append([]*TupleTypeArg{}, typeToCombine.Priv.TupleTypeArgs...)
					newTypeArgs[index] = &TupleTypeArg{Type: subtype}
					newTypesToCombine = append(newTypesToCombine,
						ClassTypeCloneAsInstance(
							SpecializeTupleClass(typeToCombine, newTypeArgs, true, false), true))
				})
			}
			typesToCombine = newTypesToCombine
		}

		if len(typesToCombine) > maxExpansion {
			return nil
		}
	}

	if len(typesToCombine) == 1 {
		return nil
	}

	result := make([]Type, len(typesToCombine))
	for i, t := range typesToCombine {
		result[i] = t
	}
	return result
}
