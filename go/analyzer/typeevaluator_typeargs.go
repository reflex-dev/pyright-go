/*
 * typeevaluator_typeargs.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/typeEvaluator.ts (pyright 1.1.412):
 * getTypeArgs and getTypeArg.
 *
 * Turning the subscripts of an index expression into type arguments. Almost all
 * of it is deciding which EvalFlags apply, because the answer differs per
 * argument: `Annotated[int, "meta"]` treats only the first as a type, a custom
 * __class_getitem__ treats none of them as types, and Final/ClassVar each
 * disallow the other unless the annotation is inside a dataclass body.
 *
 * Two structural details are load bearing:
 *
 *   - `Foo[int, str]` parses as a single subscript holding a Tuple, so the
 *     one-item-no-trailing-comma-tuple case is unpacked into separate arguments
 *     and the tuple node's own type is set to Unknown so it is not re-evaluated
 *     as a real tuple later.
 *   - adjFlags is REASSIGNED inside the per-argument closure for the
 *     custom-__class_getitem__ and Annotated cases, not shadowed -- so once one
 *     argument takes those paths, every later argument inherits the narrowed
 *     flags. That is observable for `Annotated[a, b, c]` and is preserved.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/localization"
	"github.com/microsoft/pyright/go/parser"
)

// getTypeArgs corresponds to the function of the same name.
func (e *typeEvaluator) getTypeArgs(
	node *parser.IndexNode,
	flags EvalFlags,
	options *getTypeArgsOptions,
) []*TypeResultWithNode {
	if options == nil {
		options = &getTypeArgsOptions{}
	}

	typeArgs := []*TypeResultWithNode{}
	adjFlags := flags | EvalFlagsNoConvertSpecialForm
	if (adjFlags & EvalFlagsTypeFormArg) != 0 {
		adjFlags |= EvalFlagsTypeExpression
	}

	// The original's comment: if the annotation is a variable within the body of
	// a dataclass, a Final is allowed with a ClassVar annotation. In all other
	// cases, it's disallowed.
	allowFinalClassVar := func() bool {
		if enclosingClassNode := GetEnclosingClass(node, true); enclosingClassNode != nil {
			if classTypeInfo := e.GetTypeOfClass(enclosingClassNode); classTypeInfo != nil &&
				classTypeInfo.ClassType != nil && ClassTypeIsDataClass(classTypeInfo.ClassType) {
				return true
			}
		}
		return false
	}

	switch {
	case options.IsFinalAnnotation:
		adjFlags |= EvalFlagsNoFinal
		if !allowFinalClassVar() {
			adjFlags |= EvalFlagsNoClassVar
		}

	case options.IsClassVarAnnotation:
		adjFlags |= EvalFlagsNoClassVar
		if !allowFinalClassVar() {
			adjFlags |= EvalFlagsNoFinal
		}

	default:
		adjFlags &^= EvalFlagsNoSpecialize |
			EvalFlagsNoParamSpec |
			EvalFlagsNoTypeVarTuple |
			EvalFlagsAllowRequired |
			EvalFlagsEnforceVarianceConsistency

		if !options.IsAnnotatedClass {
			adjFlags |= EvalFlagsNoClassVar | EvalFlagsNoFinal
		}

		adjFlags |= EvalFlagsAllowUnpackedTuple | EvalFlagsAllowConcatenate
	}

	// The original's comment: create a local function that validates a single
	// type argument. adjFlags is captured by reference and reassigned inside;
	// see the file header.
	getTypeArgTypeResult := func(expr parser.ExpressionNode, argIndex int) *TypeResultWithNode {
		// The original's comment: if it's a custom __class_getitem__, none of
		// the arguments should be treated as types.
		if options.HasCustomClassGetItem {
			adjFlags = EvalFlagsNoParamSpec | EvalFlagsNoTypeVarTuple | EvalFlagsNoSpecialize | EvalFlagsNoClassVar
			return &TypeResultWithNode{TypeResult: *e.getTypeOfExpression(expr, adjFlags, nil), Node: expr}
		}

		// The original's comment: if it's an Annotated[a, b, c], only the first
		// index should be treated as a type. The others can be regular
		// (non-type) objects.
		if options.IsAnnotatedClass && argIndex > 0 {
			adjFlags = EvalFlagsNoParamSpec | EvalFlagsNoTypeVarTuple | EvalFlagsNoSpecialize | EvalFlagsNoClassVar
			if IsAnnotationEvaluationPostponed(GetFileInfo(node)) {
				adjFlags |= EvalFlagsForwardRefs
			}

			return &TypeResultWithNode{TypeResult: *e.getTypeOfExpression(expr, adjFlags, nil), Node: expr}
		}

		return e.getTypeArg(expr, adjFlags, options.SupportsTypedDictTypeArg && argIndex == 0)
	}

	// The original's comment: a tuple is treated the same as a list of items in
	// the index.
	if len(node.D.Items) == 1 && !node.D.TrailingComma && node.D.Items[0].D.Name == nil {
		if tuple, ok := node.D.Items[0].D.ValueExpr.(*parser.TupleNode); ok {
			for index, item := range tuple.D.Items {
				typeArgs = append(typeArgs, getTypeArgTypeResult(item, index))
			}

			// The original's comment: set the node's type so it isn't
			// reevaluated later.
			e.SetTypeResultForNode(tuple, &TypeResult{Type: UnknownTypeCreate(false)}, EvalFlagsNone)

			return typeArgs
		}
	}

	for index, arg := range node.D.Items {
		typeResult := getTypeArgTypeResult(arg.D.ValueExpr, index)

		if arg.D.ArgCategory == parser.ArgCategoryUnpackedList {
			if !options.IsAnnotatedClass || index == 0 {
				if unpackedType := e.applyUnpackToTupleLike(typeResult.Type); unpackedType != nil {
					typeResult.Type = unpackedType
				} else if (flags & EvalFlagsTypeExpression) != 0 {
					e.AddDiagnostic(
						DiagnosticRuleReportInvalidTypeForm,
						localization.LocMessage.UnpackNotAllowed(),
						arg.D.ValueExpr,
						nil,
					)
					typeResult.TypeErrors = true
				} else {
					typeResult.Type = UnknownTypeCreate(false)
				}
			}
		}

		if arg.D.Name != nil {
			if (flags & EvalFlagsTypeExpression) != 0 {
				e.AddDiagnostic(
					DiagnosticRuleReportInvalidTypeForm,
					localization.LocMessage.KeywordArgInTypeArgument(),
					arg.D.ValueExpr,
					nil,
				)
				typeResult.TypeErrors = true
			} else {
				typeResult.Type = UnknownTypeCreate(false)
			}
		}

		// The original skips an ErrorNode whose category is MissingIndexOrSlice
		// -- `Foo[]` -- so it does not become a bogus type argument.
		if errorNode, ok := arg.D.ValueExpr.(*parser.ErrorNode); ok &&
			errorNode.D.Category == parser.ErrorExpressionCategoryMissingIndexOrSlice {
			continue
		}

		typeArgs = append(typeArgs, typeResult)
	}

	return typeArgs
}

// getTypeArg corresponds to the function of the same name: one subscript
// evaluated as a type argument.
func (e *typeEvaluator) getTypeArg(
	node parser.ExpressionNode,
	flags EvalFlags,
	supportsDictExpression bool,
) *TypeResultWithNode {
	adjustedFlags := flags | EvalFlagsInstantiableType | EvalFlagsConvertEllipsisToAny | EvalFlagsStrLiteralAsType

	fileInfo := GetFileInfo(node)
	if fileInfo.IsStubFile {
		adjustedFlags |= EvalFlagsForwardRefs
	}

	if listNode, ok := node.(*parser.ListNode); ok {
		typeList := make([]*TypeResultWithNode, 0, len(listNode.D.Items))
		for _, entry := range listNode.D.Items {
			typeList = append(typeList, &TypeResultWithNode{
				TypeResult: *e.getTypeOfExpression(entry, adjustedFlags, nil),
				Node:       entry,
			})
		}

		// The original's comment: set the node's type so it isn't reevaluated
		// later.
		e.SetTypeResultForNode(node, &TypeResult{Type: UnknownTypeCreate(false)}, EvalFlagsNone)

		return &TypeResultWithNode{
			TypeResult: TypeResult{
				Type:            UnknownTypeCreate(false),
				TypeList:        typeList,
				TypeListPresent: true,
			},
			Node: node,
		}
	}

	if dictNode, ok := node.(*parser.DictionaryNode); ok && supportsDictExpression {
		var inlinedTypeDict *ClassType
		if e.prefetched != nil && e.prefetched.TypedDictClass != nil &&
			IsInstantiableClass(e.prefetched.TypedDictClass) {
			inlinedTypeDict = e.createTypedDictTypeInlined(dictNode, e.prefetched.TypedDictClass.(*ClassType))
		}

		var keyTypeFallback Type = UnknownTypeCreate(false)
		if e.prefetched != nil && e.prefetched.StrClass != nil && IsInstantiableClass(e.prefetched.StrClass) {
			keyTypeFallback = e.prefetched.StrClass
		}

		return &TypeResultWithNode{
			TypeResult: TypeResult{Type: keyTypeFallback, InlinedTypeDict: inlinedTypeDict},
			Node:       node,
		}
	}

	typeResult := &TypeResultWithNode{
		TypeResult: *e.getTypeOfExpression(node, adjustedFlags, nil),
		Node:       node,
	}

	if node.GetNodeType() == parser.ParseNodeTypeDictionary {
		e.AddDiagnostic(DiagnosticRuleReportInvalidTypeForm, localization.LocMessage.DictInAnnotation(), node, nil)
	}

	if (flags & EvalFlagsNoClassVar) != 0 {
		// The original's comment: "ClassVar" is not allowed as a type argument.
		if IsClass(typeResult.Type) && ClassTypeIsBuiltInNamed(typeResult.Type.(*ClassType), "ClassVar") {
			e.AddDiagnostic(DiagnosticRuleReportInvalidTypeForm, localization.LocMessage.ClassVarNotAllowed(), node, nil)
		}
	}

	return typeResult
}

/*
 * The two satellites this reaches.
 */

// applyUnpackToTupleLike corresponds to the function of the same name. It
// returns nil where the original returns undefined, meaning "this type cannot be
// unpacked".
func (e *typeEvaluator) applyUnpackToTupleLike(_ Type) Type {
	e.unported("applyUnpackToTupleLike")
	return nil
}

// createTypedDictTypeInlined corresponds to the typedDicts.ts function of the
// same name, which builds a TypedDict from an inline `{...}` type argument.
func (e *typeEvaluator) createTypedDictTypeInlined(_ *parser.DictionaryNode, _ *ClassType) *ClassType {
	e.unported("createTypedDictTypeInlined")
	return nil
}
