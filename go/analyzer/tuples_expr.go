/*
 * tuples_expr.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/tuples.ts (pyright 1.1.412):
 * getTypeOfTuple, getTypeOfTupleWithContext, getTypeOfTupleInferred; plus
 * buildTupleTypesList, which lives in typeEvaluator.ts but exists only to serve
 * these three through the evaluator interface.
 *
 * Tuple displays follow the same bidirectional shape as dict and list displays:
 * try against the expected type, fall back to inference. The tuple-specific
 * wrinkle is in getTypeOfTupleWithContext's expected-type array: when the
 * expected tuple has an unbounded entry (`tuple[int, ...]`), that entry is
 * either dropped or duplicated until the expected list is exactly as long as
 * the source display, so each source element gets a positional expected type.
 *
 * buildTupleTypesList is where an unpacked entry (`*x`) is flattened. If the
 * unpacked operand is itself a tuple with known arguments, those arguments are
 * spliced in; otherwise the result is a single unbounded entry, because an
 * arbitrary iterator has no known length. The final fold exists to preserve a
 * type-model invariant: a tuple may carry at most one unbounded entry, so any
 * run of them collapses into one whose type is their union.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/localization"
	"github.com/microsoft/pyright/go/parser"
)

// maxInferredTupleEntryCount is the original's constant of the same name.
const maxInferredTupleEntryCount = 256

// GetTypeOfTuple corresponds to getTypeOfTuple.
func GetTypeOfTuple(
	evaluator TypeEvaluator,
	node *parser.TupleNode,
	flags EvalFlags,
	inferenceContext *InferenceContext,
) *TypeResult {
	if flags&EvalFlagsTypeExpression != 0 {
		parent := node.NodeBase().Parent
		if parent == nil || parent.GetNodeType() != parser.ParseNodeTypeArgument {
			// The original's comment: this is allowed inside of an index trailer,
			// specifically to support Tuple[()], which is the documented way to annotate
			// a zero-length tuple.
			diag := common.NewDiagnosticAddendum()
			diag.AddMessage(localization.LocAddendum.UseTupleInstead())
			evaluator.AddDiagnostic(
				DiagnosticRuleReportInvalidTypeForm,
				localization.LocMessage.TupleInAnnotation()+diag.GetString(),
				node, nil)

			return &TypeResult{Type: UnknownTypeCreate(false)}
		}
	}

	if flags&EvalFlagsInstantiableType != 0 && len(node.D.Items) == 0 && inferenceContext == nil {
		return &TypeResult{
			Type:                  MakeTupleObject(evaluator, []*TupleTypeArg{}, false),
			IsEmptyTupleShorthand: true,
		}
	}

	flags &^= EvalFlagsTypeExpression | EvalFlagsStrLiteralAsType | EvalFlagsInstantiableType

	// The original's comment: if the expected type is a union, recursively call for
	// each of the subtypes to find one that matches.
	var expectedType Type
	expectedTypeContainsAny := false
	if inferenceContext != nil {
		expectedType = inferenceContext.ExpectedType
		expectedTypeContainsAny = IsAny(inferenceContext.ExpectedType)
	}

	if inferenceContext != nil && IsUnion(inferenceContext.ExpectedType) {
		var matchingSubtype Type

		DoForEachSubtypeSorted(inferenceContext.ExpectedType, func(subtype Type, _ int, _ []Type) {
			if IsAny(subtype) {
				expectedTypeContainsAny = true
			}

			if matchingSubtype == nil {
				var subtypeResult *TypeResult
				evaluator.UseSpeculativeMode(node, func() {
					subtypeResult = GetTypeOfTupleWithContext(
						evaluator, node, flags, MakeInferenceContext(subtype, false, nil))
				}, nil)

				if subtypeResult != nil &&
					evaluator.AssignType(subtype, subtypeResult.Type, nil, nil, AssignTypeFlagsDefault, 0) {
					matchingSubtype = subtype
				}
			}
		})

		expectedType = matchingSubtype
	}

	var expectedTypeDiagAddendum *common.DiagnosticAddendum
	if expectedType != nil {
		result := GetTypeOfTupleWithContext(
			evaluator, node, flags, MakeInferenceContext(expectedType, false, nil))

		if result != nil && !result.TypeErrors {
			return result
		}

		if result != nil {
			expectedTypeDiagAddendum = result.ExpectedTypeDiagAddendum
		}
	}

	typeResult := GetTypeOfTupleInferred(evaluator, node, flags)

	// The original's comment: if there was an expected type of Any, replace the
	// resulting type with Any rather than return a type with unknowns.
	if expectedTypeContainsAny {
		typeResult.Type = AnyTypeCreate(false)
	}

	result := *typeResult
	result.ExpectedTypeDiagAddendum = expectedTypeDiagAddendum
	return &result
}

// GetTypeOfTupleWithContext corresponds to getTypeOfTupleWithContext. It returns
// nil where the original returns undefined.
func GetTypeOfTupleWithContext(
	evaluator TypeEvaluator,
	node *parser.TupleNode,
	flags EvalFlags,
	inferenceContext *InferenceContext,
) *TypeResult {
	inferenceContext.ExpectedType = TransformPossibleRecursiveTypeAlias(inferenceContext.ExpectedType, 0)
	if !IsClassInstance(inferenceContext.ExpectedType) {
		return nil
	}
	expectedClass := inferenceContext.ExpectedType.(*ClassType)

	tupleClass := evaluator.GetTupleClassType()
	if tupleClass == nil || !IsInstantiableClass(tupleClass) {
		return nil
	}

	// The original's comment: build an array of expected types.
	expectedTypes := []Type{}

	if IsTupleClass(expectedClass) && expectedClass.Priv.TupleTypeArgs != nil {
		for _, t := range expectedClass.Priv.TupleTypeArgs {
			expectedTypes = append(expectedTypes, TransformPossibleRecursiveTypeAlias(t.Type, 0))
		}
		unboundedIndex := -1
		for i, t := range expectedClass.Priv.TupleTypeArgs {
			if t.IsUnbounded {
				unboundedIndex = i
				break
			}
		}
		if unboundedIndex >= 0 {
			if len(expectedTypes) > len(node.D.Items) {
				expectedTypes = append(expectedTypes[:unboundedIndex], expectedTypes[unboundedIndex+1:]...)
			} else {
				for len(expectedTypes) < len(node.D.Items) {
					dup := expectedTypes[unboundedIndex]
					expectedTypes = append(expectedTypes[:unboundedIndex],
						append([]Type{dup}, expectedTypes[unboundedIndex:]...)...)
				}
			}
		}
	} else {
		tupleConstraints := NewConstraintTracker()
		if !AddConstraintsForExpectedType(
			evaluator,
			ClassTypeCloneAsInstance(tupleClass, true),
			inferenceContext.ExpectedType,
			tupleConstraints,
			GetTypeVarScopesForNode(node),
			node.NodeBase().TextRange.Start,
		) {
			return nil
		}

		solved := evaluator.SolveAndApplyConstraints(tupleClass, tupleConstraints, nil, nil)
		specializedTuple, ok := solved.(*ClassType)
		if !ok || len(specializedTuple.Priv.TypeArgs) != 1 {
			return nil
		}

		homogenousType := TransformPossibleRecursiveTypeAlias(specializedTuple.Priv.TypeArgs[0], 0)
		for i := 0; i < len(node.D.Items); i++ {
			expectedTypes = append(expectedTypes, homogenousType)
		}
	}

	entryTypeResults := make([]*TypeResult, 0, len(node.D.Items))
	for index, expr := range node.D.Items {
		var entryExpected Type
		if index < len(expectedTypes) {
			entryExpected = expectedTypes[index]
		}
		entryTypeResults = append(entryTypeResults, evaluator.GetTypeOfExpression(
			expr,
			flags|EvalFlagsStripTupleLiterals,
			MakeInferenceContext(entryExpected, inferenceContext.IsTypeIncomplete, nil),
		))
	}

	isIncomplete := false
	for _, result := range entryTypeResults {
		if result.IsIncomplete {
			isIncomplete = true
			break
		}
	}

	// The original's comment: copy any expected type diag addenda for precision
	// error reporting.
	var expectedTypeDiagAddendum *common.DiagnosticAddendum
	for _, result := range entryTypeResults {
		if result.ExpectedTypeDiagAddendum != nil {
			expectedTypeDiagAddendum = common.NewDiagnosticAddendum()
			break
		}
	}
	if expectedTypeDiagAddendum != nil {
		for _, result := range entryTypeResults {
			if result.ExpectedTypeDiagAddendum != nil {
				expectedTypeDiagAddendum.AddAddendum(result.ExpectedTypeDiagAddendum)
			}
		}
	}

	// The original's comment: if the tuple contains a very large number of entries,
	// it's probably generated code. If we encounter type errors, don't bother
	// building the full tuple type.
	anyTypeErrors := false
	for _, result := range entryTypeResults {
		if result.TypeErrors {
			anyTypeErrors = true
			break
		}
	}

	var t Type
	if len(node.D.Items) > maxInferredTupleEntryCount && anyTypeErrors {
		t = MakeTupleObject(evaluator,
			[]*TupleTypeArg{{Type: UnknownTypeCreate(false), IsUnbounded: true}}, false)
	} else {
		t = MakeTupleObject(evaluator,
			evaluator.BuildTupleTypesList(entryTypeResults, false, false), false)
	}

	return &TypeResult{Type: t, ExpectedTypeDiagAddendum: expectedTypeDiagAddendum, IsIncomplete: isIncomplete}
}

// GetTypeOfTupleInferred corresponds to getTypeOfTupleInferred.
func GetTypeOfTupleInferred(evaluator TypeEvaluator, node *parser.TupleNode, flags EvalFlags) *TypeResult {
	entryTypeResults := make([]*TypeResult, 0, len(node.D.Items))
	for _, expr := range node.D.Items {
		entryTypeResults = append(entryTypeResults,
			evaluator.GetTypeOfExpression(expr, flags|EvalFlagsStripTupleLiterals, nil))
	}

	isIncomplete := false
	for _, result := range entryTypeResults {
		if result.IsIncomplete {
			isIncomplete = true
			break
		}
	}

	// The original's comment: if the tuple contains a very large number of entries,
	// it's probably generated code. Rather than taking the time to evaluate every
	// entry, simply return an unknown type in this case.
	if len(node.D.Items) > maxInferredTupleEntryCount {
		return &TypeResult{Type: MakeTupleObject(evaluator,
			[]*TupleTypeArg{{Type: UnknownTypeCreate(false), IsUnbounded: true}}, false)}
	}

	t := MakeTupleObject(evaluator, evaluator.BuildTupleTypesList(
		entryTypeResults, flags&EvalFlagsStripTupleLiterals != 0, true), false)

	if isIncomplete {
		if GetContainerDepth(t, 0) > maxInferredContainerDepth {
			return &TypeResult{Type: UnknownTypeCreate(false)}
		}
	}

	return &TypeResult{Type: t, IsIncomplete: isIncomplete}
}

// BuildTupleTypesList corresponds to buildTupleTypesList in typeEvaluator.ts.
func (e *typeEvaluator) BuildTupleTypesList(
	entryTypeResults []*TypeResult, stripLiterals bool, convertModule bool,
) []*TupleTypeArg {
	entryTypes := []*TupleTypeArg{}

	for _, typeResult := range entryTypeResults {
		var possibleUnpackedTuple Type
		if typeResult.UnpackedType != nil {
			possibleUnpackedTuple = typeResult.UnpackedType
		} else if IsUnpacked(typeResult.Type) {
			possibleUnpackedTuple = typeResult.Type
		}

		// The original's comment: is this an unpacked tuple? If so, we can append the
		// individual unpacked entries onto the new tuple. If it's not an upacked tuple
		// but some other iterator (e.g. a List), we won't know the number of items, so
		// we'll need to leave the Tuple open-ended.
		switch {
		case possibleUnpackedTuple != nil && IsClassInstance(possibleUnpackedTuple) &&
			possibleUnpackedTuple.(*ClassType).Priv.TupleTypeArgs != nil:
			// The original re-tests the same array for undefined here, which cannot be
			// true given the guard above; the else arm is dead. Kept as written.
			typeArgs := possibleUnpackedTuple.(*ClassType).Priv.TupleTypeArgs
			if typeArgs == nil {
				entryTypes = append(entryTypes,
					&TupleTypeArg{Type: UnknownTypeCreate(false), IsUnbounded: true})
			} else {
				entryTypes = append(entryTypes, typeArgs...)
			}

		case IsNever(typeResult.Type) && typeResult.IsIncomplete && typeResult.UnpackedType == nil:
			entryTypes = append(entryTypes,
				&TupleTypeArg{Type: UnknownTypeCreate(true), IsUnbounded: false})

		default:
			entryType := e.convertSpecialFormToRuntimeValueEx(typeResult.Type, EvalFlagsNone, convertModule)
			if stripLiterals {
				entryType = StripTypeForm(e.StripLiteralValue(entryType))
			}
			entryTypes = append(entryTypes,
				&TupleTypeArg{Type: entryType, IsUnbounded: typeResult.UnpackedType != nil})
		}
	}

	// The original's comment: if there are multiple unbounded entries, combine all
	// of them into a single unbounded entry to avoid violating the invariant that
	// there can be at most one unbounded entry in a tuple.
	unboundedCount := 0
	firstUnboundedEntryIndex := -1
	for i, t := range entryTypes {
		if t.IsUnbounded {
			unboundedCount++
			if firstUnboundedEntryIndex < 0 {
				firstUnboundedEntryIndex = i
			}
		}
	}
	if unboundedCount > 1 {
		removedEntries := entryTypes[firstUnboundedEntryIndex:]
		removedTypes := make([]Type, 0, len(removedEntries))
		for _, t := range removedEntries {
			removedTypes = append(removedTypes, t.Type)
		}
		entryTypes = append(entryTypes[:firstUnboundedEntryIndex],
			&TupleTypeArg{Type: CombineTypes(removedTypes, nil), IsUnbounded: true})
	}

	return entryTypes
}

// getTypeOfTuple delegates to the tuples.ts function of the same name, which the
// original reaches as a module import.
func (e *typeEvaluator) getTypeOfTuple(
	node *parser.TupleNode, flags EvalFlags, inferenceContext *InferenceContext,
) *TypeResult {
	return GetTypeOfTuple(e, node, flags, inferenceContext)
}
