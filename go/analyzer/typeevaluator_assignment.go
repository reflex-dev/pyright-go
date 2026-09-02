/*
 * typeevaluator_assignment.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/typeEvaluator.ts (pyright 1.1.412):
 * evaluateTypesForAssignmentStatement and the two predicates it uses to decide
 * whether an assignment target has a declared type.
 *
 * An assignment is where a symbol acquires an inferred type, so this is the
 * other end of the path getInferredTypeOfDeclaration walks: that function finds
 * the statement that assigned a variable and calls into here to evaluate it.
 *
 * Most of the body is type-alias handling, and the reason is recursion. A
 * traditional type alias is written with ordinary assignment syntax, so the
 * evaluator cannot know it is looking at one until it has evaluated the
 * right-hand side -- but the right-hand side may refer to the alias being
 * defined. The original resolves this by synthesizing a placeholder TypeVar,
 * writing it to the cache under the assignment and both sides of it, evaluating
 * the RHS against that, and then retroactively setting the placeholder's bound
 * type to the result. synthesizeTypeAliasPlaceholder is ported here in full
 * because it is what makes that work.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/localization"
	"github.com/microsoft/pyright/go/parser"
)

// evaluateTypesForAssignmentStatement corresponds to the function of the same
// name.
func (e *typeEvaluator) evaluateTypesForAssignmentStatement(node *parser.AssignmentNode) {
	fileInfo := GetFileInfo(node)

	// The original's comment: if the entire statement has already been
	// evaluated, don't re-evaluate it.
	if e.isTypeCached(node) {
		return
	}

	flags := EvalFlagsNone
	if fileInfo.IsStubFile {
		// The original's comment: an assignment of ellipsis means "Any" within a
		// type stub file.
		flags |= EvalFlagsConvertEllipsisToAny
	}

	switch node.D.RightExpr.GetNodeType() {
	case parser.ParseNodeTypeName, parser.ParseNodeTypeMemberAccess:
		// The original's comment: don't specialize a generic class on assignment
		// (e.g. "x = list" or "x = collections.OrderedDict") because we may want
		// to later specialize it (e.g. "x[int]").
		flags |= EvalFlagsNoSpecialize
	}

	// Is this type already cached?
	rightHandType := e.readTypeCache(node.D.RightExpr, nil)
	isIncomplete := false
	var expectedTypeDiagAddendum *common.DiagnosticAddendum
	var declaredType Type
	declaredTypeResolved := false

	// The original's comment: a runtime-first query may have cached the RHS
	// without its assignment context. Re-evaluate it when the annotation expects
	// a TypeForm so the ordinary cache cannot suppress contextual validation and
	// conversion.
	if rightHandType != nil && e.cachedAssignmentTargetMayHaveDeclaredType(node.D.LeftExpr) {
		declaredType = e.GetDeclaredTypeForExpression(node.D.LeftExpr, EvaluatorUsageSet())
		declaredTypeResolved = true
		if declaredType != nil && expectedTypeWantsTypeForm(declaredType) {
			rightHandType = nil
		}
	}

	if rightHandType == nil {
		// The original's comment: special-case the typing.pyi file, which
		// contains some special types that the type analyzer needs to interpret
		// differently.
		if fileInfo.IsTypingStubFile || fileInfo.IsTypingExtensionsStubFile {
			rightHandType = e.handleTypingStubAssignment(node)
			if rightHandType != nil {
				e.writeTypeCache(node.D.RightExpr, &TypeResult{Type: rightHandType}, evalFlagsNonePtr(), nil, false)
			}
		}
	}

	if rightHandType == nil {
		rightHandType, isIncomplete, expectedTypeDiagAddendum =
			e.evaluateAssignmentRightHandSide(node, fileInfo, flags, declaredType, declaredTypeResolved)
	}

	e.assignTypeToExpression(
		node.D.LeftExpr,
		&TypeResult{Type: rightHandType, IsIncomplete: isIncomplete},
		node.D.RightExpr,
		true,
		true,
		expectedTypeDiagAddendum,
	)

	e.writeTypeCache(
		node,
		&TypeResult{Type: rightHandType, IsIncomplete: isIncomplete},
		evalFlagsNonePtr(),
		nil,
		false,
	)
}

// evaluateAssignmentRightHandSide is the original's `if (!rightHandType)` block:
// the type-alias detection, the placeholder, the RHS evaluation and the
// constant-bool special case.
func (e *typeEvaluator) evaluateAssignmentRightHandSide(
	node *parser.AssignmentNode,
	fileInfo *AnalyzerFileInfo,
	flags EvalFlags,
	declaredType Type,
	declaredTypeResolved bool,
) (Type, bool, *common.DiagnosticAddendum) {
	var typeAliasNameNode *parser.NameNode
	var typeAliasPlaceholder *TypeVarType
	isSpeculativeTypeAlias := false

	if e.isDeclaredTypeAlias(node.D.LeftExpr) {
		flags = EvalFlagsInstantiableType |
			EvalFlagsTypeExpression |
			EvalFlagsStrLiteralAsType |
			EvalFlagsNoParamSpec |
			EvalFlagsNoTypeVarTuple |
			EvalFlagsNoClassVar

		typeAliasNameNode, _ = node.D.LeftExpr.(*parser.TypeAnnotationNode).D.ValueExpr.(*parser.NameNode)

		if !e.isLegalTypeAliasExpressionForm(node.D.RightExpr, true) {
			e.AddDiagnostic(
				DiagnosticRuleReportInvalidTypeForm,
				localization.LocMessage.TypeAliasIllegalExpressionForm(),
				node.D.RightExpr,
				nil,
			)
		}
	} else if leftName, ok := node.D.LeftExpr.(*parser.NameNode); ok {
		symbolWithScope := e.lookUpSymbolRecursive(leftName, leftName.D.Value, false, false)

		if symbolWithScope != nil {
			decls := symbolWithScope.Symbol.GetDeclarations()

			if len(decls) == 1 {
				if e.isPossibleTypeAliasDeclaration(decls[0]) {
					typeAliasNameNode = leftName
					isSpeculativeTypeAlias = true
					flags |= EvalFlagsNoConvertSpecialForm
				} else if e.isPossibleTypeDictFactoryCall(decls[0]) {
					// The original's comment: handle calls to TypedDict factory
					// functions like type aliases to support recursive field type
					// definitions.
					typeAliasNameNode = leftName
				}
			}
		}
	}

	if typeAliasNameNode != nil {
		typeAliasPlaceholder = e.synthesizeTypeAliasPlaceholder(typeAliasNameNode, false)

		e.writeTypeCache(node, &TypeResult{Type: typeAliasPlaceholder}, nil, nil, false)
		e.writeTypeCache(node.D.LeftExpr, &TypeResult{Type: typeAliasPlaceholder}, nil, nil, false)

		if annotation, ok := node.D.LeftExpr.(*parser.TypeAnnotationNode); ok {
			e.writeTypeCache(annotation.D.ValueExpr, &TypeResult{Type: typeAliasPlaceholder}, nil, nil, false)
		}
	}

	if !declaredTypeResolved {
		declaredType = e.GetDeclaredTypeForExpression(node.D.LeftExpr, EvaluatorUsageSet())
	}

	if declaredType != nil {
		liveTypeVarScopes := GetTypeVarScopesForNode(node)
		declaredType = MakeTypeVarsBound(declaredType, liveTypeVarScopes, true)
	}

	srcTypeResult := e.getTypeOfExpression(node.D.RightExpr, flags, makeInferenceContext(declaredType))

	rightHandType := srcTypeResult.Type
	expectedTypeDiagAddendum := srcTypeResult.ExpectedTypeDiagAddendum
	isIncomplete := srcTypeResult.IsIncomplete

	// The original's comment: if this was a speculative type alias, it becomes a
	// real type alias only if the evaluated type is an instantiable type.
	if isSpeculativeTypeAlias && !e.isLegalImplicitTypeAliasType(rightHandType) {
		typeAliasNameNode = nil
	}

	if typeAliasNameNode == nil {
		// The original's comment: if the RHS is a constant boolean expression,
		// assign it a literal type.
		//
		// The original passes only three arguments; the two alias lists are
		// optional there and absent here.
		constExprValue, known := EvaluateStaticBoolExpression(
			node.D.RightExpr,
			fileInfo.ExecutionEnvironment,
			fileInfo.DefinedConstants,
			nil,
			nil,
		)

		if known {
			boolType := e.GetBuiltInObject(node, "bool", nil)
			if IsClassInstance(boolType) {
				rightHandType = ClassTypeCloneWithLiteral(boolType.(*ClassType), LiteralBool(constExprValue))
			}
		}

		return rightHandType, isIncomplete, expectedTypeDiagAddendum
	}

	// The original asserts typeAliasPlaceholder is defined here.
	if typeAliasPlaceholder == nil {
		return rightHandType, isIncomplete, expectedTypeDiagAddendum
	}

	// The original's comment: if this is a type alias, record its name based on
	// the assignment target.
	rightHandType = e.transformTypeForTypeAlias(rightHandType, typeAliasNameNode, typeAliasPlaceholder, false)

	if IsTypeAliasRecursive(typeAliasPlaceholder, rightHandType) {
		e.AddDiagnostic(
			DiagnosticRuleReportGeneralTypeIssues,
			localization.LocMessage.TypeAliasIsRecursiveDirect().Format(typeAliasNameNode.D.Value),
			node.D.RightExpr,
			nil,
		)

		rightHandType = UnknownTypeCreate(false)
	}

	// The original's comment: set the resulting type to the boundType of the
	// original type alias to support recursive type aliases.
	typeAliasPlaceholder.Shared.BoundType = rightHandType

	// The original's comment: record the type parameters within the recursive
	// type alias so it can be specialized.
	//
	// `rightHandType.props?.typeAliasInfo?.shared.typeParams` is undefined when
	// either link is absent, which is a nil slice here.
	if props := rightHandType.Base().Props; props != nil && props.TypeAliasInfo != nil {
		typeAliasPlaceholder.Shared.RecursiveAlias.TypeParams = props.TypeAliasInfo.Shared.TypeParams
	} else {
		typeAliasPlaceholder.Shared.RecursiveAlias.TypeParams = nil
	}

	return rightHandType, isIncomplete, expectedTypeDiagAddendum
}

// synthesizeTypeAliasPlaceholder corresponds to the function of the same name.
// The original's comment: synthesize a TypeVar that acts as a placeholder for a
// type alias. This allows the type alias definition to refer to itself.
func (e *typeEvaluator) synthesizeTypeAliasPlaceholder(
	nameNode *parser.NameNode,
	isTypeAliasType bool,
) *TypeVarType {
	placeholder := TypeVarTypeCreateInstantiable("__type_alias_"+nameNode.D.Value, TypeVarKindTypeVar)
	placeholder.Shared.IsSynthesized = true
	typeVarScopeID := GetScopeIdForNode(nameNode)
	fileInfo := GetFileInfo(nameNode)

	placeholder.Shared.RecursiveAlias = &TypeAliasSharedInfo{
		Name:            nameNode.D.Value,
		FullName:        GetClassFullName(nameNode, fileInfo.ModuleName, nameNode.D.Value),
		ModuleName:      fileInfo.ModuleName,
		FileUri:         fileInfo.FileUri,
		TypeVarScopeId:  typeVarScopeID,
		IsTypeAliasType: isTypeAliasType,
	}
	placeholder.Priv.ScopeID = typeVarScopeID

	return placeholder
}

/*
 * The two predicates that decide whether an assignment target has a declared
 * type.
 */

