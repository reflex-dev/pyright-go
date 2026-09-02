/*
 * typeevaluator_functionshape.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/typeEvaluator.ts (pyright 1.1.412):
 * getTypeOfFunctionPredecorated and getFunctionFullName.
 *
 * The shape of a `def` before any decorator or async wrapper is applied: its
 * flags, its type parameters, one FunctionParam per parameter with the type
 * resolved from an annotation or inferred, and its declared return type.
 *
 * This was producing the largest single class of false positives in the gate.
 * Returning nil made GetTypeOfFunction return nil, which made
 * getTypeForDeclaration yield a DeclaredSymbolTypeInfo with no type -- and
 * getEffectiveTypeOfSymbolForUsage reads a nil declared type as "this symbol
 * refers to itself". Every function in the corpus was being reported as a
 * recursive definition: ~1,100 of 3,417 emitted diagnostics.
 *
 * The parameter loop is long because each parameter is four decisions -- where
 * its annotation comes from, whether the pseudo-generic substitution applies,
 * what its default evaluates to, and whether it introduces an implicit
 * position-only separator -- and they interact. The position-only logic in
 * particular reads oddly and is preserved exactly: `paramsArePositionOnly`
 * starts true, is cleared by the first non-implicit parameter, and the separator
 * is added *before* processing a parameter rather than after the group ends.
 */

package analyzer

import (
	"strings"

	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/localization"
	"github.com/microsoft/pyright/go/parser"
)

// getFunctionFullName corresponds to the function of the same name.
func getFunctionFullName(functionNode parser.ParseNode, moduleName string, functionName string) string {
	nameParts := []string{functionName}

	// The original's comment: walk the parse tree looking for classes or
	// functions.
	curNode := functionNode
	for curNode != nil {
		enclosing := GetEnclosingClassOrFunction(curNode)
		if enclosing == nil {
			break
		}
		switch n := enclosing.(type) {
		case *parser.ClassNode:
			nameParts = append(nameParts, n.D.Name.D.Value)
		case *parser.FunctionNode:
			nameParts = append(nameParts, n.D.Name.D.Value)
		}
		curNode = enclosing
	}

	nameParts = append(nameParts, moduleName)

	// reverse().join('.')
	for i, j := 0, len(nameParts)-1; i < j; i, j = i+1, j-1 {
		nameParts[i], nameParts[j] = nameParts[j], nameParts[i]
	}
	return strings.Join(nameParts, ".")
}

// getTypeOfFunctionPredecorated corresponds to the function of the same name.
// The original's comment: evaluates the type of a "def" statement without
// applying an async modifier or any decorators.
func (e *typeEvaluator) getTypeOfFunctionPredecorated(node *parser.FunctionNode) *FunctionType {
	fileInfo := GetFileInfo(node)

	// Is this type already cached?
	if cached := e.readTypeCache(node.D.Name, evalFlagsNonePtr()); cached != nil && IsFunction(cached) {
		return cached.(*FunctionType)
	}

	var functionDecl *FunctionDeclaration
	if decl := GetDeclaration(node); decl != nil {
		functionDecl, _ = decl.(*FunctionDeclaration)
	}

	// The original's comment: there was no cached type, so create a new one.
	// Retrieve the containing class node if the function is a method.
	containingClassNode := GetEnclosingClass(node, true)
	var containingClassType *ClassType
	if containingClassNode != nil {
		if classInfo := e.GetTypeOfClass(containingClassNode); classInfo != nil {
			containingClassType = classInfo.ClassType
		}
	}

	functionInfo := e.getFunctionInfoFromDecorators(node, containingClassNode != nil)
	functionFlags := functionInfo.Flags
	if functionDecl != nil && functionDecl.IsGenerator {
		functionFlags |= FunctionTypeFlagsGenerator
	}

	if fileInfo.IsStubFile {
		functionFlags |= FunctionTypeFlagsStubDefinition
	} else if fileInfo.IsInPyTypedPackage {
		functionFlags |= FunctionTypeFlagsPyTypedDefinition
	}

	if node.D.IsAsync {
		functionFlags |= FunctionTypeFlagsAsync
	}

	docString, hasDocString := GetDocString(node.D.Suite.D.Statements)
	var docStringPtr *string
	if hasDocString {
		docStringPtr = &docString
	}

	functionType := FunctionTypeCreateInstance(
		node.D.Name.D.Value,
		getFunctionFullName(node, fileInfo.ModuleName, node.D.Name.D.Value),
		fileInfo.ModuleName,
		functionFlags|FunctionTypeFlagsPartiallyEvaluated,
		docStringPtr,
	)

	functionType.Shared.TypeVarScopeID = GetScopeIdForNode(node)
	functionType.Shared.DeprecatedMessage = functionInfo.DeprecationMessage
	functionType.Shared.MethodClass = containingClassType

	if node.D.Name.D.Value == "__init__" || node.D.Name.D.Value == "__new__" {
		if containingClassNode != nil {
			functionType.Priv.ConstructorTypeVarScopeID = GetScopeIdForNode(containingClassNode)
		}
	}

	if fileInfo.IsBuiltInStubFile || fileInfo.IsTypingStubFile || fileInfo.IsTypingExtensionsStubFile {
		// The original's comment: mark the function as a built-in stdlib
		// function.
		functionType.Shared.Flags |= FunctionTypeFlagsBuiltIn
	}

	functionType.Shared.Declaration = functionDecl

	// The original's comment: allow recursion by caching and registering the
	// partially-constructed function type.
	if scope := GetScopeForNode(node); scope != nil && functionDecl != nil {
		if functionSymbol := scope.LookUpSymbolRecursive(node.D.Name.D.Value, nil); functionSymbol != nil {
			e.setSymbolResolutionPartialType(functionSymbol.Symbol, functionDecl, functionType)
		}
	}

	// The original wraps the rest in invalidateTypeCacheIfCanceled; see the note
	// on that wrapper in typeevaluator_class.go.
	return e.buildFunctionShape(node, functionType, functionInfo, fileInfo, containingClassType)
}

