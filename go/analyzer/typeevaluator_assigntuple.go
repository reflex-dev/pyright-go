/*
 * typeevaluator_assigntuple.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/typeEvaluator.ts (pyright 1.1.412):
 * assignTypeToTupleOrListNode.
 *
 * Destructuring assignment: `a, b = x` and `[a, *rest] = x`. The shape is a
 * per-target accumulator -- targetTypes[i] is a list, not a type -- because the
 * source may be a union and each subtype contributes its own candidate to every
 * position. Only at the very end is each position's list combined and assigned.
 *
 * Each source subtype takes one of two paths. A tuple with known arguments is
 * matched positionally, after two length adjustments: an unbounded source entry
 * is replicated or dropped to reach the target count, and a starred target
 * absorbs the surplus into a union (or, when the source is exactly one short,
 * receives Never -- the empty-remainder case). Anything else is treated as an
 * iterable, and every target gets the same element type.
 *
 * The length-mismatch diagnostic is collected per subtype into a shared
 * addendum and reported once, so a union whose subtypes disagree about arity
 * produces one error listing each offender rather than one error per subtype.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/localization"
	"github.com/microsoft/pyright/go/parser"
)

// typeSliceSplice removes count entries starting at index and returns them,
// standing in for Array.prototype.splice over a []Type.
func typeSliceSplice(types *[]Type, index int, count int) []Type {
	if count < 0 {
		count = 0
	}
	if index > len(*types) {
		index = len(*types)
	}
	if index+count > len(*types) {
		count = len(*types) - index
	}

	removed := append([]Type(nil), (*types)[index:index+count]...)
	*types = append((*types)[:index], (*types)[index+count:]...)
	return removed
}

// typeSliceInsert inserts one entry at index.
func typeSliceInsert(types *[]Type, index int, t Type) {
	if index > len(*types) {
		index = len(*types)
	}

	*types = append(*types, nil)
	copy((*types)[index+1:], (*types)[index:])
	(*types)[index] = t
}

// assignTypeToTupleOrListNode corresponds to the function of the same name: the
// destructuring case, which matches the source's elements against the targets
// and handles a starred target absorbing the remainder.
func (e *typeEvaluator) assignTypeToTupleOrListNode(
	target parser.ExpressionNode, typeResult *TypeResult, srcExpr parser.ExpressionNode,
) {
	isListTarget := target.GetNodeType() == parser.ParseNodeTypeList

	var targetExpressions []parser.ExpressionNode
	switch t := target.(type) {
	case *parser.ListNode:
		targetExpressions = t.D.Items
	case *parser.TupleNode:
		targetExpressions = t.D.Items
	default:
		// The original's parameter type admits only these two node types.
		return
	}

	// The original's comment: initialize the array of target types, one for each
	// target.
	targetTypes := make([][]Type, len(targetExpressions))
	for i := range targetTypes {
		targetTypes[i] = []Type{}
	}

	// The original computes the same findIndex twice, into targetUnpackIndex and
	// unpackIndex, and reads each in a different place. They are always equal.
	unpackIndex := -1
	for i, expr := range targetExpressions {
		if expr.GetNodeType() == parser.ParseNodeTypeUnpack {
			unpackIndex = i
			break
		}
	}
	targetUnpackIndex := unpackIndex

	// The original rebinds its own `typeResult` parameter to a spread copy with a
	// concretized type; rebinding a Go pointer parameter would be invisible to
	// the caller either way, but copying keeps the caller's TypeResult untouched
	// as the spread does.
	concrete := *typeResult
	concrete.Type = e.makeTopLevelTypeVarsConcrete(typeResult.Type, false, nil)
	typeResult = &concrete

	diagAddendum := common.NewDiagnosticAddendum()

	DoForEachSubtype(typeResult.Type, func(subtype Type, _ int, _ []Type) {
		// The original's comment: is this subtype a tuple?
		tupleType := GetSpecializedTupleType(subtype)
		if tupleType != nil && tupleType.Priv.TupleTypeArgs != nil {
			sourceEntryTypes := make([]Type, 0, len(tupleType.Priv.TupleTypeArgs))
			for _, t := range tupleType.Priv.TupleTypeArgs {
				sourceEntryTypes = append(sourceEntryTypes,
					AddConditionToType(t.Type, GetTypeCondition(subtype),
						&AddConditionOptions{SkipSelfCondition: true}))
			}

			unboundedIndex := -1
			for i, t := range tupleType.Priv.TupleTypeArgs {
				if t.IsUnbounded {
					unboundedIndex = i
					break
				}
			}

			if unboundedIndex >= 0 {
				if len(sourceEntryTypes) < len(targetTypes) {
					// The original's `sourceEntryTypes.length > 0 ? ... : AnyType`
					// ternary can only take its first arm: unboundedIndex >= 0
					// implies a non-empty list.
					typeToReplicate := sourceEntryTypes[unboundedIndex]

					// The original's comment: add elements to make the count match
					// the target count.
					for len(sourceEntryTypes) < len(targetTypes) {
						typeSliceInsert(&sourceEntryTypes, unboundedIndex, typeToReplicate)
					}
				}

				if len(sourceEntryTypes) > len(targetTypes) {
					// The original's comment: remove elements to make the count
					// match the target count.
					typeSliceSplice(&sourceEntryTypes, unboundedIndex, 1)
				}
			}

			// The original's comment: if there's an unpack operator in the target
			// and we have too many source elements, combine them to assign to the
			// unpacked target.
			if targetUnpackIndex >= 0 {
				if len(sourceEntryTypes) > len(targetTypes) {
					removedEntries := typeSliceSplice(&sourceEntryTypes, targetUnpackIndex,
						len(sourceEntryTypes)-len(targetTypes)+1)
					combinedTypes := CombineTypes(removedEntries, nil)
					if isListTarget {
						combinedTypes = e.StripLiteralValue(combinedTypes)
					}
					typeSliceInsert(&sourceEntryTypes, targetUnpackIndex, combinedTypes)
				} else if len(sourceEntryTypes) == len(targetTypes)-1 {
					typeSliceInsert(&sourceEntryTypes, targetUnpackIndex, NeverTypeCreateNever())
				}
			}

			for targetIndex, t := range sourceEntryTypes {
				if targetIndex < len(targetTypes) {
					targetTypes[targetIndex] = append(targetTypes[targetIndex], t)
				}
			}

			// The original's comment: have we accounted for all of the targets and
			// sources? If not, we have a size mismatch.
			if len(sourceEntryTypes) != len(targetExpressions) {
				subDiag := diagAddendum.CreateAddendum()

				mismatchMessage := localization.LocAddendum.TupleAssignmentMismatch().
					Format(e.PrintType(subtype, nil))
				if isListTarget {
					mismatchMessage = localization.LocAddendum.ListAssignmentMismatch().
						Format(e.PrintType(subtype, nil))
				}
				subDiag.AddMessage(mismatchMessage)

				expected := len(targetExpressions)
				sizeMessage := localization.LocAddendum.TupleSizeMismatch().
					Format(expected, len(sourceEntryTypes))
				if unpackIndex >= 0 {
					expected = len(targetExpressions) - 1
					sizeMessage = localization.LocAddendum.TupleSizeMismatchIndeterminateDest().
						Format(expected, len(sourceEntryTypes))
				}
				subDiag.CreateAddendum().AddMessage(sizeMessage)
			}

			return
		}

		// The original's comment: the assigned expression isn't a tuple, so it had
		// better be some iterable type.
		var iterableType Type = UnknownTypeCreate(false)
		if result := e.GetTypeOfIterator(
			&TypeResult{Type: subtype, IsIncomplete: typeResult.IsIncomplete},
			false, srcExpr, nil); result != nil {
			iterableType = result.Type
		}

		for index := 0; index < len(targetExpressions); index++ {
			targetTypes[index] = append(targetTypes[index],
				AddConditionToType(iterableType, GetTypeCondition(subtype), nil))
		}
	})

	if !diagAddendum.IsEmpty() {
		message := localization.LocMessage.TupleAssignmentMismatch().
			Format(e.PrintType(typeResult.Type, nil))
		if isListTarget {
			message = localization.LocMessage.ListAssignmentMismatch().
				Format(e.PrintType(typeResult.Type, nil))
		}

		e.AddDiagnostic(DiagnosticRuleReportAssignmentType,
			message+diagAddendum.GetString(), target, nil)
	}

	// The original's comment: assign the resulting types to the individual names
	// in the tuple or list target expression.
	for index, expr := range targetExpressions {
		typeList := targetTypes[index]

		var targetType Type = UnknownTypeCreate(false)
		if len(typeList) > 0 {
			targetType = CombineTypes(typeList, nil)
		}

		e.assignTypeToExpression(expr,
			&TypeResult{Type: targetType, IsIncomplete: typeResult.IsIncomplete},
			srcExpr, true, false, nil)
	}

	noneFlags := EvalFlagsNone
	e.writeTypeCache(target, typeResult, &noneFlags, nil, false)
}
