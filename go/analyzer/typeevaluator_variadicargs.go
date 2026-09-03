/*
 * typeevaluator_variadicargs.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/typeEvaluator.ts (pyright 1.1.412):
 * adjustTypeArgsForTypeVarTuple and validateTypeVarTupleIsUnpacked.
 *
 * The original's comment: if the list of type parameters includes a
 * TypeVarTuple, we may need to adjust the supplied type arguments to map to the
 * type parameter list.
 *
 * Two distinct adjustments happen here, in order. First, if one of the supplied
 * arguments is an unbounded `*tuple[T, ...]` and there are fewer arguments than
 * parameters, that tuple's element type is "smeared" across the empty slots on
 * both sides of the TypeVarTuple -- so `Foo[int, *tuple[str, ...]]` against
 * `Foo[A, B, C]` fills B and C with str. Second, the arguments that land on the
 * TypeVarTuple itself are collected into a single unpacked tuple object, unless
 * they are a lone TypeVarTuple, which passes through.
 *
 * The trailing-ParamSpec exclusion in the middle is easy to miss and changes
 * which arguments the TypeVarTuple claims: a type parameter list ending in a
 * defaulted ParamSpec does not count that parameter when working out how many
 * arguments belong to the TypeVarTuple, and trailing arguments that are type
 * LISTS are ParamSpec arguments rather than TypeVarTuple ones.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/localization"
	"github.com/microsoft/pyright/go/parser"
)

// adjustTypeArgsForTypeVarTuple corresponds to the function of the same name.
func (e *typeEvaluator) adjustTypeArgsForTypeVarTuple(
	typeArgs []*TypeResultWithNode,
	typeParams []*TypeVarType,
	errorNode parser.ExpressionNode,
) []*TypeResultWithNode {
	variadicIndex := -1
	for i, param := range typeParams {
		if IsTypeVarTuple(param) {
			variadicIndex = i
			break
		}
	}

	// The original's comment: is there a *tuple[T, ...] somewhere in the type
	// arguments that we can expand if needed? The closure assigns to
	// srcUnboundedTupleType as a side effect of the search, which is why it is
	// declared outside.
	var srcUnboundedTupleType Type
	findUnboundedTupleIndex := func(startArgIndex int) int {
		for index, arg := range typeArgs {
			if index < startArgIndex {
				continue
			}
			if IsUnpackedClass(arg.Type) {
				tupleTypeArgs := arg.Type.(*ClassType).Priv.TupleTypeArgs
				if len(tupleTypeArgs) == 1 && tupleTypeArgs[0].IsUnbounded {
					srcUnboundedTupleType = tupleTypeArgs[0].Type
					return index
				}
			}
		}
		return -1
	}
	srcUnboundedTupleIndex := findUnboundedTupleIndex(0)

	// The original's comment: allow only one unpacked tuple that maps to a
	// TypeVarTuple.
	if srcUnboundedTupleIndex >= 0 {
		if secondUnboundedTupleIndex := findUnboundedTupleIndex(srcUnboundedTupleIndex + 1); secondUnboundedTupleIndex >= 0 {
			e.AddDiagnostic(
				DiagnosticRuleReportInvalidTypeForm,
				localization.LocMessage.VariadicTypeArgsTooMany(),
				typeArgs[secondUnboundedTupleIndex].Node,
				nil,
			)
		}
	}

	if srcUnboundedTupleType != nil && srcUnboundedTupleIndex >= 0 &&
		variadicIndex >= 0 && len(typeArgs) < len(typeParams) {
		// The original's comment: "smear" the tuple type across type argument
		// slots prior to the TypeVarTuple.
		for variadicIndex > srcUnboundedTupleIndex {
			inserted := &TypeResultWithNode{
				TypeResult: TypeResult{Type: srcUnboundedTupleType},
				Node:       typeArgs[srcUnboundedTupleIndex].Node,
			}
			typeArgs = insertTypeArg(typeArgs, srcUnboundedTupleIndex, inserted)
			srcUnboundedTupleIndex++
		}

		// The original's comment: "smear" the tuple type across type argument
		// slots following the TypeVarTuple.
		for len(typeArgs) < len(typeParams) {
			inserted := &TypeResultWithNode{
				TypeResult: TypeResult{Type: srcUnboundedTupleType},
				Node:       typeArgs[srcUnboundedTupleIndex].Node,
			}
			typeArgs = insertTypeArg(typeArgs, srcUnboundedTupleIndex+1, inserted)
		}
	}

	// The original's comment: do we need to adjust the type arguments to map to
	// a variadic type param somewhere in the list?
	if variadicIndex < 0 {
		return typeArgs
	}

	variadicTypeVar := typeParams[variadicIndex]

	// The original's comment: if the type param list ends with a ParamSpec with
	// a default value, we can ignore it for purposes of finding type args that
	// map to the TypeVarTuple.
	typeParamCount := len(typeParams)
	for typeParamCount > 0 {
		lastTypeParam := typeParams[typeParamCount-1]
		if !IsParamSpec(lastTypeParam) || !lastTypeParam.Shared.IsDefaultExplicit {
			break
		}
		typeParamCount--
	}

	if variadicIndex >= len(typeArgs) {
		if !variadicTypeVar.Shared.IsDefaultExplicit {
			// The original's comment: add an empty tuple that maps to the
			// TypeVarTuple type parameter.
			//
			// The empty slice is not interchangeable with nil here: ClassType
			// .specialize stores tupleTypeArgs only when it is defined, and a nil
			// slice reads as absent. A tuple with no tupleTypeArgs is not an
			// empty tuple -- it is a tuple whose element types are unknown, and
			// it prints as `tuple[Never]` rather than `tuple[()]`.
			typeArgs = append(typeArgs, &TypeResultWithNode{
				TypeResult: TypeResult{Type: MakeTupleObject(e, []*TupleTypeArg{}, true)},
				Node:       errorNode,
			})
		}
		return typeArgs
	}

	// The original's comment: if there are typeArg lists at the end, these
	// should map to ParamSpecs rather than the TypeVarTuple, so exclude them.
	variadicEndIndex := variadicIndex + 1 + len(typeArgs) - typeParamCount
	for variadicEndIndex > variadicIndex {
		if !typeArgs[variadicEndIndex-1].TypeListPresent {
			break
		}
		variadicEndIndex--
	}
	// `typeArgs.slice(variadicIndex, variadicEndIndex)`. variadicEndIndex can
	// fall below variadicIndex when there are fewer type arguments than type
	// parameters; Array.prototype.slice answers an empty array there, while a Go
	// slice expression panics, so the bounds are clamped the way slice does.
	variadicTypeResults := sliceTypeArgs(typeArgs, variadicIndex, variadicEndIndex)

	// The original's comment: if the type args consist of a lone TypeVarTuple,
	// don't wrap it in a tuple.
	if len(variadicTypeResults) == 1 && IsTypeVarTuple(variadicTypeResults[0].Type) {
		e.validateTypeVarTupleIsUnpacked(variadicTypeResults[0].Type.(*TypeVarType), variadicTypeResults[0].Node)
		return typeArgs
	}

	for index, arg := range variadicTypeResults {
		e.ValidateTypeArg(arg, &ValidateTypeArgsOptions{
			AllowEmptyTuple:     index == 0,
			AllowTypeVarTuple:   true,
			AllowUnpackedTuples: true,
		})
	}

	variadicTypes := []*TupleTypeArg{}
	if len(variadicTypeResults) != 1 || !variadicTypeResults[0].IsEmptyTupleShorthand {
		for _, typeResult := range variadicTypeResults {
			if IsUnpackedClass(typeResult.Type) && typeResult.Type.(*ClassType).Priv.TupleTypeArgs != nil {
				variadicTypes = append(variadicTypes, typeResult.Type.(*ClassType).Priv.TupleTypeArgs...)
			} else {
				variadicTypes = append(variadicTypes, &TupleTypeArg{
					Type:        ConvertToInstance(typeResult.Type, true),
					IsUnbounded: false,
				})
			}
		}
	}

	tupleObject := MakeTupleObject(e, variadicTypes, true)

	replaced := make([]*TypeResultWithNode, 0, variadicIndex+1+len(typeArgs)-variadicEndIndex)
	replaced = append(replaced, sliceTypeArgs(typeArgs, 0, variadicIndex)...)
	replaced = append(replaced, &TypeResultWithNode{
		TypeResult: TypeResult{Type: tupleObject},
		Node:       typeArgs[variadicIndex].Node,
	})
	// `typeArgs.slice(variadicEndIndex, typeArgs.length)` -- a negative
	// variadicEndIndex counts back from the end here rather than meaning zero.
	replaced = append(replaced, sliceTypeArgs(typeArgs, variadicEndIndex, len(typeArgs))...)

	return replaced
}

// insertTypeArg is the original's `[...slice(0, i), item, ...slice(i)]`, which
// builds a fresh array rather than splicing in place. The copy matters: typeArgs
// entries are shared with the caller's slice.
func insertTypeArg(typeArgs []*TypeResultWithNode, index int, item *TypeResultWithNode) []*TypeResultWithNode {
	out := make([]*TypeResultWithNode, 0, len(typeArgs)+1)
	out = append(out, typeArgs[:index]...)
	out = append(out, item)
	out = append(out, typeArgs[index:]...)
	return out
}

// validateTypeVarTupleIsUnpacked corresponds to the function of the same name.
// The original's comment: if the variadic type variable is not unpacked, report
// an error.
func (e *typeEvaluator) validateTypeVarTupleIsUnpacked(t *TypeVarType, node parser.ParseNode) bool {
	if !t.Priv.IsUnpacked {
		e.AddDiagnostic(
			DiagnosticRuleReportInvalidTypeForm,
			localization.LocMessage.UnpackedTypeVarTupleExpected().Format(t.Shared.Name, t.Shared.Name),
			node,
			nil,
		)
		return false
	}

	return true
}

// sliceTypeArgs reproduces Array.prototype.slice over a type argument list.
// Go's slice expression is not a substitute: a negative index counts back from
// the end rather than being an error, an out-of-range index is clamped, and an
// end below the start yields an empty result instead of a panic. All three
// cases occur here, because variadicEndIndex is arithmetic on two lengths that
// can legitimately disagree.
func sliceTypeArgs(typeArgs []*TypeResultWithNode, start int, end int) []*TypeResultWithNode {
	n := len(typeArgs)

	resolve := func(index int) int {
		if index < 0 {
			index += n
			if index < 0 {
				return 0
			}
			return index
		}
		if index > n {
			return n
		}
		return index
	}

	start = resolve(start)
	end = resolve(end)
	if end < start {
		end = start
	}
	return typeArgs[start:end]
}