// buildFunctionShape is the body of the original's
// invalidateTypeCacheIfCanceled callback.
func (e *typeEvaluator) buildFunctionShape(
	node *parser.FunctionNode,
	functionType *FunctionType,
	functionInfo *FunctionDecoratorInfo,
	fileInfo *AnalyzerFileInfo,
	containingClassType *ClassType,
) *FunctionType {
	e.writeTypeCache(node.D.Name, &TypeResult{Type: functionType}, nil, nil, false)

	// The original's comment: is this an "__init__" method within a
	// pseudo-generic class? If so, we'll add generic types to the constructor's
	// parameters.
	addGenericParamTypes := containingClassType != nil &&
		ClassTypeIsPseudoGenericClass(containingClassType) &&
		node.D.Name.D.Value == "__init__"

	paramTypes := []Type{}

	// The original's comment: determine if the first parameter should be skipped
	// for comment-based function annotations.
	firstCommentAnnotationIndex := 0
	if containingClassType != nil && (functionType.Shared.Flags&FunctionTypeFlagsStaticMethod) == 0 {
		firstCommentAnnotationIndex = 1
	}

	// The original's comment: if there is a function annotation comment,
	// validate that it has the correct number of parameter annotations.
	if node.D.FuncAnnotationComment != nil && !node.D.FuncAnnotationComment.D.IsEllipsis {
		expected := len(node.D.Params) - firstCommentAnnotationIndex
		received := len(node.D.FuncAnnotationComment.D.ParamAnnotations)

		// The original's comment: for methods with "self" or "cls" parameters,
		// the annotation list can either include or exclude the annotation for
		// the first parameter.
		if firstCommentAnnotationIndex > 0 && received == len(node.D.Params) {
			firstCommentAnnotationIndex = 0
		} else if received != expected {
			e.AddDiagnostic(
				DiagnosticRuleReportInvalidTypeForm,
				localization.LocMessage.AnnotatedParamCountMismatch().Format(expected, received),
				node.D.FuncAnnotationComment,
				nil,
			)
		}
	}

	// The original's comment: if this function uses PEP 695 syntax for type
	// parameters, accumulate the list of type parameters upfront.
	typeParamsSeen := []*TypeVarType{}
	typeParamsSeenPtr := &typeParamsSeen
	if node.D.TypeParams != nil {
		evaluated := e.evaluateTypeParamList(node.D.TypeParams)
		converted := make([]*TypeVarType, 0, len(evaluated))
		for _, typeParam := range evaluated {
			if asTypeVar, ok := ConvertToInstance(typeParam, false).(*TypeVarType); ok {
				converted = append(converted, asTypeVar)
			}
		}
		functionType.Shared.TypeParams = converted

		// The original still creates typeParamsSeen in this branch and keeps
		// appending to it, but never reads it back into shared.typeParams, so the
		// accumulator is an orphan. Preserved.
	} else {
		// DIVERGENCE forced by Go's value semantics. The original writes
		// `functionType.shared.typeParams = typeParamsSeen`, which aliases the same
		// JavaScript array -- every later append is visible through
		// shared.typeParams. A Go slice header is a value, so assigning it once
		// froze shared.typeParams at empty, and findScopedTypeVar could never match
		// a legacy-syntax TypeVar against the function that owns it. The
		// accumulator therefore points at the field itself.
		functionType.Shared.TypeParams = typeParamsSeen
		typeParamsSeenPtr = &functionType.Shared.TypeParams
	}

	state := &functionParamState{
		paramsArePositionOnly:       true,
		firstCommentAnnotationIndex: firstCommentAnnotationIndex,
		addGenericParamTypes:        addGenericParamTypes,
		typeParamsSeen:              typeParamsSeenPtr,
		paramTypes:                  &paramTypes,
	}

	isFirstParamClsOrSelf := containingClassType != nil &&
		(FunctionTypeIsClassMethod(functionType) ||
			FunctionTypeIsInstanceMethod(functionType) ||
			FunctionTypeIsConstructorMethod(functionType))
	state.isFirstParamClsOrSelf = isFirstParamClsOrSelf
	if isFirstParamClsOrSelf {
		state.firstNonClsSelfParamIndex = 1
	}

	for index, param := range node.D.Params {
		e.processFunctionParam(node, functionType, functionInfo, fileInfo, containingClassType, param, index, state)
	}

	if state.paramsArePositionOnly &&
		len(functionType.Shared.Parameters) > state.firstNonClsSelfParamIndex {
		FunctionTypeAddPositionOnlyParamSeparator(functionType)
	}

	// The original's comment: update the types for the nodes associated with the
	// parameters.
	scopeIds := GetTypeVarScopesForNode(node)
	for index, paramType := range paramTypes {
		paramNameNode := node.D.Params[index].D.Name
		if paramNameNode == nil {
			continue
		}

		if IsUnknown(paramType) {
			functionType.Shared.Flags |= FunctionTypeFlagsUnannotatedParams
		}

		bound := MakeTypeVarsBound(paramType, scopeIds, true)
		e.writeTypeCache(paramNameNode, &TypeResult{Type: bound}, evalFlagsNonePtr(), nil, false)
	}

	e.applyGradualCallableForm(functionType, paramTypes)
	e.evaluateDeclaredReturnType(node, functionType, fileInfo, typeParamsSeenPtr)

	// The original's comment: validate the default types for all type
	// parameters.
	for index, typeParam := range functionType.Shared.TypeParams {
		var bestErrorNode parser.ExpressionNode = node.D.Name
		if node.D.TypeParams != nil && index < len(node.D.TypeParams.D.Params) {
			typeParamNode := node.D.TypeParams.D.Params[index]
			if typeParamNode.D.DefaultExpr != nil {
				bestErrorNode = typeParamNode.D.DefaultExpr
			} else {
				bestErrorNode = typeParamNode.D.Name
			}
		}

		e.validateTypeParamDefault(
			bestErrorNode,
			typeParam,
			functionType.Shared.TypeParams[:index],
			functionType.Shared.TypeVarScopeID,
		)
	}

	// The original's comment: clear the "partially evaluated" flag to indicate
	// that the functionType is fully evaluated.
	functionType.Shared.Flags &^= FunctionTypeFlagsPartiallyEvaluated

	e.writeTypeCache(node.D.Name, &TypeResult{Type: functionType}, evalFlagsNonePtr(), nil, false)

	return functionType
}

