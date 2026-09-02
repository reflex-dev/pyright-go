/*
 * typeevaluator_indexobject.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/typeEvaluator.ts (pyright 1.1.412):
 * getIndexAccessMagicMethodName and getTypeOfIndexedObjectOrClass, plus the
 * tuples.ts helpers getSlicedTupleType and getTupleSliceParam that the latter
 * reaches.
 *
 * `x[i]` at runtime is a call to `__getitem__`, and that is ultimately how this
 * evaluates: bind the magic method, package the subscript into an argument
 * list, and validate the call. Everything before that is precision work laid
 * over the general mechanism.
 *
 * Two shortcuts come first, both for tuples, because `__getitem__` on a tuple is
 * declared to return a union of every element type and that answer is far weaker
 * than what a literal subscript permits. A literal integer index reads the exact
 * element (isTupleIndexUnambiguous is what refuses when an unbounded entry makes
 * the position uncertain); a literal slice produces a shorter tuple.
 *
 * The argument packaging is the other half. A single positional subscript is
 * passed through as-is, but `x[a, b]` and `x[*rest]` become one tuple argument,
 * which is why an unpacked list is first asked for deterministic entries -- a
 * union of same-length tuples still yields a known arity, element-wise unioned --
 * and only falls back to a single unbounded entry when it does not. The final
 * fold of multiple unbounded entries into one preserves the type-model invariant
 * that a tuple carries at most one.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/localization"
	"github.com/microsoft/pyright/go/parser"
)

// getIndexAccessMagicMethodName corresponds to the function of the same name.
func (e *typeEvaluator) getIndexAccessMagicMethodName(usage *EvaluatorUsage) string {
	switch usage.Method {
	case "get":
		return "__getitem__"
	case "set":
		return "__setitem__"
	default:
		// The original asserts usage.method === 'del' here.
		return "__delitem__"
	}
}

// GetSlicedTupleType corresponds to the tuples.ts function of the same name:
// the tuple that results from a slice whose bounds are known literals, or nil
// when they are not.
func GetSlicedTupleType(
	evaluator TypeEvaluator, tupleType *ClassType, sliceNode *parser.SliceNode,
) Type {
	// The original's comment: we don't handle step values.
	if sliceNode.D.StepValue != nil || tupleType.Priv.TupleTypeArgs == nil {
		return nil
	}

	tupleTypeArgs := tupleType.Priv.TupleTypeArgs
	startValue, startOK := getTupleSliceParam(evaluator, sliceNode.D.StartValue, 0, tupleTypeArgs)
	endValue, endOK := getTupleSliceParam(
		evaluator, sliceNode.D.EndValue, len(tupleTypeArgs), tupleTypeArgs)

	if !startOK || !endOK || endValue < startValue {
		return nil
	}

	slicedTypeArgs := tupleTypeArgs[startValue:endValue]
	return ClassTypeCloneAsInstance(SpecializeTupleClass(tupleType, slicedTypeArgs, true, false), true)
}

// getTupleSliceParam corresponds to the function of the same name. The second
// result stands in for the original's `undefined`, which means "not a statically
// known index".
func getTupleSliceParam(
	evaluator TypeEvaluator,
	expression parser.ExpressionNode,
	defaultValue int,
	tupleTypeArgs []*TupleTypeArg,
) (int, bool) {
	value := defaultValue

	if expression == nil {
		return value, true
	}

	valType := evaluator.GetTypeOfExpression(expression, EvalFlagsNone, nil).Type
	if !IsClassInstance(valType) || !ClassTypeIsBuiltInNamed(valType.(*ClassType), "int") ||
		!IsLiteralType(valType.(*ClassType)) {
		return 0, false
	}

	// The original writes `literalValue as number`, an unchecked cast. Pyright
	// stores an int literal as `number | bigint`, and the `number` arm is
	// LiteralFloat here; a bigint would make the arithmetic below throw upstream,
	// so refusing the slice is the closest faithful answer.
	literal, ok := valType.(*ClassType).Priv.LiteralValue.(LiteralFloat)
	if !ok {
		return 0, false
	}
	value = int(literal)

	unboundedIndex := -1
	for i, typeArg := range tupleTypeArgs {
		if typeArg.IsUnbounded || IsTypeVarTuple(typeArg.Type) {
			unboundedIndex = i
			break
		}
	}

	if value < 0 {
		value = len(tupleTypeArgs) + value
		if unboundedIndex >= 0 && value <= unboundedIndex {
			return 0, false
		} else if value < 0 {
			return 0, true
		}
	} else {
		if unboundedIndex >= 0 && value > unboundedIndex {
			return 0, false
		} else if value > len(tupleTypeArgs) {
			return len(tupleTypeArgs), true
		}
	}

	return value, true
}

// getTypeOfIndexedObjectOrClass corresponds to the function of the same name:
// the subscript side of the fork, which goes to __getitem__ and friends.
func (e *typeEvaluator) getTypeOfIndexedObjectOrClass(
	node *parser.IndexNode,
	baseType *ClassType,
	selfType Type,
	usage *EvaluatorUsage,
) *TypeResult {
	// The original's comment: handle index operations for TypedDict classes
	// specially.
	if IsClassInstance(baseType) && ClassTypeIsTypedDictClass(baseType) {
		if typeFromTypedDict := e.getTypeOfIndexedTypedDict(node, baseType, usage); typeFromTypedDict != nil {
			return typeFromTypedDict
		}
	}

	magicMethodName := e.getIndexAccessMagicMethodName(usage)
	itemMethodType := e.GetBoundMagicMethod(baseType, magicMethodName, selfType, node.D.LeftExpr, nil, 0)

	if itemMethodType == nil {
		e.AddDiagnostic(DiagnosticRuleReportIndexIssue,
			localization.LocMessage.MethodNotDefinedOnType().Format(
				magicMethodName, e.PrintType(baseType, nil)),
			node.D.LeftExpr, nil)
		return &TypeResult{Type: UnknownTypeCreate(false)}
	}

	// The original's comment: handle the special case where the object is a tuple
	// and the index is a constant number (integer) or a slice with integer start
	// and end values. In these cases, we can determine the exact type by indexing
	// into the tuple type array.
	if len(node.D.Items) == 1 && !node.D.TrailingComma && node.D.Items[0].D.Name == nil &&
		node.D.Items[0].D.ArgCategory == parser.ArgCategorySimple && IsClassInstance(baseType) {
		index0Expr := node.D.Items[0].D.ValueExpr
		valueType := e.getTypeOfExpression(index0Expr, EvalFlagsNone, nil).Type

		if IsClassInstance(valueType) && ClassTypeIsBuiltInNamed(valueType.(*ClassType), "int") &&
			IsLiteralType(valueType.(*ClassType)) {
			// `typeof valueType.priv.literalValue === 'number'` -- the `number`
			// arm of pyright's `number | bigint` int literal, which is
			// LiteralFloat here.
			if literal, ok := valueType.(*ClassType).Priv.LiteralValue.(LiteralFloat); ok {
				indexValue := int(literal)
				tupleType := GetSpecializedTupleType(baseType)

				if tupleType != nil && tupleType.Priv.TupleTypeArgs != nil {
					args := tupleType.Priv.TupleTypeArgs
					if IsTupleIndexUnambiguous(tupleType, indexValue) {
						if indexValue >= 0 && indexValue < len(args) {
							return &TypeResult{Type: args[indexValue].Type}
						} else if indexValue < 0 && len(args)+indexValue >= 0 {
							return &TypeResult{Type: args[len(args)+indexValue].Type}
						}
					}
				}
			}
		} else if IsClassInstance(valueType) && ClassTypeIsBuiltInNamed(valueType.(*ClassType), "slice") {
			tupleType := GetSpecializedTupleType(baseType)

			if sliceNode, ok := index0Expr.(*parser.SliceNode); ok && tupleType != nil {
				if slicedTupleType := GetSlicedTupleType(e, tupleType, sliceNode); slicedTupleType != nil {
					return &TypeResult{Type: slicedTupleType}
				}
			}
		}
	}

	positionalArgCount := 0
	unpackedListArgCount := 0
	for _, item := range node.D.Items {
		switch item.D.ArgCategory {
		case parser.ArgCategorySimple:
			positionalArgCount++
		case parser.ArgCategoryUnpackedList:
			unpackedListArgCount++
		}
	}

	var positionalIndexType Type
	isPositionalIndexTypeIncomplete := false

	if positionalArgCount == 1 && unpackedListArgCount == 0 && !node.D.TrailingComma {
		// The original's comment: handle the common case where there is a single
		// positional argument.
		var firstPositional *parser.ArgumentNode
		for _, item := range node.D.Items {
			if item.D.ArgCategory == parser.ArgCategorySimple {
				firstPositional = item
				break
			}
		}

		typeResult := e.getTypeOfExpression(firstPositional.D.ValueExpr, EvalFlagsNone, nil)
		positionalIndexType = typeResult.Type
		if typeResult.IsIncomplete {
			isPositionalIndexTypeIncomplete = true
		}
	} else {
		// The original's comment: package up all of the positionals into a tuple.
		tupleTypeArgs := []*TupleTypeArg{}

		for _, arg := range node.D.Items {
			switch arg.D.ArgCategory {
			case parser.ArgCategorySimple:
				typeResult := e.getTypeOfExpression(arg.D.ValueExpr, EvalFlagsNone, nil)
				tupleTypeArgs = append(tupleTypeArgs,
					&TupleTypeArg{Type: typeResult.Type, IsUnbounded: false})
				if typeResult.IsIncomplete {
					isPositionalIndexTypeIncomplete = true
				}

			case parser.ArgCategoryUnpackedList:
				typeResult := e.getTypeOfExpression(arg.D.ValueExpr, EvalFlagsNone, nil)
				if typeResult.IsIncomplete {
					isPositionalIndexTypeIncomplete = true
				}

				if entries := getDeterministicTupleEntries(typeResult.Type); entries != nil {
					tupleTypeArgs = append(tupleTypeArgs, entries...)
					continue
				}

				var iterableType Type = UnknownTypeCreate(false)
				if result := e.GetTypeOfIterator(typeResult, false, arg.D.ValueExpr, nil); result != nil {
					iterableType = result.Type
				}
				tupleTypeArgs = append(tupleTypeArgs,
					&TupleTypeArg{Type: iterableType, IsUnbounded: true})
			}
		}

		unboundedCount := 0
		firstUnboundedIndex := -1
		for i, typeArg := range tupleTypeArgs {
			if typeArg.IsUnbounded {
				unboundedCount++
				if firstUnboundedIndex < 0 {
					firstUnboundedIndex = i
				}
			}
		}

		if unboundedCount > 1 {
			removedEntries := tupleTypeArgs[firstUnboundedIndex:]
			removedTypes := make([]Type, 0, len(removedEntries))
			for _, entry := range removedEntries {
				removedTypes = append(removedTypes, entry.Type)
			}
			tupleTypeArgs = append(tupleTypeArgs[:firstUnboundedIndex],
				&TupleTypeArg{Type: CombineTypes(removedTypes, nil), IsUnbounded: true})
		}

		positionalIndexType = MakeTupleObject(e, tupleTypeArgs, false)
	}

	argList := []*Arg{
		{
			ArgCategory: parser.ArgCategorySimple,
			TypeResult: &TypeResult{
				Type:         positionalIndexType,
				IsIncomplete: isPositionalIndexTypeIncomplete,
			},
		},
	}

	if usage.Method == "set" {
		var setType Type = AnyTypeCreate(false)
		if usage.SetType != nil && usage.SetType.Type != nil {
			setType = usage.SetType.Type
		}

		// The original's comment: expand constrained type variables.
		if IsTypeVar(setType) && TypeVarTypeHasConstraints(setType.(*TypeVarType)) {
			var conditionFilter []TypeCondition
			if IsClassInstance(baseType) {
				conditionFilter = propsCondition(baseType)
			}
			setType = e.makeTopLevelTypeVarsConcrete(setType, false, conditionFilter)
		}

		argList = append(argList, &Arg{
			ArgCategory: parser.ArgCategorySimple,
			TypeResult: &TypeResult{
				Type:         setType,
				IsIncomplete: usage.SetType != nil && usage.SetType.IsIncomplete,
			},
		})
	}

	callResult := e.validateCallArgs(
		node, argList, &TypeResult{Type: itemMethodType}, nil, true, nil, 0)

	resultType := callResult.ReturnType
	if resultType == nil {
		resultType = UnknownTypeCreate(false)
	}

	return &TypeResult{Type: resultType, IsIncomplete: callResult.IsTypeIncomplete}
}

// getDeterministicTupleEntries corresponds to the local function of the same
// name: the tuple entries a type contributes when every subtype is a
// fixed-length tuple of the same length, or nil when any subtype makes the arity
// uncertain. Same-length subtypes are unioned element-wise.
func getDeterministicTupleEntries(t Type) []*TupleTypeArg {
	var aggregatedArgs []*TupleTypeArg
	isDeterministic := true

	DoForEachSubtype(t, func(subtype Type, _ int, _ []Type) {
		if !isDeterministic {
			return
		}

		tupleType := GetSpecializedTupleType(subtype)
		var tupleTypeArgs []*TupleTypeArg
		if tupleType != nil {
			tupleTypeArgs = tupleType.Priv.TupleTypeArgs
		}

		if tupleTypeArgs == nil {
			isDeterministic = false
			return
		}
		for _, entry := range tupleTypeArgs {
			if entry.IsUnbounded || IsTypeVarTuple(entry.Type) {
				isDeterministic = false
				return
			}
		}

		if aggregatedArgs == nil {
			aggregatedArgs = make([]*TupleTypeArg, 0, len(tupleTypeArgs))
			for _, entry := range tupleTypeArgs {
				aggregatedArgs = append(aggregatedArgs,
					&TupleTypeArg{Type: entry.Type, IsUnbounded: false})
			}
			return
		}

		if len(aggregatedArgs) != len(tupleTypeArgs) {
			isDeterministic = false
			return
		}

		for i := range aggregatedArgs {
			aggregatedArgs[i] = &TupleTypeArg{
				Type:        CombineTypes([]Type{aggregatedArgs[i].Type, tupleTypeArgs[i].Type}, nil),
				IsUnbounded: false,
			}
		}
	})

	if !isDeterministic {
		return nil
	}

	return aggregatedArgs
}