// isDeclaredTypeAlias corresponds to the function of the same name.
func (e *typeEvaluator) isDeclaredTypeAlias(expression parser.ExpressionNode) bool {
	annotation, ok := expression.(*parser.TypeAnnotationNode)
	if !ok {
		return false
	}

	valueName, ok := annotation.D.ValueExpr.(*parser.NameNode)
	if !ok {
		return false
	}

	symbolWithScope := e.lookUpSymbolRecursive(expression, valueName.D.Value, false, false)
	if symbolWithScope == nil {
		return false
	}

	for _, decl := range symbolWithScope.Symbol.GetDeclarations() {
		if e.isExplicitTypeAliasDeclaration(decl) {
			return true
		}
	}

	return false
}

// cachedAssignmentTargetMayHaveDeclaredType corresponds to the function of the
// same name.
func (e *typeEvaluator) cachedAssignmentTargetMayHaveDeclaredType(expression parser.ExpressionNode) bool {
	switch n := expression.(type) {
	case *parser.NameNode:
		symbolWithScope := e.lookUpSymbolRecursive(n, n.D.Value, true, false)
		return symbolWithScope != nil &&
			(symbolWithScope.Symbol.HasTypedDeclarations() || symbolWithScope.Scope.Type == ScopeTypeClass)

	case *parser.TypeAnnotationNode, *parser.MemberAccessNode, *parser.IndexNode:
		return true

	case *parser.TupleNode:
		if len(n.D.Items) == 0 {
			return false
		}
		for _, item := range n.D.Items {
			if item.GetNodeType() == parser.ParseNodeTypeUnpack {
				return false
			}
		}
		for _, item := range n.D.Items {
			if !e.cachedAssignmentTargetMayHaveDeclaredType(item) {
				return false
			}
		}
		return true
	}

	return false
}