// functionParamState carries the loop-carried variables of the original's
// `node.d.params.forEach` body, which mutates four bindings declared outside it.
type functionParamState struct {
	paramsArePositionOnly       bool
	isFirstParamClsOrSelf       bool
	firstNonClsSelfParamIndex   int
	firstCommentAnnotationIndex int
	addGenericParamTypes        bool
	typeParamsSeen              *[]*TypeVarType
	paramTypes                  *[]Type
}

// processFunctionParam is one iteration of the original's parameter loop.
func (e *typeEvaluator) processFunctionParam(
	node *parser.FunctionNode,
	functionType *FunctionType,
	functionInfo *FunctionDecoratorInfo,
	fileInfo *AnalyzerFileInfo,
	containingClassType *ClassType,
	param *parser.ParameterNode,
	index int,
	state *functionParamState,
) {
	var paramType Type
	var annotatedType Type
	var paramTypeNode parser.ExpressionNode

	if param.D.Name != nil {
		if index == 0 && state.isFirstParamClsOrSelf {
			// The original's comment: mark "self/cls" as accessed.
			e.markParamAccessed(param)
		} else if FunctionTypeIsAbstractMethod(functionType) {
			// The original's comment: mark all parameters in abstract methods as
			// accessed.
			e.markParamAccessed(param)
		} else if containingClassType != nil && ClassTypeIsProtocolClass(containingClassType) {
			// The original's comment: mark all parameters in protocol methods as
			// accessed.
			e.markParamAccessed(param)
		}
	}

	switch {
	case param.D.Annotation != nil:
		paramTypeNode = param.D.Annotation
	case param.D.AnnotationComment != nil:
		paramTypeNode = param.D.AnnotationComment
	case node.D.FuncAnnotationComment != nil && !node.D.FuncAnnotationComment.D.IsEllipsis:
		adjustedIndex := index - state.firstCommentAnnotationIndex
		if adjustedIndex >= 0 && adjustedIndex < len(node.D.FuncAnnotationComment.D.ParamAnnotations) {
			paramTypeNode = node.D.FuncAnnotationComment.D.ParamAnnotations[adjustedIndex]
		}
	}

	if paramTypeNode != nil {
		if (functionInfo.Flags & FunctionTypeFlagsNoTypeCheck) != 0 {
			annotatedType = UnknownTypeCreate(false)
		} else {
			annotatedType = e.getTypeOfParamAnnotation(paramTypeNode, param.D.Category)
		}

		if annotatedType != nil {
			*state.typeParamsSeen = AddTypeVarsToListIfUnique(
				*state.typeParamsSeen,
				GetTypeVarArgsRecursive(annotatedType, 0),
				functionType.Shared.TypeVarScopeID,
			)
		}

		if IsTypeVarTuple(annotatedType) && !annotatedType.(*TypeVarType).Priv.IsUnpacked {
			name := annotatedType.(*TypeVarType).Shared.Name
			e.AddDiagnostic(
				DiagnosticRuleReportInvalidTypeForm,
				localization.LocMessage.UnpackedTypeVarTupleExpected().Format(name, name),
				paramTypeNode,
				nil,
			)
			annotatedType = UnknownTypeCreate(false)
		}
	}

	if annotatedType == nil && state.addGenericParamTypes {
		if index > 0 && param.D.Category == parser.ParamCategorySimple &&
			param.D.Name != nil && param.D.DefaultValue == nil {
			typeParamName := getPseudoGenericTypeVarName(param.D.Name.D.Value)
			for _, classParam := range containingClassType.Shared.TypeParams {
				if classParam.Shared.Name == typeParamName {
					annotatedType = classParam
					break
				}
			}
		}
	}

	if annotatedType != nil {
		annotatedType = e.adjustParamAnnotatedType(param, annotatedType)
	}

	var defaultValueType Type
	if param.D.DefaultValue != nil {
		// The original's comment: if this is a stub file, a protocol, an
		// overload, or a class whose body is a placeholder implementation, treat
		// a "...", as an "Any" value.
		treatEllipsisAsAny := fileInfo.IsStubFile || IsSuiteEmpty(node.D.Suite)
		if containingClassType != nil && ClassTypeIsProtocolClass(containingClassType) {
			treatEllipsisAsAny = true
		}
		if FunctionTypeIsOverloaded(functionType) || FunctionTypeIsAbstractMethod(functionType) {
			treatEllipsisAsAny = true
		}

		defaultFlags := EvalFlagsNone
		if treatEllipsisAsAny {
			defaultFlags = EvalFlagsConvertEllipsisToAny
		}

		defaultValueType = e.getTypeOfExpression(
			param.D.DefaultValue,
			defaultFlags,
			makeInferenceContext(annotatedType),
		).Type
	}

	if annotatedType != nil {
		// The original's comment: if there was both a type annotation and a
		// default value, verify that the default value matches the annotation.
		if param.D.DefaultValue != nil && defaultValueType != nil {
			diagAddendum := common.NewDiagnosticAddendum()

			if !e.AssignType(annotatedType, defaultValueType, diagAddendum, nil, AssignTypeFlagsDefault, 0) {
				e.AddDiagnostic(
					DiagnosticRuleReportArgumentType,
					localization.LocMessage.ParamAssignmentMismatch().Format(
						e.PrintType(defaultValueType, nil),
						e.PrintType(annotatedType, nil),
					)+diagAddendum.GetString(),
					param.D.DefaultValue,
					nil,
				)
			}
		}

		paramType = annotatedType
	}

	e.applyImplicitPositionOnly(node, functionType, param, index, state)

	// The original's comment: if there was no annotation for the parameter,
	// infer its type if possible.
	isTypeInferred := false
	if paramTypeNode == nil {
		isTypeInferred = true
		if inferredType := e.inferParamType(node, functionType.Shared.Flags, index, containingClassType); inferredType != nil {
			paramType = inferredType
		}
	}

	if paramType == nil {
		paramType = UnknownTypeCreate(false)
	}

	paramFlags := FunctionParamFlagsNone
	if isTypeInferred {
		paramFlags |= FunctionParamFlagsTypeInferred
	}
	if paramTypeNode != nil {
		paramFlags |= FunctionParamFlagsTypeDeclared
	}

	var paramName *string
	if param.D.Name != nil {
		name := param.D.Name.D.Value
		paramName = &name
	}

	functionParam := FunctionParamCreate(
		param.D.Category,
		paramType,
		paramFlags,
		paramName,
		defaultValueType,
		param.D.DefaultValue,
	)

	FunctionTypeAddParam(functionType, functionParam)

	if FunctionParamIsTypeDeclared(functionParam) {
		*state.typeParamsSeen = AddTypeVarsToListIfUnique(
			*state.typeParamsSeen,
			GetTypeVarArgsRecursive(paramType, 0),
			functionType.Shared.TypeVarScopeID,
		)
	}

	if param.D.Name != nil {
		*state.paramTypes = append(*state.paramTypes,
			e.transformVariadicParamType(node, param.D.Category, paramType))
	} else {
		*state.paramTypes = append(*state.paramTypes, paramType)
	}
}

