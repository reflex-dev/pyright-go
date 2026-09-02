/*
 * patternmatching_class.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/patternMatching.ts (pyright 1.1.412):
 * narrowTypeBasedOnClassPattern, getPositionalMatchArgNames,
 * isClassSpecialCaseForClassPattern, narrowTypeOfClassPatternArg,
 * specializeBoundedMatchTypeParams and validateClassPattern.
 *
 * A class pattern is an isinstance check plus optional sub-patterns on
 * attributes, and the positional form is driven entirely by `__match_args__`:
 * `case Point(x, y)` means "the attributes named by the first two entries of
 * Point.__match_args__". That is why getPositionalMatchArgNames reads the class
 * member rather than the parameter list of `__init__`.
 *
 * PEP 634 carves out a set of builtins -- bool, int, str, list, dict and the
 * rest -- for which a single positional pattern matches the *subject itself*
 * rather than an attribute, so `case str(s)` binds the string. That extends to
 * subclasses that do not define their own `__match_args__`, which is what
 * isClassSpecialCaseForClassPattern determines: an explicit `__match_args__`
 * opts back out of the special case.
 *
 * The negative direction can only eliminate a subtype when the match is
 * *certain*. A non-final class may have a subclass carrying extra attributes, so
 * argument-based elimination is restricted to exact matches, final classes, and
 * protocols -- the three cases where no subclass can change the answer.
 *
 * specializeBoundedMatchTypeParams handles a case that would otherwise surface
 * Unknown to the user: matching `Thing[T: bool]` against a subject with no type
 * arguments leaves T unsolved, and falling back to the bound is more useful than
 * Unknown. Its own comment warns that solvedTypeArgs must be indexed by the
 * *pattern* class's parameters, never the subject's.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/localization"
	"github.com/microsoft/pyright/go/parser"
)

// getPositionalMatchArgNames corresponds to the function of the same name. The
// original's comment: looks up the "__match_args__" class member to determine the
// names of the attributes used for class pattern matching.
func getPositionalMatchArgNames(evaluator TypeEvaluator, t *ClassType) []string {
	matchArgsMemberInfo := LookUpClassMember(t, "__match_args__", MemberAccessFlagsDefault, nil)
	if matchArgsMemberInfo == nil {
		return []string{}
	}

	matchArgsType := evaluator.GetTypeOfMember(matchArgsMemberInfo)
	if !IsClassInstance(matchArgsType) {
		return []string{}
	}
	matchArgsClass := matchArgsType.(*ClassType)
	if !IsTupleClass(matchArgsClass) || IsUnboundedTupleClass(matchArgsClass) ||
		matchArgsClass.Priv.TupleTypeArgs == nil {
		return []string{}
	}

	tupleArgs := matchArgsClass.Priv.TupleTypeArgs

	// The original's comment: are all the args string literals?
	names := make([]string, 0, len(tupleArgs))
	for _, arg := range tupleArgs {
		if !IsClassInstance(arg.Type) || !ClassTypeIsBuiltInNamed(arg.Type.(*ClassType), "str") ||
			!IsLiteralType(arg.Type.(*ClassType)) {
			return []string{}
		}
		s, ok := arg.Type.(*ClassType).Priv.LiteralValue.(LiteralString)
		if !ok {
			return []string{}
		}
		names = append(names, string(s))
	}

	return names
}

// isClassSpecialCaseForClassPattern corresponds to the function of the same name.
// The original's comment: some built-in classes are treated as special cases for
// the class pattern if a positional argument is used.
func isClassSpecialCaseForClassPattern(classType *ClassType) bool {
	for _, className := range classPatternSpecialCases {
		if classType.Shared.FullName == className {
			return true
		}
	}

	// The original's comment: if the class supplies its own `__match_args__`, it's
	// not a special case.
	if LookUpClassMember(classType, "__match_args__", MemberAccessFlagsDefault, nil) != nil {
		return false
	}

	// The original's comment: if the class derives from a built-in class, it is
	// considered a special case.
	for _, mroClass := range classType.Shared.Mro {
		if !IsClass(mroClass) {
			continue
		}
		for _, className := range classPatternSpecialCases {
			if mroClass.(*ClassType).Shared.FullName == className {
				return true
			}
		}
	}

	return false
}

// specializeBoundedMatchTypeParams corresponds to the function of the same name.
// The original's comment: when a class pattern matches a generic class whose type
// parameters have an upper bound (e.g. `class Thing[T: bool]`), the constraint
// solver may leave the parameters unsolved (Unknown) because the subject carries
// no type arguments. Rather than surfacing Unknown, fall back to each parameter's
// bound while preserving any argument that was concretely solved. The subject's
// condition is reapplied to the resulting instance.
func specializeBoundedMatchTypeParams(
	evaluator TypeEvaluator, matchType *ClassType, solvedTypeArgs []Type, condition []TypeCondition,
) Type {
	// The original's comment: `solvedTypeArgs` is indexed by `matchType`'s own type
	// parameters, so the caller must align it to the pattern class. The subject's
	// own type arguments must not be used here: the subject may be a different
	// generic class (e.g. a generic supertype), and indexing it by the pattern
	// class's parameters would misread unrelated arguments.
	typeArgs := make([]Type, 0, len(matchType.Shared.TypeParams))
	for index, param := range matchType.Shared.TypeParams {
		var specializedArg Type
		if index < len(solvedTypeArgs) {
			specializedArg = solvedTypeArgs[index]
		}

		// The original's comment: keep an argument unless it is the unsolved
		// sentinel (a bare top-level Unknown left by the constraint solver when the
		// subject carried no type arguments). A concrete argument that is merely
		// implicitly parameterized -- e.g. bare `list` (`list[Unknown]`) or `dict`
		// (`dict[Unknown, Unknown]`) -- is a real, solved type and must be preserved
		// rather than widened to the bound. A bare in-scope TypeVar is likewise a
		// legitimately narrowed argument.
		if !IsNilType(specializedArg) && !IsUnknown(specializedArg) {
			typeArgs = append(typeArgs, specializedArg)
			continue
		}

		if param.Shared.BoundType != nil {
			typeArgs = append(typeArgs, ConvertToInstance(param.Shared.BoundType, false))
			continue
		}

		if !IsNilType(specializedArg) {
			typeArgs = append(typeArgs, specializedArg)
			continue
		}
		typeArgs = append(typeArgs, GetUnknownForTypeVar(param, evaluator.GetTupleClassType()))
	}

	return AddConditionToType(
		ConvertToInstance(ClassTypeSpecialize(matchType, typeArgs, nil, false, nil, nil), false),
		condition, nil)
}

// narrowTypeBasedOnClassPattern corresponds to the function of the same name.
func narrowTypeBasedOnClassPattern(
	evaluator TypeEvaluator, t Type, pattern *parser.PatternClassNode, isPositiveTest bool,
) Type {
	exprType := evaluator.GetTypeOfExpression(classNameExpr(pattern.D.ClassName), EvalFlagsCallBaseDefaults, nil).Type

	// The original's comment: if this is a class (but not a type alias that refers
	// to a class), specialize it with Unknown type arguments.
	if IsClass(exprType) && propsTypeAliasInfo(exprType) == nil {
		exprType = ClassTypeCloneRemoveTypePromotions(exprType.(*ClassType))
		exprType = SpecializeWithUnknownTypeArgs(exprType.(*ClassType), evaluator.GetTupleClassType())
	}

	// The original's comment: are there any positional arguments? If so, try to get
	// the mappings for these arguments by fetching the __match_args__ symbol from
	// the class.
	positionalArgNames := []string{}
	if patternHasPositionalArg(pattern) && IsInstantiableClass(exprType) {
		positionalArgNames = getPositionalMatchArgNames(evaluator, exprType.(*ClassType))
	}

	if !isPositiveTest {
		return narrowTypeBasedOnClassPatternNegative(evaluator, t, pattern, exprType, positionalArgNames)
	}

	if !exprType.Base().IsInstantiable() && !IsNever(exprType) {
		evaluator.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
			localization.LocAddendum.TypeNotClass().Format(evaluator.PrintType(exprType, nil)),
			classNameExpr(pattern.D.ClassName), nil)
		return UnknownTypeCreate(false)
	} else if IsInstantiableClass(exprType) {
		exprClass := exprType.(*ClassType)
		if ClassTypeIsProtocolClass(exprClass) && !ClassTypeIsRuntimeCheckable(exprClass) {
			evaluator.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
				localization.LocAddendum.ProtocolRequiresRuntimeCheckable(), pattern.D.ClassName, nil)
			return UnknownTypeCreate(false)
		} else if ClassTypeIsTypedDictClass(exprClass) {
			evaluator.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
				localization.LocMessage.TypedDictInClassPattern(), pattern.D.ClassName, nil)
			return UnknownTypeCreate(false)
		}
	}

	return evaluator.MapSubtypesExpandTypeVars(exprType, nil,
		func(expandedSubtype Type, unexpandedSubtype Type) Type {
			if IsAnyOrUnknown(expandedSubtype) {
				return unexpandedSubtype
			}

			if !IsInstantiableClass(expandedSubtype) {
				return nil
			}

			expandedClass := expandedSubtype.(*ClassType)
			expandedSubtypeInstance := ConvertToInstance(expandedSubtype, false)
			isPatternMetaclass := IsMetaclassInstance(expandedSubtypeInstance)

			return evaluator.MapSubtypesExpandTypeVars(t, nil,
				func(subjectSubtypeExpanded Type, _ Type) Type {
					return narrowClassPatternSubject(evaluator, pattern, expandedClass,
						unexpandedSubtype, subjectSubtypeExpanded, isPatternMetaclass)
				})
		})
}

// narrowTypeBasedOnClassPatternNegative is the original's `if (!isPositiveTest)`
// block.
func narrowTypeBasedOnClassPatternNegative(
	evaluator TypeEvaluator,
	t Type,
	pattern *parser.PatternClassNode,
	exprType Type,
	positionalArgNames []string,
) Type {
	// The original's comment: don't attempt to narrow if the class type is a more
	// complex type (e.g. a TypeVar or union).
	if !IsInstantiableClass(exprType) {
		return t
	}

	classType := exprType.(*ClassType)

	if len(classType.Shared.TypeParams) > 0 {
		classType = ClassTypeSpecialize(classType, nil, nil, false, nil, nil)
	}

	classInstance := ClassTypeCloneAsInstance(classType, false)
	isPatternMetaclass := IsMetaclassInstance(classInstance)

	return evaluator.MapSubtypesExpandTypeVars(t,
		&EvaluatorMapSubtypesOptions{
			ExpandCallback: func(t Type) Type {
				return evaluator.ExpandPromotionTypes(pattern, t)
			},
		},
		func(subjectSubtypeExpanded Type, subjectSubtypeUnexpanded Type) Type {
			// The original's comment: handle the case where the class pattern
			// references type() or a subtype thereof and the subject type is an
			// instantiable class itself.
			if isPatternMetaclass && IsInstantiableClass(subjectSubtypeExpanded) {
				var metaclass Type = UnknownTypeCreate(false)
				if m := subjectSubtypeExpanded.(*ClassType).Shared.EffectiveMetaclass; m != nil {
					metaclass = m
				}
				if evaluator.AssignType(classType, metaclass, nil, nil, AssignTypeFlagsDefault, 0) {
					return nil
				}

				return subjectSubtypeExpanded
			}

			// The original's comment: handle Callable specially.
			if !IsAnyOrUnknown(subjectSubtypeExpanded) && ClassTypeIsBuiltInNamed(classType, "Callable") {
				if evaluator.AssignType(GetUnknownTypeForCallable(), subjectSubtypeExpanded,
					nil, nil, AssignTypeFlagsDefault, 0) {
					return nil
				}
			}

			if !IsNoneInstance(subjectSubtypeExpanded) && !IsClassInstance(subjectSubtypeExpanded) {
				return subjectSubtypeUnexpanded
			}

			// The original's comment: handle NoneType specially.
			if IsNoneInstance(subjectSubtypeExpanded) && ClassTypeIsBuiltInNamed(classType, "NoneType") {
				return nil
			}

			if !evaluator.AssignType(classInstance, subjectSubtypeExpanded, nil, nil,
				AssignTypeFlagsDefault, 0) {
				return subjectSubtypeExpanded
			}

			// The original's comment: handle literal types specially.
			if IsClassInstance(subjectSubtypeExpanded) &&
				IsLiteralType(subjectSubtypeExpanded.(*ClassType)) {
				return nil
			}

			if len(pattern.D.Args) == 0 {
				if IsClass(classInstance) && IsClass(subjectSubtypeExpanded) {
					// The original's comment: we know that this match will always
					// succeed, so we can eliminate this subtype.
					return nil
				}

				return subjectSubtypeExpanded
			}

			// The original's comment: we might be able to narrow further based on
			// arguments, but only if the types match exactly, the subject subtype is
			// a final class (and therefore cannot be subclassed), or the pattern
			// class is a protocol class.
			if !evaluator.AssignType(subjectSubtypeExpanded, classInstance, nil, nil,
				AssignTypeFlagsDefault, 0) {
				if IsClass(subjectSubtypeExpanded) &&
					!ClassTypeIsFinal(subjectSubtypeExpanded.(*ClassType)) &&
					!ClassTypeIsProtocolClass(classInstance) {
					return subjectSubtypeExpanded
				}
			}

			for index := range pattern.D.Args {
				narrowedArgType := narrowTypeOfClassPatternArg(evaluator, pattern.D.Args[index], index,
					positionalArgNames, subjectSubtypeExpanded, false)

				if !IsNever(narrowedArgType) {
					return subjectSubtypeUnexpanded
				}
			}

			// The original's comment: we've completely eliminated the type based on
			// the arguments.
			return nil
		})
}

// narrowClassPatternSubject is the innermost callback of the positive branch of
// narrowTypeBasedOnClassPattern.
func narrowClassPatternSubject(
	evaluator TypeEvaluator,
	pattern *parser.PatternClassNode,
	expandedSubtype *ClassType,
	unexpandedSubtype Type,
	subjectSubtypeExpanded Type,
	isPatternMetaclass bool,
) Type {
	if IsAnyOrUnknown(subjectSubtypeExpanded) {
		if ClassTypeIsBuiltInNamed(expandedSubtype, "Callable") {
			// The original's comment: convert to an unknown callable type.
			unknownCallable := FunctionTypeCreateSynthesizedInstance("", FunctionTypeFlagsGradualCallableForm)
			FunctionTypeAddDefaultParams(unknownCallable, IsUnknown(subjectSubtypeExpanded))
			unknownCallable.Shared.DeclaredReturnType = subjectSubtypeExpanded
			return unknownCallable
		}

		return ConvertToInstance(unexpandedSubtype, false)
	}

	// The original's comment: handle the case where the class pattern references
	// type() or a subtype thereof and the subject type is a class itself.
	if isPatternMetaclass && IsInstantiableClass(subjectSubtypeExpanded) {
		var metaclass Type = UnknownTypeCreate(false)
		if m := subjectSubtypeExpanded.(*ClassType).Shared.EffectiveMetaclass; m != nil {
			metaclass = m
		}
		if evaluator.AssignType(expandedSubtype, metaclass, nil, nil, AssignTypeFlagsDefault, 0) ||
			evaluator.AssignType(metaclass, expandedSubtype, nil, nil, AssignTypeFlagsDefault, 0) {
			return subjectSubtypeExpanded
		}

		return nil
	}

	// The original's comment: handle NoneType specially.
	if IsNoneInstance(subjectSubtypeExpanded) && ClassTypeIsBuiltInNamed(expandedSubtype, "NoneType") {
		return subjectSubtypeExpanded
	}

	// The original's comment: handle Callable specially.
	if ClassTypeIsBuiltInNamed(expandedSubtype, "Callable") {
		callableType := GetUnknownTypeForCallable()

		if evaluator.AssignType(callableType, subjectSubtypeExpanded, nil, nil, AssignTypeFlagsDefault, 0) {
			return subjectSubtypeExpanded
		}

		subjObjType := ConvertToInstance(subjectSubtypeExpanded, false)
		if evaluator.AssignType(subjObjType, callableType, nil, nil, AssignTypeFlagsDefault, 0) {
			return callableType
		}

		return nil
	}

	if !IsClassInstance(subjectSubtypeExpanded) {
		return nil
	}

	resultType, ok := classPatternResultType(evaluator, expandedSubtype, unexpandedSubtype,
		subjectSubtypeExpanded)
	if !ok {
		return nil
	}

	// The original's comment: are there any positional arguments? If so, try to get
	// the mappings for these arguments by fetching the __match_args__ symbol from
	// the class.
	positionalArgNames := []string{}
	if patternHasPositionalArg(pattern) {
		positionalArgNames = getPositionalMatchArgNames(evaluator, expandedSubtype)
	}

	isMatchValid := true
	for index, arg := range pattern.D.Args {
		// The original's comment: narrow the arg pattern. It's possible that the
		// actual type of the object being matched is a subtype of the resultType,
		// so it might contain additional attributes that we don't know about.
		narrowedArgType := narrowTypeOfClassPatternArg(evaluator, arg, index,
			positionalArgNames, resultType, true)

		if IsNever(narrowedArgType) {
			isMatchValid = false
		}
	}

	if isMatchValid {
		return resultType
	}

	return nil
}

// classPatternResultType is the original's block that decides which of the
// pattern class and the subject class the narrowed result should be. The second
// result is false where the original returns undefined.
func classPatternResultType(
	evaluator TypeEvaluator,
	expandedSubtype *ClassType,
	unexpandedSubtype Type,
	subjectSubtypeExpanded Type,
) (Type, bool) {
	var resultType Type

	if evaluator.AssignType(ClassTypeCloneAsInstance(expandedSubtype, false), subjectSubtypeExpanded,
		nil, nil, AssignTypeFlagsDefault, 0) {
		// The subject is already at least as narrow as the pattern class.
		resultType = subjectSubtypeExpanded
		return classPatternApplyBoundedParams(evaluator, resultType, unexpandedSubtype,
			subjectSubtypeExpanded), true
	}

	if !evaluator.AssignType(subjectSubtypeExpanded, ClassTypeCloneAsInstance(expandedSubtype, false),
		nil, nil, AssignTypeFlagsDefault, 0) {
		return nil, false
	}

	resultType = AddConditionToType(ConvertToInstance(unexpandedSubtype, false),
		GetTypeCondition(subjectSubtypeExpanded), nil)

	// The original's comment: try to retain the type arguments for the pattern
	// class type.
	if IsInstantiableClass(unexpandedSubtype) && IsClassInstance(subjectSubtypeExpanded) {
		unexpandedClass := unexpandedSubtype.(*ClassType)
		if ClassTypeIsSpecialBuiltIn(unexpandedClass) || len(unexpandedClass.Shared.TypeParams) > 0 {
			constraints := NewConstraintTracker()
			unspecializedMatchType := ClassTypeSpecialize(unexpandedClass, nil, nil, false, nil, nil)

			matchTypeInstance := ClassTypeCloneAsInstance(unspecializedMatchType, false)
			if AddConstraintsForExpectedType(evaluator, matchTypeInstance, subjectSubtypeExpanded,
				constraints, nil, 0) {
				if solved, ok := evaluator.SolveAndApplyConstraints(matchTypeInstance, constraints,
					&ApplyTypeVarOptions{
						ReplaceUnsolved: &ReplaceUnsolvedOptions{
							ScopeIDs:       GetTypeVarScopeIDs(unexpandedSubtype),
							TupleClassType: evaluator.GetTupleClassType(),
						},
					}, nil).(*ClassType); ok {
					resultType = solved
				}
			}
		}
	}

	return classPatternApplyBoundedParams(evaluator, resultType, unexpandedSubtype,
		subjectSubtypeExpanded), true
}

// classPatternApplyBoundedParams is the original's bounded-type-parameter
// fallback, shared by both arms above.
func classPatternApplyBoundedParams(
	evaluator TypeEvaluator, resultType Type, unexpandedSubtype Type, subjectSubtypeExpanded Type,
) Type {
	// The original's comment: for a generic class pattern whose type parameters are
	// bounded (e.g. `class Thing[T: bool]`), the subject may carry no type
	// arguments, leaving the parameters unsolved. Fall back to each parameter's
	// bound -- but only for the pattern class itself. When the subject narrowed to a
	// proper subclass of the pattern class, its own type arguments must be preserved
	// rather than rebuilt from the (widened) base.
	if !IsClassInstance(resultType) || !IsInstantiableClass(unexpandedSubtype) {
		return resultType
	}
	unexpandedClass := unexpandedSubtype.(*ClassType)

	if !ClassTypeIsSameGenericClass(resultType.(*ClassType),
		ClassTypeCloneAsInstance(unexpandedClass, false), 0) {
		return resultType
	}

	hasBoundedParam := false
	for _, param := range unexpandedClass.Shared.TypeParams {
		if param.Shared.BoundType != nil {
			hasBoundedParam = true
			break
		}
	}
	if !hasBoundedParam {
		return resultType
	}

	return specializeBoundedMatchTypeParams(evaluator, unexpandedClass,
		resultType.(*ClassType).Priv.TypeArgs, GetTypeCondition(subjectSubtypeExpanded))
}

// narrowTypeOfClassPatternArg corresponds to the function of the same name. The
// original's comment: narrows the pattern provided for a class pattern argument.
func narrowTypeOfClassPatternArg(
	evaluator TypeEvaluator,
	arg *parser.PatternClassArgumentNode,
	argIndex int,
	positionalArgNames []string,
	matchType Type,
	isPositiveTest bool,
) Type {
	argName := ""
	if arg.D.Name != nil {
		argName = arg.D.Name.D.Value
	} else if argIndex < len(positionalArgNames) {
		argName = positionalArgNames[argIndex]
	}

	if IsAnyOrUnknown(matchType) {
		return matchType
	}

	if !IsClass(matchType) {
		return UnknownTypeCreate(false)
	}
	matchClass := matchType.(*ClassType)

	// The original's comment: according to PEP 634, some built-in types use
	// themselves as the subject for the first positional argument to a class
	// pattern. Although the PEP does state so explicitly, this is true of subclasses
	// of these built-in classes if the subclass doesn't define its own
	// __match_args__.
	useSelfForPattern := false
	selfForPatternType := matchClass

	if arg.D.Name == nil && argIndex == 0 {
		if isClassSpecialCaseForClassPattern(matchClass) {
			useSelfForPattern = true
		} else if len(positionalArgNames) == 0 {
			// Note: the original iterates the whole MRO without breaking, so the
			// *last* matching entry wins. That is reproduced rather than corrected.
			for _, mroClass := range matchClass.Shared.Mro {
				if IsClass(mroClass) && isClassSpecialCaseForClassPattern(mroClass.(*ClassType)) {
					selfForPatternType = mroClass.(*ClassType)
					useSelfForPattern = true
				}
			}
		}
	}

	var argType Type

	if useSelfForPattern {
		argType = ClassTypeCloneAsInstance(selfForPatternType, false)
	} else {
		if argName != "" {
			// The original's comment: we need to apply a rather ugly cast here
			// because PatternClassArgumentNode is not technically an ExpressionNode,
			// but it is OK to use it in this context.
			// The original passes `arg as any as ExpressionNode` here, an explicit
			// cast of a node that is not an expression. Go has no such escape
			// hatch, so nil is passed instead. errorNode is used only to place a
			// class-definition-cycle diagnostic, and this whole call is inside
			// speculative mode where diagnostics are discarded, so nothing that
			// would have surfaced is lost.
			evaluator.UseSpeculativeMode(arg, func() {
				if result := evaluator.GetTypeOfBoundMember(nil,
					ClassTypeCloneAsInstance(matchClass, false), argName, nil, nil, 0, nil); result != nil {
					argType = result.Type
				}
			}, nil)
		}

		if IsNilType(argType) {
			if !isPositiveTest {
				return matchType
			}

			// The original's comment: if the class type in question is "final", we
			// know that no additional attributes can be added by subtypes, so it's
			// safe to eliminate this type entirely.
			if ClassTypeIsFinal(matchClass) {
				return NeverTypeCreateNever()
			}

			argType = UnknownTypeCreate(false)
		}
	}

	return NarrowTypeBasedOnPattern(evaluator, argType, arg.D.Pattern, isPositiveTest)
}

// ValidateClassPattern corresponds to validateClassPattern.
func ValidateClassPattern(evaluator TypeEvaluator, pattern *parser.PatternClassNode) {
	exprType := evaluator.GetTypeOfExpression(classNameExpr(pattern.D.ClassName), EvalFlagsCallBaseDefaults, nil).Type

	// The original's comment: if the expression is a type alias or other special
	// form, treat it as the special form rather than the class.
	if sf := propsSpecialForm(exprType); sf != nil {
		exprType = sf
	}

	if IsAnyOrUnknown(exprType) {
		return
	}

	// The original's comment: check for certain uses of type aliases that generate
	// runtime exceptions.
	if propsTypeAliasInfo(exprType) != nil && IsInstantiableClass(exprType) &&
		exprType.(*ClassType).Priv.TypeArgs != nil &&
		exprType.(*ClassType).Priv.IsTypeArgExplicit != nil &&
		*exprType.(*ClassType).Priv.IsTypeArgExplicit {
		evaluator.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
			localization.LocMessage.ClassPatternTypeAlias().Format(evaluator.PrintType(exprType, nil)),
			classNameExpr(pattern.D.ClassName), nil)
		return
	}

	if !IsInstantiableClass(exprType) {
		if !IsNever(exprType) {
			evaluator.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
				localization.LocAddendum.TypeNotClass().Format(evaluator.PrintType(exprType, nil)),
				classNameExpr(pattern.D.ClassName), nil)
		}
		return
	}
	exprClass := exprType.(*ClassType)

	if ClassTypeIsNewTypeClass(exprClass) {
		evaluator.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
			localization.LocMessage.ClassPatternNewType().Format(evaluator.PrintType(exprType, nil)),
			classNameExpr(pattern.D.ClassName), nil)
		return
	}

	isBuiltIn := isClassSpecialCaseForClassPattern(exprClass)

	// The original's comment: if it's a special-case builtin class, only positional
	// arguments are allowed.
	if isBuiltIn {
		if len(pattern.D.Args) == 1 && pattern.D.Args[0].D.Name != nil {
			evaluator.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
				localization.LocMessage.ClassPatternBuiltInArgPositional(),
				pattern.D.Args[0].D.Name, nil)
		}
	}

	// The original's comment: emits an error if the supplied number of positional
	// patterns is less than expected for the given subject type.
	positionalPatternCount := -1
	for i, arg := range pattern.D.Args {
		if arg.D.Name != nil {
			positionalPatternCount = i
			break
		}
	}
	if positionalPatternCount < 0 {
		positionalPatternCount = len(pattern.D.Args)
	}

	expectedPatternCount := 1
	if !isBuiltIn {
		positionalArgNames := []string{}
		if patternHasPositionalArg(pattern) {
			positionalArgNames = getPositionalMatchArgNames(evaluator, exprClass)
		}

		expectedPatternCount = len(positionalArgNames)
	}

	if positionalPatternCount > expectedPatternCount {
		evaluator.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
			localization.LocMessage.ClassPatternPositionalArgCount().Format(
				exprClass.Shared.Name, expectedPatternCount, positionalPatternCount),
			pattern.D.Args[expectedPatternCount], nil)
	}
}

// patternHasPositionalArg is the original's `pattern.d.args.some((arg) => !arg.d.name)`.
func patternHasPositionalArg(pattern *parser.PatternClassNode) bool {
	for _, arg := range pattern.D.Args {
		if arg.D.Name == nil {
			return true
		}
	}
	return false
}

// classNameExpr narrows ClassNameNode -- the original's `NameNode |
// MemberAccessNode` union -- to an ExpressionNode. Both arms are expressions;
// the union type simply does not declare that.
func classNameExpr(node parser.ClassNameNode) parser.ExpressionNode {
	switch typed := node.(type) {
	case *parser.NameNode:
		return typed
	case *parser.MemberAccessNode:
		return typed
	}
	return nil
}
