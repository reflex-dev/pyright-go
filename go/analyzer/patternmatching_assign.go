/*
 * patternmatching_assign.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/patternMatching.ts (pyright 1.1.412):
 * assignTypeToPatternTargets and getPatternSubtypeNarrowingCallback.
 *
 * assignTypeToPatternTargets is the second half of pattern matching: once the
 * subject has been narrowed, the names bound by the pattern have to be given
 * types. It re-runs the positive narrowing first, because the type a capture
 * binds is the *narrowed* subject rather than the original -- `case [int(), y]`
 * binds y from a subject already known to be a two-element sequence.
 *
 * The as-pattern case narrows between alternatives for the same reason the
 * narrower does: `case A() | B() as x` binds x to A-or-B, but the B alternative
 * only sees what A did not already capture.
 *
 * The wildcard `_` is the one capture that binds nothing, and it is handled
 * separately because it is where the unknown-type diagnostics live. That is
 * deliberate: `case _:` is the exhaustive fallback, so if the subject reaches it
 * partially unknown the user has lost type information without any name to
 * inspect.
 *
 * getPatternSubtypeNarrowingCallback is unrelated to binding and belongs to the
 * code-flow engine. It answers a narrower question: when the *subject* of a
 * match is a compound expression -- `x[0]`, `(x, y)`, `x.tag` -- narrowing the
 * subject should also narrow the referenced variable. Each of the three forms it
 * recognizes is a discriminated-union idiom, and each defers to the
 * corresponding narrower in typeGuards.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/localization"
	"github.com/microsoft/pyright/go/parser"
)

// AssignTypeToPatternTargets corresponds to assignTypeToPatternTargets. The
// original's comment: recursively assigns the specified type to the pattern and
// any capture nodes within it. It returns the narrowed type, as dictated by the
// pattern.
func AssignTypeToPatternTargets(
	evaluator TypeEvaluator, t Type, isTypeIncomplete bool, pattern parser.ParseNode,
) Type {
	// The original's comment: further narrow the type based on this pattern.
	narrowedType := NarrowTypeBasedOnPattern(evaluator, t, pattern, true)

	switch typed := pattern.(type) {
	case *parser.PatternSequenceNode:
		assignSequencePatternTargets(evaluator, narrowedType, isTypeIncomplete, typed)

	case *parser.PatternAsNode:
		if typed.D.Target != nil {
			evaluator.AssignTypeToExpression(typed.D.Target,
				&TypeResult{Type: narrowedType, IsIncomplete: isTypeIncomplete}, typed.D.Target)
		}

		runningNarrowedType := narrowedType
		for _, orPattern := range typed.D.OrPatterns {
			AssignTypeToPatternTargets(evaluator, runningNarrowedType, isTypeIncomplete, orPattern)

			// The original's comment: OR patterns are evaluated left to right, so we
			// can narrow the type as we go.
			runningNarrowedType = NarrowTypeBasedOnPattern(
				evaluator, runningNarrowedType, orPattern, false)
		}

	case *parser.PatternCaptureNode:
		assignCapturePatternTarget(evaluator, narrowedType, isTypeIncomplete, typed)

	case *parser.PatternMappingNode:
		assignMappingPatternTargets(evaluator, narrowedType, isTypeIncomplete, typed)

	case *parser.PatternClassNode:
		assignClassPatternTargets(evaluator, narrowedType, isTypeIncomplete, typed)

	default:
		// PatternLiteral, PatternValue and Error bind nothing. The original's
		// comment: nothing to do here.
	}

	return narrowedType
}

// assignSequencePatternTargets is the original's PatternSequence case.
func assignSequencePatternTargets(
	evaluator TypeEvaluator, narrowedType Type, isTypeIncomplete bool, pattern *parser.PatternSequenceNode,
) {
	all := getSequencePatternInfo(evaluator, pattern, narrowedType)
	sequenceInfo := make([]*SequencePatternInfo, 0, len(all))
	for _, seqInfo := range all {
		if !seqInfo.IsDefiniteNoMatch {
			sequenceInfo = append(sequenceInfo, seqInfo)
		}
	}

	for index, entry := range pattern.D.Entries {
		entryTypes := make([]Type, 0, len(sequenceInfo))
		for _, info := range sequenceInfo {
			entryTypes = append(entryTypes, getTypeOfPatternSequenceEntry(evaluator, pattern, info,
				index, len(pattern.D.Entries), pattern.D.StarEntryIndex, false))
		}

		AssignTypeToPatternTargets(evaluator, CombineTypes(entryTypes, nil), isTypeIncomplete, entry)
	}
}

// assignCapturePatternTarget is the original's PatternCapture case.
func assignCapturePatternTarget(
	evaluator TypeEvaluator, narrowedType Type, isTypeIncomplete bool, pattern *parser.PatternCaptureNode,
) {
	if !pattern.D.IsWildcard {
		evaluator.AssignTypeToExpression(pattern.D.Target,
			&TypeResult{Type: narrowedType, IsIncomplete: isTypeIncomplete}, pattern.D.Target)
		return
	}

	if isTypeIncomplete {
		return
	}

	// `case _:` binds nothing, so an unknown subject here means information was
	// lost with no name left to inspect.
	if IsUnknown(narrowedType) {
		evaluator.AddDiagnostic(DiagnosticRuleReportUnknownVariableType,
			localization.LocMessage.WildcardPatternTypeUnknown(), pattern.D.Target, nil)
		return
	}

	if IsPartlyUnknown(narrowedType, 0) {
		diagAddendum := common.NewDiagnosticAddendum()
		diagAddendum.AddMessage(localization.LocAddendum.TypeOfSymbol().Format("_",
			evaluator.PrintType(narrowedType, &PrintTypeOptions{ExpandTypeAlias: true})))
		evaluator.AddDiagnostic(DiagnosticRuleReportUnknownVariableType,
			localization.LocMessage.WildcardPatternTypePartiallyUnknown()+diagAddendum.GetString(),
			pattern.D.Target, nil)
	}
}

// assignMappingPatternTargets is the original's PatternMapping case.
func assignMappingPatternTargets(
	evaluator TypeEvaluator, narrowedType Type, isTypeIncomplete bool, pattern *parser.PatternMappingNode,
) {
	mappingInfo := getMappingPatternInfo(evaluator, narrowedType, pattern)

	for _, entry := range pattern.D.Entries {
		keyTypes := []Type{}
		valueTypes := []Type{}

		for _, mappingSubtypeInfo := range mappingInfo {
			if mappingSubtypeInfo.TypedDict != nil {
				collectTypedDictPatternEntryTypes(evaluator, pattern, entry, mappingSubtypeInfo,
					&keyTypes, &valueTypes)
				continue
			}

			if mappingSubtypeInfo.DictTypeArgs == nil {
				continue
			}

			switch mappingEntry := entry.(type) {
			case *parser.PatternMappingKeyEntryNode:
				keyType := NarrowTypeBasedOnPattern(evaluator,
					mappingSubtypeInfo.DictTypeArgs.Key, mappingEntry.D.KeyPattern, true)
				keyTypes = append(keyTypes, keyType)
				valueTypes = append(valueTypes, NarrowTypeBasedOnPattern(evaluator,
					mappingSubtypeInfo.DictTypeArgs.Value, mappingEntry.D.ValuePattern, true))

			case *parser.PatternMappingExpandEntryNode:
				keyTypes = append(keyTypes, mappingSubtypeInfo.DictTypeArgs.Key)
				valueTypes = append(valueTypes, mappingSubtypeInfo.DictTypeArgs.Value)
			}
		}

		keyType := CombineTypes(keyTypes, nil)
		valueType := CombineTypes(valueTypes, nil)

		switch mappingEntry := entry.(type) {
		case *parser.PatternMappingKeyEntryNode:
			AssignTypeToPatternTargets(evaluator, keyType, isTypeIncomplete, mappingEntry.D.KeyPattern)
			AssignTypeToPatternTargets(evaluator, valueType, isTypeIncomplete, mappingEntry.D.ValuePattern)

		case *parser.PatternMappingExpandEntryNode:
			// `**rest` binds a fresh dict of whatever the unmatched keys and values
			// were determined to be.
			dictClass := evaluator.GetBuiltInType(pattern, "dict")
			strType := evaluator.GetBuiltInObject(pattern, "str", nil)
			var dictType Type = UnknownTypeCreate(false)
			if dictClass != nil && IsInstantiableClass(dictClass) && IsClassInstance(strType) {
				dictType = ClassTypeCloneAsInstance(ClassTypeSpecialize(dictClass.(*ClassType),
					[]Type{keyType, valueType}, nil, false, nil, nil), true)
			}
			evaluator.AssignTypeToExpression(mappingEntry.D.Target,
				&TypeResult{Type: dictType, IsIncomplete: isTypeIncomplete},
				mappingEntry.D.Target)
		}
	}
}

// collectTypedDictPatternEntryTypes is the TypedDict arm of the mapping entry
// loop.
func collectTypedDictPatternEntryTypes(
	evaluator TypeEvaluator,
	pattern *parser.PatternMappingNode,
	entry parser.ParseNode,
	mappingSubtypeInfo *MappingPatternInfo,
	keyTypes *[]Type,
	valueTypes *[]Type,
) {
	switch mappingEntry := entry.(type) {
	case *parser.PatternMappingKeyEntryNode:
		keyType := NarrowTypeBasedOnPattern(evaluator,
			evaluator.GetBuiltInObject(pattern, "str", nil), mappingEntry.D.KeyPattern, true)
		*keyTypes = append(*keyTypes, keyType)

		DoForEachSubtype(keyType, func(keySubtype Type, _ int, _ []Type) {
			if IsClassInstance(keySubtype) && ClassTypeIsBuiltInNamed(keySubtype.(*ClassType), "str") &&
				IsLiteralType(keySubtype.(*ClassType)) {
				if s, ok := keySubtype.(*ClassType).Priv.LiteralValue.(LiteralString); ok {
					tdEntries := GetTypedDictMembersForClass(evaluator, mappingSubtypeInfo.TypedDict, false)
					if valueInfo, found := tdEntries.KnownItems.Get(string(s)); found && valueInfo != nil {
						*valueTypes = append(*valueTypes, valueInfo.ValueType)
						return
					}
				}
			}
			*valueTypes = append(*valueTypes, UnknownTypeCreate(false))
		})

	case *parser.PatternMappingExpandEntryNode:
		*keyTypes = append(*keyTypes, evaluator.GetBuiltInObject(pattern, "str", nil))
		*valueTypes = append(*valueTypes, evaluator.GetObjectType())
	}
}

// assignClassPatternTargets is the original's PatternClass case.
func assignClassPatternTargets(
	evaluator TypeEvaluator, narrowedType Type, isTypeIncomplete bool, pattern *parser.PatternClassNode,
) {
	argTypes := make([][]Type, len(pattern.D.Args))
	for i := range argTypes {
		argTypes[i] = []Type{}
	}

	evaluator.MapSubtypesExpandTypeVars(narrowedType, nil,
		func(expandedSubtype Type, _ Type) Type {
			if !IsClassInstance(expandedSubtype) {
				for index := range pattern.D.Args {
					argTypes[index] = append(argTypes[index], UnknownTypeCreate(false))
				}
				return nil
			}
			expandedClass := expandedSubtype.(*ClassType)

			DoForEachSubtype(narrowedType, func(subjectSubtype Type, _ int, _ []Type) {
				concreteSubtype := evaluator.MakeTopLevelTypeVarsConcrete(subjectSubtype, false)

				if IsAnyOrUnknown(concreteSubtype) {
					for index := range pattern.D.Args {
						argTypes[index] = append(argTypes[index], concreteSubtype)
					}
					return
				}

				if !IsClassInstance(concreteSubtype) {
					return
				}

				// The original's comment: are there any positional arguments? If so,
				// try to get the mappings for these arguments by fetching the
				// __match_args__ symbol from the class.
				positionalArgNames := []string{}
				if patternHasPositionalArg(pattern) {
					positionalArgNames = getPositionalMatchArgNames(evaluator,
						ClassTypeCloneAsInstantiable(expandedClass, false))
				}

				for index, arg := range pattern.D.Args {
					narrowedArgType := narrowTypeOfClassPatternArg(evaluator, arg, index,
						positionalArgNames, ClassTypeCloneAsInstantiable(expandedClass, false), true)
					argTypes[index] = append(argTypes[index], narrowedArgType)
				}
			})

			return nil
		})

	for index, arg := range pattern.D.Args {
		AssignTypeToPatternTargets(evaluator, CombineTypes(argTypes[index], nil),
			isTypeIncomplete, arg.D.Pattern)
	}
}

// GetPatternSubtypeNarrowingCallback corresponds to
// getPatternSubtypeNarrowingCallback. The original's comment: determines whether
// the reference expression has a relationship to the subject expression in such a
// way that the type of the reference expression can be narrowed based on the
// narrowed type of the subject expression.
func GetPatternSubtypeNarrowingCallback(
	evaluator TypeEvaluator, reference parser.ExpressionNode, subjectExpression parser.ExpressionNode,
) PatternSubtypeNarrowingCallback {
	// The original's comment: look for a subject expression of the form
	// <reference>[<literal>] where <literal> is either a str (for TypedDict
	// discrimination) or an int (for tuple discrimination).
	if indexNode, ok := subjectExpression.(*parser.IndexNode); ok {
		if cb := patternIndexNarrowingCallback(evaluator, reference, indexNode); cb != nil {
			return cb
		}
	}

	// The original's comment: look for a subject expression that contains the
	// reference expression as an entry in a tuple.
	if tupleNode, ok := subjectExpression.(*parser.TupleNode); ok {
		if cb := patternTupleNarrowingCallback(evaluator, reference, tupleNode); cb != nil {
			return cb
		}
	}

	// The original's comment: look for a subject expression of the form "a.b" where
	// "b" is an attribute that is annotated with a literal type.
	if memberNode, ok := subjectExpression.(*parser.MemberAccessNode); ok {
		if IsMatchingExpression(reference, memberNode.D.LeftExpr, nil) {
			return patternMemberNarrowingCallback(evaluator, memberNode)
		}
	}

	return nil
}

// patternIndexNarrowingCallback is the original's Index arm.
func patternIndexNarrowingCallback(
	evaluator TypeEvaluator, reference parser.ExpressionNode, subjectExpression *parser.IndexNode,
) PatternSubtypeNarrowingCallback {
	if len(subjectExpression.D.Items) != 1 || subjectExpression.D.TrailingComma ||
		subjectExpression.D.Items[0].D.ArgCategory != parser.ArgCategorySimple ||
		!IsMatchingExpression(reference, subjectExpression.D.LeftExpr, nil) {
		return nil
	}

	indexTypeResult := evaluator.GetTypeOfExpression(
		subjectExpression.D.Items[0].D.ValueExpr, EvalFlagsNone, nil)
	indexType := indexTypeResult.Type

	if !IsClassInstance(indexType) || !IsLiteralType(indexType.(*ClassType)) {
		return nil
	}
	indexClass := indexType.(*ClassType)
	if !ClassTypeIsBuiltInNamed(indexClass, "int", "str") {
		return nil
	}

	unnarrowedReferenceTypeResult := evaluator.GetTypeOfExpression(
		subjectExpression.D.LeftExpr, EvalFlagsCallBaseDefaults, nil)
	unnarrowedReferenceType := unnarrowedReferenceTypeResult.Type

	return func(narrowedSubjectType Type) *TypeResult {
		canNarrow := true
		typesToCombine := []Type{}

		DoForEachSubtype(narrowedSubjectType, func(subtype Type, _ int, _ []Type) {
			subtype = evaluator.MakeTopLevelTypeVarsConcrete(subtype, false)

			if IsClassInstance(subtype) && subtype.(*ClassType).Priv.LiteralValue != nil {
				if ClassTypeIsBuiltInNamed(indexClass, "str") {
					typesToCombine = append(typesToCombine,
						narrowTypeForDiscriminatedDictEntryComparison(evaluator,
							unnarrowedReferenceType, indexClass, subtype.(*ClassType), true))
				} else {
					typesToCombine = append(typesToCombine,
						narrowTypeForDiscriminatedTupleComparison(evaluator,
							unnarrowedReferenceType, indexClass, subtype.(*ClassType), true))
				}
				return
			}

			if !IsNever(subtype) {
				// The original's comment: we don't know how to narrow in this case.
				canNarrow = false
			}
		})

		if !canNarrow {
			return nil
		}

		return &TypeResult{
			Type:         CombineTypes(typesToCombine, nil),
			IsIncomplete: indexTypeResult.IsIncomplete || unnarrowedReferenceTypeResult.IsIncomplete,
		}
	}
}

// patternTupleNarrowingCallback is the original's Tuple arm.
func patternTupleNarrowingCallback(
	evaluator TypeEvaluator, reference parser.ExpressionNode, subjectExpression *parser.TupleNode,
) PatternSubtypeNarrowingCallback {
	matchingEntryIndex := -1
	for i, expr := range subjectExpression.D.Items {
		if IsMatchingExpression(reference, expr, nil) {
			matchingEntryIndex = i
			break
		}
	}
	if matchingEntryIndex < 0 {
		return nil
	}

	typeResult := evaluator.GetTypeOfExpression(
		subjectExpression.D.Items[matchingEntryIndex], EvalFlagsNone, nil)

	return func(narrowedSubjectType Type) *TypeResult {
		canNarrow := true
		narrowedSubtypes := []Type{}

		DoForEachSubtype(narrowedSubjectType, func(subtype Type, _ int, _ []Type) {
			if IsClassInstance(subtype) && ClassTypeIsBuiltInNamed(subtype.(*ClassType), "tuple") &&
				subtype.(*ClassType).Priv.TupleTypeArgs != nil &&
				matchingEntryIndex < len(subtype.(*ClassType).Priv.TupleTypeArgs) {
				allBounded := true
				for _, e := range subtype.(*ClassType).Priv.TupleTypeArgs {
					if e.IsUnbounded {
						allBounded = false
						break
					}
				}
				if allBounded {
					narrowedSubtypes = append(narrowedSubtypes,
						subtype.(*ClassType).Priv.TupleTypeArgs[matchingEntryIndex].Type)
					return
				}
			}

			// Note: the original tests `isNever(narrowedSubjectType)` here, not
			// `isNever(subtype)`. That looks like a slip but is reproduced, since
			// changing it would alter which subjects are considered narrowable.
			if IsNever(narrowedSubjectType) {
				narrowedSubtypes = append(narrowedSubtypes, narrowedSubjectType)
				return
			}

			canNarrow = false
		})

		if !canNarrow {
			return nil
		}
		return &TypeResult{Type: CombineTypes(narrowedSubtypes, nil), IsIncomplete: typeResult.IsIncomplete}
	}
}

// patternMemberNarrowingCallback is the original's MemberAccess arm.
func patternMemberNarrowingCallback(
	evaluator TypeEvaluator, subjectExpression *parser.MemberAccessNode,
) PatternSubtypeNarrowingCallback {
	unnarrowedReferenceTypeResult := evaluator.GetTypeOfExpression(
		subjectExpression.D.LeftExpr, EvalFlagsCallBaseDefaults, nil)
	unnarrowedReferenceType := unnarrowedReferenceTypeResult.Type

	return func(narrowedSubjectType Type) *TypeResult {
		if IsNever(narrowedSubjectType) {
			return &TypeResult{Type: NeverTypeCreateNever()}
		}

		if !IsLiteralTypeOrUnion(narrowedSubjectType, false) {
			return nil
		}

		resultType := MapSubtypes(narrowedSubjectType, func(literalSubtype Type) Type {
			// The original asserts the subtype is a literal class instance here;
			// isLiteralTypeOrUnion above already established it.
			if !IsClassInstance(literalSubtype) || literalSubtype.(*ClassType).Priv.LiteralValue == nil {
				return literalSubtype
			}

			return narrowTypeForDiscriminatedLiteralFieldComparison(evaluator,
				unnarrowedReferenceType, subjectExpression.D.Member.D.Value,
				literalSubtype.(*ClassType), true)
		}, nil)

		return &TypeResult{Type: resultType}
	}
}