// applyImplicitPositionOnly is the original's implicit position-only block. The
// original's comment: determine whether we need to insert an implied
// position-only parameter. This is needed when a function's parameters are named
// using the old-style way of specifying position-only parameters.
func (e *typeEvaluator) applyImplicitPositionOnly(
	node *parser.FunctionNode,
	functionType *FunctionType,
	param *parser.ParameterNode,
	index int,
	state *functionParamState,
) {
	if index < state.firstNonClsSelfParamIndex {
		return
	}

	isImplicitPositionOnlyParam := false

	if param.D.Category == parser.ParamCategorySimple && param.D.Name != nil {
		hasUnnamedSimple := false
		for _, p := range node.D.Params {
			if p.D.Category == parser.ParamCategorySimple && p.D.Name == nil {
				hasUnnamedSimple = true
				break
			}
		}

		if IsPrivateName(param.D.Name.D.Value) && !hasUnnamedSimple {
			isImplicitPositionOnlyParam = true

			// The original's comment: if the parameter name indicates an
			// implicit position-only parameter but we have already seen
			// non-position-only parameters, report an error.
			allSimple := true
			for _, p := range functionType.Shared.Parameters {
				if p.Category != parser.ParamCategorySimple {
					allSimple = false
					break
				}
			}

			if !state.paramsArePositionOnly && allSimple {
				e.AddDiagnostic(
					DiagnosticRuleReportGeneralTypeIssues,
					localization.LocMessage.PositionOnlyAfterNon(),
					param.D.Name,
					nil,
				)
			}
		}
	} else {
		state.paramsArePositionOnly = false
	}

	if state.paramsArePositionOnly && !isImplicitPositionOnlyParam &&
		len(functionType.Shared.Parameters) > state.firstNonClsSelfParamIndex {
		FunctionTypeAddPositionOnlyParamSeparator(functionType)
	}

	if !isImplicitPositionOnlyParam {
		state.paramsArePositionOnly = false
	}
}

