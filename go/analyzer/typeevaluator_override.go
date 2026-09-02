/*
 * typeevaluator_override.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/typeEvaluator.ts (pyright 1.1.412):
 * validateOverrideMethod, isOverrideMethodApplicable and
 * validateOverrideMethodInternal.
 *
 * This is Liskov substitution for methods: can a caller holding the base class
 * call the override and get what the base promised? Parameters are therefore
 * compared *contravariantly* -- the override's parameter must accept the base's
 * -- while the return type is compared covariantly. Reading the assignType calls
 * with that in mind is what makes the argument order stop looking arbitrary.
 *
 * validateOverrideMethod is dispatch over the four combinations of overloaded
 * and non-overloaded, and each has a different rule:
 *
 *   - simple over simple: compare directly.
 *   - overloaded over simple: *some* overload (or the implementation) must
 *     match, because a caller can only reach one of them.
 *   - simple over overloaded: the override must satisfy *every* base overload,
 *     since a caller may use any of them.
 *   - overloaded over overloaded: every base overload must be matched, and the
 *     matches must be in non-decreasing order, because overload resolution picks
 *     the first match and reordering would change which one wins.
 *
 * isOverrideMethodApplicable exists for that third and fourth case. A base
 * overload whose `self` is annotated with a type the child does not satisfy can
 * never be selected through the child, so requiring the child to match it would
 * be wrong.
 *
 * validateOverrideMethodInternal accumulates into `canOverride` rather than
 * returning early, so a single diagnostic can list every incompatibility at
 * once. Note that the whole parameter-comparison body is skipped when either
 * side is a gradual callable form (`(...) -> X`), which is compatible with
 * anything by construction.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/localization"
	"github.com/microsoft/pyright/go/parser"
)

// ValidateOverrideMethod corresponds to validateOverrideMethod. The original's
// enforceParamNames defaults to true; nil selects that default.
func (e *typeEvaluator) ValidateOverrideMethod(
	baseMethod Type,
	overrideMethod Type,
	baseClass *ClassType,
	diag *common.DiagnosticAddendum,
	enforceParamNames *bool,
) bool {
	enforce := enforceParamNames == nil || *enforceParamNames

	// The original's comment: if we're overriding a non-method with a method,
	// report it as an error. This occurs when a non-property overrides a
	// property.
	if !IsFunctionOrOverloaded(baseMethod) {
		if diag != nil {
			diag.AddMessage(localization.LocAddendum.OverrideType().
				Format(e.PrintType(baseMethod, nil)))
		}
		return false
	}

	if IsFunction(baseMethod) {
		return e.validateOverrideOfSimpleBase(baseMethod.(*FunctionType), overrideMethod, diag, enforce)
	}

	baseOverloaded := baseMethod.(*OverloadedType)

	// The original's comment: for a non-overloaded method overriding an
	// overloaded method, the override must match all of the overloads.
	if IsFunction(overrideMethod) {
		for _, overload := range OverloadedTypeGetOverloads(baseOverloaded) {
			// The original's comment: if the override isn't applicable for this
			// base class, skip the check.
			if baseClass != nil && !e.isOverrideMethodApplicable(overload, baseClass) {
				continue
			}

			var subDiag *common.DiagnosticAddendum
			if diag != nil {
				subDiag = diag.CreateAddendum()
			}

			if !e.validateOverrideMethodInternal(overload, overrideMethod.(*FunctionType), subDiag, enforce) {
				return false
			}
		}
		return true
	}

	return e.validateOverloadedOverOverloaded(baseOverloaded,
		overrideMethod.(*OverloadedType), baseClass, diag, enforce)
}

// validateOverrideOfSimpleBase is the original's `isFunction(baseMethod)` arm.
func (e *typeEvaluator) validateOverrideOfSimpleBase(
	baseMethod *FunctionType, overrideMethod Type,
	diag *common.DiagnosticAddendum, enforceParamNames bool,
) bool {
	// The original's comment: handle the easy case -- a simple function
	// overriding another simple function.
	if IsFunction(overrideMethod) {
		return e.validateOverrideMethodInternal(
			baseMethod, overrideMethod.(*FunctionType), diag, enforceParamNames)
	}

	overloaded := overrideMethod.(*OverloadedType)
	overloadsAndImpl := append([]*FunctionType(nil), OverloadedTypeGetOverloads(overloaded)...)
	if impl := OverloadedTypeGetImplementation(overloaded); impl != nil && IsFunction(impl) {
		overloadsAndImpl = append(overloadsAndImpl, impl.(*FunctionType))
	}

	// The original's comment: for an overload overriding a base method, at least
	// one overload or the implementation must be compatible with the base method.
	for _, overrideOverload := range overloadsAndImpl {
		if e.validateOverrideMethodInternal(baseMethod, overrideOverload, nil, enforceParamNames) {
			return true
		}
	}

	if diag != nil {
		diag.AddMessage(localization.LocAddendum.OverrideNoOverloadMatches())
	}
	return false
}

// validateOverloadedOverOverloaded is the original's final arm. Its comment: for
// an overloaded method overriding an overloaded method, the overrides must all
// match and be in the correct order. It is OK if the base method has additional
// overloads that are not present in the override.
func (e *typeEvaluator) validateOverloadedOverOverloaded(
	baseMethod *OverloadedType, overrideMethod *OverloadedType,
	baseClass *ClassType, diag *common.DiagnosticAddendum, enforceParamNames bool,
) bool {
	previousMatchIndex := -1
	baseOverloads := OverloadedTypeGetOverloads(baseMethod)

	for _, overrideOverload := range OverloadedTypeGetOverloads(overrideMethod) {
		possibleMatchIndex := -1

		matchIndex := -1
		for index, baseOverload := range baseOverloads {
			// The original's comment: if the override isn't applicable for this
			// base class, skip the check.
			if baseClass != nil && !e.isOverrideMethodApplicable(baseOverload, baseClass) {
				continue
			}

			isCompatible := e.validateOverrideMethodInternal(
				baseOverload, overrideOverload, nil, enforceParamNames)

			// The original's comment: if the override is compatible but the match
			// is one that is below the previous matched index, keep looking for
			// additional matches. Record the fact that we found at least one match.
			if isCompatible && index <= previousMatchIndex && possibleMatchIndex < 0 {
				possibleMatchIndex = index
				continue
			}

			if isCompatible {
				matchIndex = index
				break
			}
		}

		if matchIndex < 0 && possibleMatchIndex >= 0 {
			matchIndex = possibleMatchIndex
		}

		if matchIndex < 0 {
			break
		}

		if matchIndex < previousMatchIndex {
			if diag != nil {
				diag.AddMessage(localization.LocAddendum.OverrideOverloadOrder())
			}
			return false
		}

		previousMatchIndex = matchIndex
	}

	if previousMatchIndex < len(baseOverloads)-1 {
		unmatchedOverloads := baseOverloads[previousMatchIndex+1:]

		// The original's comment: see if all of the remaining overrides are
		// nonapplicable.
		anyApplicable := false
		for _, overload := range unmatchedOverloads {
			if baseClass != nil && e.isOverrideMethodApplicable(overload, baseClass) {
				anyApplicable = true
				break
			}
		}

		if baseClass == nil || anyApplicable {
			// The original's comment: we didn't find matches for all of the base
			// overloads.
			if diag != nil {
				diag.AddMessage(localization.LocAddendum.OverrideOverloadNoMatch())
			}
			return false
		}
	}

	return true
}

// isOverrideMethodApplicable corresponds to the function of the same name. The
// original's comment: determines whether a child class override is applicable to
// a parent class method signature. This is important in cases where the parent
// class defines an overload where some of the overload signatures supply
// explicit type annotations for the "self" or "cls" parameter and some of these
// do not apply to the child class.
func (e *typeEvaluator) isOverrideMethodApplicable(baseMethod *FunctionType, childClass *ClassType) bool {
	if !FunctionTypeIsInstanceMethod(baseMethod) && !FunctionTypeIsClassMethod(baseMethod) &&
		!FunctionTypeIsConstructorMethod(baseMethod) {
		return true
	}

	baseParamDetails := GetParamListDetails(baseMethod, nil)
	if len(baseParamDetails.Params) == 0 {
		return true
	}

	baseParamType := baseParamDetails.Params[0].Param

	if baseParamType.Category != parser.ParamCategorySimple ||
		!FunctionParamIsTypeDeclared(baseParamType) {
		return true
	}

	// The original's comment: if this is a self or cls parameter, determine
	// whether the override class can be assigned to the base parameter type. If
	// not, then this override doesn't apply.
	var childSelfOrClsType Type = childClass
	if FunctionTypeIsInstanceMethod(baseMethod) {
		childSelfOrClsType = ClassTypeCloneAsInstance(childClass, true)
	}

	return e.AssignType(baseParamDetails.Params[0].Type, childSelfOrClsType,
		nil, nil, AssignTypeFlagsDefault, 0)
}

// addDiagMessage is the original's `diag?.addMessage(...)`.
func addDiagMessage(diag *common.DiagnosticAddendum, message string) {
	if diag != nil {
		diag.AddMessage(message)
	}
}

// createSubAddendum is the original's `diag?.createAddendum()`.
func createSubAddendum(diag *common.DiagnosticAddendum) *common.DiagnosticAddendum {
	if diag == nil {
		return nil
	}
	return diag.CreateAddendum()
}

// validateOverrideMethodInternal corresponds to the function of the same name.
// The original's comment: determines whether the override method is compatible
// with the overridden method. This is used both for parent/child overrides and
// implicit overrides for peer classes in a multi-inheritance case. If
// enforceParamNames is true, the parameter names of non-positional-only
// parameters are enforced.
func (e *typeEvaluator) validateOverrideMethodInternal(
	baseMethod *FunctionType,
	overrideMethod *FunctionType,
	diag *common.DiagnosticAddendum,
	enforceParamNames bool,
) bool {
	baseParamDetails := GetParamListDetails(baseMethod, nil)
	overrideParamDetails := GetParamListDetails(overrideMethod, nil)
	constraints := NewConstraintTracker()

	canOverride := true

	// A gradual callable form -- `(...) -> X` -- is compatible with any parameter
	// list by construction, so the whole parameter comparison is skipped.
	if !FunctionTypeIsGradualCallableForm(baseMethod) &&
		!FunctionTypeIsGradualCallableForm(overrideMethod) {
		if !e.checkOverrideMethodKind(baseMethod, overrideMethod, diag) {
			canOverride = false
		}
		if !e.checkOverrideParamCount(baseParamDetails, overrideParamDetails,
			baseMethod, overrideMethod, diag, constraints) {
			canOverride = false
		}
		if !e.checkOverridePositionalParams(baseParamDetails, overrideParamDetails,
			overrideMethod, diag, constraints, enforceParamNames) {
			canOverride = false
		}
		if !e.checkOverrideVariadicAndKeywordParams(baseParamDetails, overrideParamDetails,
			diag, constraints, enforceParamNames) {
			canOverride = false
		}
	}

	// The original's comment: verify that one or the other method doesn't contain
	// a ParamSpec.
	if baseParamDetails.ParamSpec != nil && overrideParamDetails.ParamSpec == nil {
		// The original's comment: if the override uses an `*args: Any,
		// **kwargs: Any` signature, we will allow this as an acceptable overload
		// for a `*args: P.args, **kwargs: P.kwargs`.
		overrideHasArgsKwargs := overrideParamDetails.ArgsIndex != nil &&
			IsAnyOrUnknown(overrideParamDetails.Params[*overrideParamDetails.ArgsIndex].Type) &&
			overrideParamDetails.KwargsIndex != nil &&
			IsAnyOrUnknown(overrideParamDetails.Params[*overrideParamDetails.KwargsIndex].Type)

		if !overrideHasArgsKwargs {
			addDiagMessage(diag, localization.LocAddendum.ParamSpecMissingInOverride())
			canOverride = false
		}
	}

	// The original's comment: now check the return type. This is the one
	// covariant comparison: the override's return must be assignable to the
	// base's, not the other way round.
	baseReturnType := e.getEffectiveReturnType(baseMethod)
	overrideReturnType := e.SolveAndApplyConstraints(
		e.getEffectiveReturnType(overrideMethod), constraints, nil, nil)

	if !e.AssignType(baseReturnType, overrideReturnType, createSubAddendum(diag),
		constraints, AssignTypeFlagsDefault, 0) {
		addDiagMessage(diag, localization.LocAddendum.OverrideReturnType().Format(
			e.PrintType(baseReturnType, nil), e.PrintType(overrideReturnType, nil)))
		canOverride = false
	}

	return canOverride
}

// checkOverrideMethodKind is the original's static/class/instance comparison.
func (e *typeEvaluator) checkOverrideMethodKind(
	baseMethod *FunctionType, overrideMethod *FunctionType, diag *common.DiagnosticAddendum,
) bool {
	// The original's comment: verify that we're not overriding a static, class or
	// instance method with an incompatible type.
	switch {
	case FunctionTypeIsStaticMethod(baseMethod):
		if !FunctionTypeIsStaticMethod(overrideMethod) {
			addDiagMessage(diag, localization.LocAddendum.OverrideNotStaticMethod())
			return false
		}
	case FunctionTypeIsClassMethod(baseMethod):
		if !FunctionTypeIsClassMethod(overrideMethod) {
			addDiagMessage(diag, localization.LocAddendum.OverrideNotClassMethod())
			return false
		}
	case FunctionTypeIsInstanceMethod(baseMethod):
		if !FunctionTypeIsInstanceMethod(overrideMethod) {
			addDiagMessage(diag, localization.LocAddendum.OverrideNotInstanceMethod())
			return false
		}
	}
	return true
}

// checkOverrideParamCount is the original's positional-count comparison. The
// original's comment: verify that the positional param count matches exactly or
// that the override adds only params that preserve the original signature.
func (e *typeEvaluator) checkOverrideParamCount(
	baseParamDetails *ParamListDetails,
	overrideParamDetails *ParamListDetails,
	baseMethod *FunctionType,
	overrideMethod *FunctionType,
	diag *common.DiagnosticAddendum,
	constraints *ConstraintTracker,
) bool {
	canOverride := true
	foundParamCountMismatch := false

	if overrideParamDetails.PositionParamCount < baseParamDetails.PositionParamCount {
		if overrideParamDetails.ArgsIndex == nil {
			foundParamCountMismatch = true
		} else {
			overrideArgsType := overrideParamDetails.Params[*overrideParamDetails.ArgsIndex].Type
			for i := overrideParamDetails.PositionParamCount; i < baseParamDetails.PositionParamCount; i++ {
				if !e.AssignType(overrideArgsType, baseParamDetails.Params[i].Type,
					createSubAddendum(diag), constraints, AssignTypeFlagsDefault, 0) {
					// The original formats overrideParamType() here and discards
					// the result without adding it to the diagnostic -- the
					// message is built and dropped. Only canOverride is affected.
					canOverride = false
				}
			}
		}
	} else if overrideParamDetails.PositionParamCount > baseParamDetails.PositionParamCount {
		isCallableAttrOverriddenByInstanceMethod := baseParamDetails.PositionParamCount == 0 &&
			overrideParamDetails.PositionParamCount == 1 &&
			FunctionTypeIsInstanceMethod(overrideMethod) &&
			!FunctionTypeIsStaticMethod(baseMethod) &&
			!FunctionTypeIsClassMethod(baseMethod)

		if !isCallableAttrOverriddenByInstanceMethod {
			// The original's comment: verify that all of the override parameters
			// that extend the signature are either *args, **kwargs or parameters
			// with default values.
			for i := baseParamDetails.PositionParamCount; i < overrideParamDetails.PositionParamCount; i++ {
				overrideParam := overrideParamDetails.Params[i].Param

				if overrideParam.Category == parser.ParamCategorySimple &&
					overrideParam.Name != nil && overrideParamDetails.Params[i].DefaultType == nil {
					foundParamCountMismatch = true
				}
			}
		}
	}

	if foundParamCountMismatch {
		addDiagMessage(diag, localization.LocAddendum.OverridePositionalParamCount().Format(
			len(baseParamDetails.Params), len(overrideParamDetails.Params)))
		canOverride = false
	}

	return canOverride
}

// checkOverridePositionalParams is the original's per-position loop over the
// shared positional parameters.
func (e *typeEvaluator) checkOverridePositionalParams(
	baseParamDetails *ParamListDetails,
	overrideParamDetails *ParamListDetails,
	overrideMethod *FunctionType,
	diag *common.DiagnosticAddendum,
	constraints *ConstraintTracker,
	enforceParamNames bool,
) bool {
	canOverride := true

	positionalParamCount := baseParamDetails.PositionParamCount
	if overrideParamDetails.PositionParamCount < positionalParamCount {
		positionalParamCount = overrideParamDetails.PositionParamCount
	}

	for i := 0; i < positionalParamCount; i++ {
		// The original's comment: if the first parameter is a "self" or "cls"
		// parameter, skip the test because these are allowed to violate the Liskov
		// substitution principle.
		//
		// The checker deliberately defeats this skip for the "callable variable
		// overridden by a method" case by marking both sides static before calling
		// this routine; keep the two in sync if this skip logic changes.
		if i == 0 {
			if FunctionTypeIsInstanceMethod(overrideMethod) ||
				FunctionTypeIsClassMethod(overrideMethod) ||
				FunctionTypeIsConstructorMethod(overrideMethod) {
				continue
			}
		}

		baseParam := baseParamDetails.Params[i].Param
		overrideParam := overrideParamDetails.Params[i].Param

		baseName := paramNameOrStar(baseParam)
		overrideName := paramNameOrStar(overrideParam)

		switch {
		case i >= baseParamDetails.PositionOnlyParamCount &&
			!IsPrivateOrProtectedName(paramNameOrEmpty(baseParam)) &&
			baseParamDetails.Params[i].Kind != ParamKindPositional &&
			baseParam.Category == parser.ParamCategorySimple &&
			enforceParamNames &&
			paramNameOrEmpty(baseParam) != paramNameOrEmpty(overrideParam):

			if overrideParam.Category != parser.ParamCategorySimple {
				break
			}
			if FunctionParamIsNameSynthesized(baseParam) {
				break
			}

			if overrideParamDetails.Params[i].Kind == ParamKindPositional {
				addDiagMessage(diag, localization.LocAddendum.OverrideParamNamePositionOnly().
					Format(i+1, baseName))
			} else {
				addDiagMessage(diag, localization.LocAddendum.OverrideParamName().
					Format(i+1, baseName, overrideName))
			}
			canOverride = false

		case i < overrideParamDetails.PositionOnlyParamCount &&
			i >= baseParamDetails.PositionOnlyParamCount:

			if !FunctionParamIsNameSynthesized(baseParam) &&
				baseParamDetails.Params[i].Kind != ParamKindPositional &&
				baseParamDetails.Params[i].Kind != ParamKindExpandedArgs {
				addDiagMessage(diag, localization.LocAddendum.OverrideParamNamePositionOnly().
					Format(i+1, baseName))
				canOverride = false
			}

		default:
			baseParamType := baseParamDetails.Params[i].Type
			overrideParamType := overrideParamDetails.Params[i].Type

			baseIsSynthesizedTypeVar := IsTypeVar(baseParamType) &&
				baseParamType.(*TypeVarType).Shared.IsSynthesized
			overrideIsSynthesizedTypeVar := IsTypeVar(overrideParamType) &&
				overrideParamType.(*TypeVarType).Shared.IsSynthesized

			if !baseIsSynthesizedTypeVar && !overrideIsSynthesizedTypeVar {
				// Contravariance: the override's parameter must accept the base's.
				if baseParam.Category != overrideParam.Category ||
					!e.AssignType(overrideParamType, baseParamType,
						createSubAddendum(diag), constraints, AssignTypeFlagsDefault, 0) {
					addDiagMessage(diag, localization.LocAddendum.OverrideParamType().Format(
						i+1, e.PrintType(baseParamType, nil), e.PrintType(overrideParamType, nil)))
					canOverride = false
				}
			}

			if baseParamDetails.Params[i].DefaultType != nil &&
				overrideParamDetails.Params[i].DefaultType == nil {
				addDiagMessage(diag, localization.LocAddendum.OverrideParamNoDefault().Format(i+1))
				canOverride = false
			}
		}
	}

	// The original's comment: check for positional (named) parameters in the base
	// method that do not exist in the override.
	if enforceParamNames && overrideParamDetails.KwargsIndex == nil {
		for i := positionalParamCount; i < baseParamDetails.PositionParamCount; i++ {
			baseParam := baseParamDetails.Params[i]

			if baseParam.Kind == ParamKindStandard &&
				baseParam.Param.Category == parser.ParamCategorySimple {
				addDiagMessage(diag, localization.LocAddendum.OverrideParamNamePositionOnly().
					Format(i+1, paramNameOrStar(baseParam.Param)))
				canOverride = false
			}
		}
	}

	return canOverride
}

// checkOverrideVariadicAndKeywordParams is the original's *args, keyword-only
// and **kwargs comparisons.
func (e *typeEvaluator) checkOverrideVariadicAndKeywordParams(
	baseParamDetails *ParamListDetails,
	overrideParamDetails *ParamListDetails,
	diag *common.DiagnosticAddendum,
	constraints *ConstraintTracker,
	enforceParamNames bool,
) bool {
	canOverride := true

	// The original's comment: check for a *args match.
	if baseParamDetails.ArgsIndex != nil {
		if overrideParamDetails.ArgsIndex == nil {
			addDiagMessage(diag, localization.LocAddendum.OverrideParamNameMissing().
				Format(paramNameOrQuestion(baseParamDetails.Params[*baseParamDetails.ArgsIndex].Param)))
			canOverride = false
		} else {
			overrideParamType := overrideParamDetails.Params[*overrideParamDetails.ArgsIndex].Type
			baseParamType := baseParamDetails.Params[*baseParamDetails.ArgsIndex].Type

			if !e.AssignType(overrideParamType, baseParamType,
				createSubAddendum(diag), constraints, AssignTypeFlagsDefault, 0) {
				addDiagMessage(diag, localization.LocAddendum.OverrideParamKeywordType().Format(
					paramNameOrQuestion(overrideParamDetails.Params[*overrideParamDetails.ArgsIndex].Param),
					e.PrintType(baseParamType, nil), e.PrintType(overrideParamType, nil)))
				canOverride = false
			}
		}
	}

	// The original's comment: now check any keyword-only parameters.
	baseKwOnlyParams := keywordOnlyParams(baseParamDetails)
	overrideKwOnlyParams := keywordOnlyParams(overrideParamDetails)

	for _, paramInfo := range baseKwOnlyParams {
		overrideParamInfo := findParamByName(overrideKwOnlyParams, paramInfo.Param.Name)

		if overrideParamInfo == nil && overrideParamDetails.KwargsIndex == nil {
			addDiagMessage(diag, localization.LocAddendum.OverrideParamNameMissing().
				Format(paramNameOrQuestion(paramInfo.Param)))
			canOverride = false
			continue
		}

		targetParamType := overrideParamDetails.Params[*overrideParamDetails.KwargsIndex].Type
		if overrideParamInfo != nil {
			targetParamType = overrideParamInfo.Type
		}

		if !e.AssignType(targetParamType, paramInfo.Type,
			createSubAddendum(diag), constraints, AssignTypeFlagsDefault, 0) {
			addDiagMessage(diag, localization.LocAddendum.OverrideParamKeywordType().Format(
				paramNameOrQuestion(paramInfo.Param),
				e.PrintType(paramInfo.Type, nil), e.PrintType(targetParamType, nil)))
			canOverride = false
		}

		if overrideParamInfo != nil &&
			paramInfo.DefaultType != nil && overrideParamInfo.DefaultType == nil {
			addDiagMessage(diag, localization.LocAddendum.OverrideParamKeywordNoDefault().
				Format(paramNameOrQuestion(overrideParamInfo.Param)))
			canOverride = false
		}
	}

	// The original's comment: verify that any keyword-only parameters added by
	// the overload are compatible with the **kwargs in the base.
	for _, paramInfo := range overrideKwOnlyParams {
		if findParamByName(baseKwOnlyParams, paramInfo.Param.Name) != nil {
			continue
		}

		if baseParamDetails.KwargsIndex == nil {
			if paramInfo.DefaultType == nil {
				addDiagMessage(diag, localization.LocAddendum.OverrideParamNameExtra().
					Format(paramNameOrQuestion(paramInfo.Param)))
				canOverride = false
			}
			continue
		}

		// The original's comment: base has a **kwargs; ensure the added
		// keyword-only parameter's type is compatible with the base's **kwargs
		// value type.
		baseKwargsType := baseParamDetails.Params[*baseParamDetails.KwargsIndex].Type
		if !e.AssignType(paramInfo.Type, baseKwargsType,
			createSubAddendum(diag), constraints, AssignTypeFlagsDefault, 0) {
			addDiagMessage(diag, localization.LocAddendum.OverrideParamKeywordType().Format(
				paramNameOrQuestion(paramInfo.Param),
				e.PrintType(baseKwargsType, nil), e.PrintType(paramInfo.Type, nil)))
			canOverride = false
		}
	}

	// The original's comment: verify that if the base method has a **kwargs
	// parameter, the override does too.
	if baseParamDetails.KwargsIndex != nil && overrideParamDetails.KwargsIndex == nil {
		addDiagMessage(diag, localization.LocAddendum.KwargsParamMissing().
			Format(paramNameOrEmpty(baseParamDetails.Params[*baseParamDetails.KwargsIndex].Param)))
		canOverride = false
	}

	return canOverride
}

// keywordOnlyParams is the original's repeated filter for keyword-only simple
// parameters.
func keywordOnlyParams(details *ParamListDetails) []*VirtualParamDetails {
	result := []*VirtualParamDetails{}
	for _, paramInfo := range details.Params {
		if paramInfo.Kind == ParamKindKeyword &&
			paramInfo.Param.Category == parser.ParamCategorySimple {
			result = append(result, paramInfo)
		}
	}
	return result
}

// findParamByName is the original's `find((pi) => name === pi.param.name)`.
func findParamByName(params []*VirtualParamDetails, name *string) *VirtualParamDetails {
	for _, paramInfo := range params {
		if paramInfo.Param.Name == nil || name == nil {
			if paramInfo.Param.Name == nil && name == nil {
				return paramInfo
			}
			continue
		}
		if *paramInfo.Param.Name == *name {
			return paramInfo
		}
	}
	return nil
}

// paramNameOrEmpty, paramNameOrStar and paramNameOrQuestion are the original's
// three different fallbacks for an unnamed parameter: `name || ”`, `name || '*'`
// and `name ?? '?'`.
func paramNameOrEmpty(param FunctionParam) string {
	if param.Name == nil {
		return ""
	}
	return *param.Name
}

func paramNameOrStar(param FunctionParam) string {
	if param.Name == nil || *param.Name == "" {
		return "*"
	}
	return *param.Name
}

func paramNameOrQuestion(param FunctionParam) string {
	if param.Name == nil {
		return "?"
	}
	return *param.Name
}
