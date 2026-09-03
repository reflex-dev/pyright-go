/*
 * typeevaluator_signatureinfo.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/typeEvaluator.ts (pyright 1.1.412):
 * getCallSignatureInfo and expandTypedKwargs, plus the printControlFlowGraph
 * passthrough.
 *
 * getCallSignatureInfo backs signature help: given a call and a cursor position,
 * which signatures apply and which parameter is the cursor in. It is the one
 * evaluator entry point designed to run on *incomplete* code, and that shapes it.
 *
 * The fake-argument insertion is the clearest example. The original's comment
 * says it: an empty argument slot -- `f(1, |)` with the cursor after the comma --
 * produces no AST node at all, so there is nothing to match a parameter against.
 * A synthetic Unknown argument is inserted at the active index so the matching
 * has something to bind, which is what lets signature help highlight the
 * parameter the user is about to type into.
 *
 * The re-run against the specialized type is not redundant. validateArgs reports
 * activeParam as a parameter of the type it was given, so after ParamSpec
 * expansion that index refers to the *unspecialized* signature and points at the
 * wrong parameter. Running it again against the specialized type is what makes
 * the highlight land correctly.
 *
 * The constructor filter is a UI judgment the original states plainly: `__new__`
 * or `__init__` is frequently just `(*args: Any, **kwargs: Any)`, and showing
 * that alongside a real signature is noise. It is dropped only when a better
 * signature exists, and never when it carries a docstring or deprecation notice.
 *
 * expandTypedKwargs turns `**kwargs: Unpack[TD]` back into the individual
 * keyword parameters TD declares, because signature help should show the actual
 * accepted keywords rather than an opaque **kwargs.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/parser"
)

// GetCallSignatureInfo corresponds to getCallSignatureInfo. It returns nil where
// the original returns undefined.
func (e *typeEvaluator) GetCallSignatureInfo(
	callNode *parser.CallNode, activeIndex int, activeOrFake bool,
) *CallSignatureInfo {
	exprNode := callNode.D.LeftExpr
	callType := e.GetType(exprNode)
	if IsNilType(callType) {
		return nil
	}

	argList := buildSignatureHelpArgList(callNode, activeIndex, activeOrFake)

	b := &signatureInfoBuilder{
		evaluator: e,
		callNode:  callNode,
		exprNode:  exprNode,
		argList:   argList,
	}

	DoForEachSubtype(callType, func(subtype Type, _ int, _ []Type) {
		switch subtype.Base().Category {
		case TypeCategoryFunction, TypeCategoryOverloaded:
			b.addFunctionToSignature(subtype)

		case TypeCategoryClass:
			b.addClassToSignature(subtype.(*ClassType))
		}
	})

	if len(b.signatures) == 0 {
		return nil
	}

	return &CallSignatureInfo{CallNode: callNode, Signatures: b.signatures}
}

// buildSignatureHelpArgList is the original's argument-collection block,
// including the synthetic arguments described in the file header.
func buildSignatureHelpArgList(
	callNode *parser.CallNode, activeIndex int, activeOrFake bool,
) []*Arg {
	argList := []*Arg{}
	previousCategory := parser.ArgCategorySimple

	// The original's comment: empty arguments do not enter the AST as nodes, but
	// instead are left blank. Instead, we detect when we appear to be between two
	// known arguments or at the end of the argument list and insert a fake argument
	// of an unknown type to have something to match later.
	addFakeArg := func() {
		argList = append(argList, &Arg{
			ArgCategory: previousCategory,
			TypeResult:  &TypeResult{Type: UnknownTypeCreate(false)},
			Active:      true,
		})
	}

	for index, arg := range callNode.D.Args {
		active := false
		if index == activeIndex {
			if activeOrFake {
				active = true
			} else {
				addFakeArg()
			}
		}

		previousCategory = arg.D.ArgCategory

		argList = append(argList, &Arg{
			ValueExpression: arg.D.ValueExpr,
			ArgCategory:     arg.D.ArgCategory,
			Name:            arg.D.Name,
			Active:          active,
		})
	}

	if len(callNode.D.Args) < activeIndex {
		addFakeArg()
	}

	return argList
}

// signatureInfoBuilder carries what the original's nested functions close over.
type signatureInfoBuilder struct {
	evaluator  *typeEvaluator
	callNode   *parser.CallNode
	exprNode   parser.ExpressionNode
	argList    []*Arg
	signatures []*CallSignature
}

// addOneFunctionToSignature corresponds to the local function of the same name.
func (b *signatureInfoBuilder) addOneFunctionToSignature(t *FunctionType) {
	var callResult *CallResult
	constraints := NewConstraintTracker()

	b.evaluator.UseSpeculativeMode(b.callNode, func() {
		callResult = b.evaluator.validateArgs(b.exprNode, b.argList, &TypeResult{Type: t},
			constraints, true, nil)
	}, nil)

	specializedType := b.evaluator.SolveAndApplyConstraints(t, constraints, nil, nil)
	finalType := t
	if IsFunction(specializedType) {
		finalType = specializedType.(*FunctionType)
	}

	hasActiveArg := false
	for _, arg := range b.argList {
		if arg.Active {
			hasActiveArg = true
			break
		}
	}

	// The original's comment: if the type was specialized (e.g. ParamSpec
	// expansion), the activeParam from the original validateArgs refers to
	// parameters in the unspecialized type and won't match the specialized type's
	// parameters. Re-run validateArgs against the specialized type to get the
	// correct activeParam mapping.
	var activeParam *FunctionParam
	if callResult != nil {
		activeParam = callResult.ActiveParam
	}
	if hasActiveArg && finalType != t {
		var specializedActiveParam *FunctionParam
		b.evaluator.UseSpeculativeMode(b.callNode, func() {
			specializedResult := b.evaluator.validateArgs(b.exprNode, b.argList,
				&TypeResult{Type: finalType}, NewConstraintTracker(), true, nil)
			if specializedResult != nil {
				specializedActiveParam = specializedResult.ActiveParam
			}
		}, nil)
		if specializedActiveParam != nil {
			activeParam = specializedActiveParam
		}
	}

	b.signatures = append(b.signatures, &CallSignature{
		Type:        expandTypedKwargs(finalType),
		ActiveParam: activeParam,
	})
}

// addFunctionToSignature corresponds to the local function of the same name.
func (b *signatureInfoBuilder) addFunctionToSignature(t Type) {
	if IsFunction(t) {
		b.addOneFunctionToSignature(t.(*FunctionType))
		return
	}
	if IsOverloaded(t) {
		for _, fn := range OverloadedTypeGetOverloads(t.(*OverloadedType)) {
			b.addOneFunctionToSignature(fn)
		}
	}
}

// addClassToSignature is the original's TypeCategory.Class arm.
func (b *signatureInfoBuilder) addClassToSignature(subtype *ClassType) {
	if !subtype.Base().IsInstantiable() {
		if methodType := b.evaluator.GetBoundMagicMethod(subtype, "__call__", nil, nil, nil, 0); methodType != nil {
			b.addFunctionToSignature(methodType)
		}
		return
	}

	constructorType := CreateFunctionFromConstructor(b.evaluator, subtype, nil, 0)
	if constructorType == nil {
		return
	}

	DoForEachSubtype(constructorType, func(ctorSubtype Type, _ int, _ []Type) {
		if IsFunctionOrOverloaded(ctorSubtype) {
			b.addFunctionToSignature(ctorSubtype)
		}
	})

	// The original's comment: it's common for either the `__new__` or `__init__`
	// methods to be simple (*args: Any, **kwargs: Any) signatures. If so, we'll
	// try to filter out these signatures if they add nothing of value.
	filteredSignatures := []*CallSignature{}
	for _, sig := range b.signatures {
		if !FunctionTypeIsGradualCallableForm(sig.Type) ||
			len(sig.Type.Shared.Parameters) > 2 ||
			sig.Type.Shared.DocString != nil ||
			sig.Type.Shared.DeprecatedMessage != nil {
			filteredSignatures = append(filteredSignatures, sig)
		}
	}

	if len(filteredSignatures) > 0 {
		b.signatures = filteredSignatures
	}
}

// expandTypedKwargs corresponds to the function of the same name. The original's
// comment: if the function includes a `**kwargs: Unpack[TypedDict]` parameter,
// the individual keyword parameters are shown instead.
func expandTypedKwargs(functionType *FunctionType) *FunctionType {
	kwargsIndex := -1
	for i, param := range functionType.Shared.Parameters {
		if param.Category == parser.ParamCategoryKwargsDict {
			kwargsIndex = i
			break
		}
	}
	if kwargsIndex < 0 {
		return functionType
	}

	kwargsType := FunctionTypeGetParamType(functionType, kwargsIndex)
	if !IsClassInstance(kwargsType) || !ClassTypeIsTypedDictClass(kwargsType.(*ClassType)) ||
		!kwargsType.(*ClassType).Priv.IsUnpacked {
		return functionType
	}
	kwargsClass := kwargsType.(*ClassType)

	tdEntries := kwargsClass.Priv.TypedDictNarrowedEntries
	if tdEntries == nil && kwargsClass.Shared.TypedDictEntries != nil {
		tdEntries = kwargsClass.Shared.TypedDictEntries.KnownItems
	}
	if tdEntries == nil {
		return functionType
	}

	newFunction := FunctionTypeClone(functionType, false, nil)
	newFunction.Shared.Parameters = newFunction.Shared.Parameters[:kwargsIndex]
	if newFunction.Priv.SpecializedTypes != nil {
		newFunction.Priv.SpecializedTypes.ParameterTypes =
			newFunction.Priv.SpecializedTypes.ParameterTypes[:kwargsIndex]
	}

	kwSeparatorIndex := -1
	for i, param := range functionType.Shared.Parameters {
		if param.Category == parser.ParamCategoryArgsList {
			kwSeparatorIndex = i
			break
		}
	}

	// The original's comment: add a keyword separator if necessary.
	if kwSeparatorIndex < 0 && tdEntries.Size() > 0 {
		FunctionTypeAddKeywordOnlyParamSeparator(newFunction)
	}

	tdEntries.ForEach(func(tdEntry *TypedDictEntry, name string) {
		// The original's comment: a TypedDict entry type may carry the TypedDict's
		// own type parameters (e.g. an unpacked generic TypedDict
		// `**kwargs: Unpack[TD[int]]`), so specialize each entry against the
		// (possibly generic) TypedDict instance. Specializing an already-concrete
		// type is a no-op, so this is safe for non-generic TypedDicts.
		specializedValueType := PartiallySpecializeType(tdEntry.ValueType, kwargsClass, nil, nil)

		entryName := name
		var defaultType Type
		if !tdEntry.IsRequired {
			defaultType = specializedValueType
		}

		FunctionTypeAddParam(newFunction, FunctionParamCreate(
			parser.ParamCategorySimple, specializedValueType, FunctionParamFlagsTypeDeclared,
			&entryName, defaultType, nil))
	})

	if kwargsClass.Shared.TypedDictEntries == nil ||
		kwargsClass.Shared.TypedDictEntries.ExtraItems == nil {
		return newFunction
	}
	extraItemsType := kwargsClass.Shared.TypedDictEntries.ExtraItems.ValueType

	if !IsNilType(extraItemsType) && !IsNever(extraItemsType) {
		// The original's comment: specialized for the same reason as the per-entry
		// types above. This PEP 728 `extra_items` branch uses the identical
		// mechanism and is intentionally not separately tested.
		specializedExtraItemsType := PartiallySpecializeType(extraItemsType, kwargsClass, nil, nil)

		kwargsName := "kwargs"
		FunctionTypeAddParam(newFunction, FunctionParamCreate(
			parser.ParamCategoryKwargsDict, specializedExtraItemsType,
			FunctionParamFlagsTypeDeclared, &kwargsName, nil, nil))
	}

	return newFunction
}

// PrintControlFlowGraph corresponds to the typeEvaluator.ts passthrough of the
// same name, which forwards to the code flow engine. It is a debugging aid
// enabled by a verbose logging option, so the port logs the same header line and
// leaves the graph body to formatControlFlowGraph, which is not ported: nothing
// consumes it except a human reading the log.
func (e *typeEvaluator) PrintControlFlowGraph(
	flowNode FlowNode, reference parser.ExpressionNode, callName string, logger common.ConsoleInterface,
) {
	if logger == nil {
		return
	}

	referenceText := "(none)"
	if reference != nil {
		fileInfo := GetFileInfo(reference)
		pos := common.ConvertOffsetToPosition(reference.NodeBase().TextRange.Start, fileInfo.Lines)
		referenceText = PrintExpression(reference, PrintExpressionFlagsNone) +
			"[" + itoa(pos.Line+1) + ":" + itoa(pos.Character+1) + "]"
	}

	logger.Log(callName + "@" + itoa(flowNode.FlowBase().ID) + ": " + referenceText)
}