// handleTypingStubAssignment corresponds to the function of the same name. It
// creates the special forms typing.pyi defines by assignment, and is the
// counterpart of handleTypingStubTypeAnnotation, which handles the ones defined
// by annotation.
//
// The table is the fifteen names typing.pyi assigns rather than declares. Six of
// them alias a builtins or collections class under a capitalized name (List ->
// list, Deque -> collections.deque); the rest carry an empty alias, which
// createSpecialBuiltInClass reads as "there is no runtime class behind this,
// synthesize one".
//
// Any is not in the table. It is the one special form that is not a class at
// all, so it short-circuits above the lookup.
func (e *typeEvaluator) handleTypingStubAssignment(node *parser.AssignmentNode) Type {
	nameNode, ok := node.D.LeftExpr.(*parser.NameNode)
	if !ok {
		return nil
	}

	assignedName := nameNode.D.Value

	if assignedName == "Any" {
		return AnyTypeCreateSpecialForm()
	}

	entry, ok := typingStubAssignmentTypes[assignedName]
	if !ok {
		return nil
	}

	// The original's comment: evaluate the expression so symbols are marked as
	// accessed.
	e.getTypeOfExpression(node.D.RightExpr, EvalFlagsNone, nil)

	return e.createSpecialBuiltInClass(node, assignedName, entry)
}

// typingStubAssignmentTypes is the original's `specialTypes` map, rebuilt on
// every call there and hoisted to a package-level table here. The entries are
// never mutated.
var typingStubAssignmentTypes = map[string]aliasMapEntry{
	"overload":      {Alias: "", Module: "builtins"},
	"TypeVar":       {Alias: "", Module: "builtins"},
	"_promote":      {Alias: "", Module: "builtins"},
	"no_type_check": {Alias: "", Module: "builtins"},
	"NoReturn":      {Alias: "", Module: "builtins"},
	"Never":         {Alias: "", Module: "builtins"},
	"Counter":       {Alias: "Counter", Module: "collections"},
	"List":          {Alias: "list", Module: "builtins"},
	"Dict":          {Alias: "dict", Module: "builtins"},
	"DefaultDict":   {Alias: "defaultdict", Module: "collections"},
	"Set":           {Alias: "set", Module: "builtins"},
	"FrozenSet":     {Alias: "frozenset", Module: "builtins"},
	"Deque":         {Alias: "deque", Module: "collections"},
	"ChainMap":      {Alias: "ChainMap", Module: "collections"},
	"OrderedDict":   {Alias: "OrderedDict", Module: "collections"},
}