// applyGradualCallableForm is the original's two "exempt from args/kwargs
// compatibility checks" blocks.
func (e *typeEvaluator) applyGradualCallableForm(functionType *FunctionType, paramTypes []Type) {
	// The original's comment: if the function ends in P.args and P.kwargs
	// parameters, make it exempt from args/kwargs compatibility checks. This is
	// important for protocol comparisons.
	if len(paramTypes) >= 2 {
		paramType1 := paramTypes[len(paramTypes)-2]
		paramType2 := paramTypes[len(paramTypes)-1]
		if IsParamSpec(paramType1) && paramType1.(*TypeVarType).Priv.ParamSpecAccess == ParamSpecAccessArgs &&
			IsParamSpec(paramType2) && paramType2.(*TypeVarType).Priv.ParamSpecAccess == ParamSpecAccessKwargs {
			functionType.Shared.Flags |= FunctionTypeFlagsGradualCallableForm
		}
	}

	// The original's comment: if the function contains an *args and a **kwargs
	// parameter and both are annotated as Any or are unannotated, make it exempt
	// from args/kwargs compatibility checks.
	variadicsWithAnyType := 0
	for index, param := range functionType.Shared.Parameters {
		if param.Category != parser.ParamCategorySimple && param.Name != nil &&
			IsAnyOrUnknown(FunctionTypeGetParamType(functionType, index)) {
			variadicsWithAnyType++
		}
	}
	if variadicsWithAnyType >= 2 {
		functionType.Shared.Flags |= FunctionTypeFlagsGradualCallableForm
	}
}

// evaluateDeclaredReturnType is the original's return-annotation block.
func (e *typeEvaluator) evaluateDeclaredReturnType(
	node *parser.FunctionNode,
	functionType *FunctionType,
	fileInfo *AnalyzerFileInfo,
	typeParamsSeen *[]*TypeVarType,
) {
	// The original's comment: if there was a defined return type, analyze that
	// first so when we walk the contents of the function, return statements can
	// be validated against this type.
	returnTypeAnnotationNode := node.D.ReturnAnnotation
	if returnTypeAnnotationNode == nil && node.D.FuncAnnotationComment != nil {
		returnTypeAnnotationNode = node.D.FuncAnnotationComment.D.ReturnAnnotation
	}

	if returnTypeAnnotationNode != nil {
		// The original's comment: temporarily set the return type to unknown in
		// case of recursion.
		functionType.Shared.DeclaredReturnType = UnknownTypeCreate(false)

		functionType.Shared.DeclaredReturnType = e.GetTypeOfAnnotation(returnTypeAnnotationNode, &ExpectedTypeOptions{
			TypeVarGetsCurScope: true,
		})
	} else if fileInfo.IsStubFile {
		// The original's comment: if there was no return type annotation and
		// this is a type stub, we have no opportunity to infer the return type,
		// so we'll indicate that it's unknown. Special-case the __init__ method,
		// which is commonly left without an annotated return type, but we can
		// assume it returns None.
		if node.D.Name.D.Value == "__init__" {
			functionType.Shared.DeclaredReturnType = e.GetNoneType()
		} else {
			functionType.Shared.DeclaredReturnType = UnknownTypeCreate(false)
		}
	}

	// The original's comment: accumulate any type parameters used in the return
	// type.
	if functionType.Shared.DeclaredReturnType != nil && returnTypeAnnotationNode != nil {
		*typeParamsSeen = AddTypeVarsToListIfUnique(
			*typeParamsSeen,
			GetTypeVarArgsRecursive(functionType.Shared.DeclaredReturnType, 0),
			functionType.Shared.TypeVarScopeID,
		)
	}
}

// markParamAccessed corresponds to the function of the same name.
func (e *typeEvaluator) markParamAccessed(param *parser.ParameterNode) {
	if param.D.Name == nil {
		return
	}

	if symbolWithScope := e.lookUpSymbolRecursive(param.D.Name, param.D.Name.D.Value, false, false); symbolWithScope != nil {
		e.setSymbolAccessed(GetFileInfo(param), symbolWithScope.Symbol, param.D.Name)
	}
}
