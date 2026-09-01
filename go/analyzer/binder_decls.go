/*
 * binder_decls.go
 *
 * The binder's declaration builders and annotation analysis, transliterated
 * from analyzer/binder.ts (pyright 1.1.412):
 * _addInferredTypeAssignmentForVariable, _addTypeDeclarationForVariable, the
 * Final/ClassVar annotation tests, _getMemberAccessInfo, the typing-stub
 * special case, and _bindYield.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/localization"
	"github.com/microsoft/pyright/go/parser"
)

// addInferredTypeAssignmentForVariable corresponds to
// _addInferredTypeAssignmentForVariable. The TypeScript defaults
// isPossibleTypeAlias to false.
func (b *Binder) addInferredTypeAssignmentForVariable(
	target parser.ExpressionNode,
	source parser.ParseNode,
	isPossibleTypeAlias bool,
) {
	switch typed := target.(type) {
	case *parser.NameNode:
		symbolWithScope := b.currentScope.LookUpSymbolRecursive(typed.D.Value, nil)
		if symbolWithScope != nil && symbolWithScope.Symbol != nil {
			var typeAliasName *parser.NameNode
			if isPossibleTypeAlias {
				typeAliasName = typed
			}

			symbolWithScope.Symbol.AddDeclaration(&VariableDeclaration{
				DeclarationBase: DeclarationBase{
					Type:            DeclarationTypeVariable,
					Node:            target,
					Uri:             b.fileInfo.FileUri,
					Range:           common.ConvertTextRangeToRange(typed.GetRange(), b.fileInfo.Lines),
					ModuleName:      b.fileInfo.ModuleName,
					IsInExceptSuite: b.isInExceptSuite,
				},
				IsConstant:                  IsConstantName(typed.D.Value),
				InferredTypeSource:          source,
				IsInferenceAllowedInPyTyped: isInferenceAllowedInPyTyped(typed.D.Value),
				TypeAliasName:               typeAliasName,
				DocString:                   b.getVariableDocString(target),
				IsExplicitBinding:           b.currentScope.GetBindingType(typed.D.Value) != NameBindingTypeNone,
			})
		}

	case *parser.MemberAccessNode:
		accessInfo := b.getMemberAccessInfo(typed)
		if accessInfo != nil {
			name := typed.D.Member

			symbol := accessInfo.ClassScope.LookUpSymbol(name.D.Value)
			if symbol == nil {
				symbol = accessInfo.ClassScope.AddSymbol(name.D.Value, SymbolFlagsInitiallyUnbound)
				honorPrivateNaming := b.fileInfo.DiagnosticRuleSet.ReportPrivateUsage != DiagnosticLevelNone
				if IsPrivateOrProtectedName(name.D.Value) && honorPrivateNaming {
					symbol.SetIsPrivateMember()
				}
			}

			if accessInfo.IsInstanceMember {
				// If a method (which has a declared type) is being overwritten
				// by an expression with no declared type, don't mark it as an
				// instance member because the type evaluator will think that it
				// doesn't need to perform object binding.
				hasMethodDecl := false
				for _, decl := range symbol.GetDeclarations() {
					if fn, ok := decl.(*FunctionDeclaration); ok && fn.IsMethod {
						hasMethodDecl = true
						break
					}
				}
				if !symbol.IsClassMember() || !hasMethodDecl {
					symbol.SetIsInstanceMember()
				}
			} else {
				symbol.SetIsClassMember()
			}

			symbol.AddDeclaration(&VariableDeclaration{
				DeclarationBase: DeclarationBase{
					Type:            DeclarationTypeVariable,
					Node:            typed.D.Member,
					Uri:             b.fileInfo.FileUri,
					Range:           common.ConvertTextRangeToRange(typed.D.Member.GetRange(), b.fileInfo.Lines),
					ModuleName:      b.fileInfo.ModuleName,
					IsInExceptSuite: b.isInExceptSuite,
				},
				IsConstant:              IsConstantName(name.D.Value),
				InferredTypeSource:      source,
				IsDefinedByMemberAccess: true,
				DocString:               b.getVariableDocString(target),
			})
		}

	case *parser.TupleNode:
		for _, expr := range typed.D.Items {
			b.addInferredTypeAssignmentForVariable(expr, source, false)
		}

	case *parser.TypeAnnotationNode:
		b.addInferredTypeAssignmentForVariable(typed.D.ValueExpr, source, false)

	case *parser.UnpackNode:
		b.addInferredTypeAssignmentForVariable(typed.D.Expr, source, false)

	case *parser.ListNode:
		for _, entry := range typed.D.Items {
			b.addInferredTypeAssignmentForVariable(entry, source, false)
		}
	}
}

// isInferenceAllowedInPyTyped corresponds to _isInferenceAllowedInPyTyped.
func isInferenceAllowedInPyTyped(symbolName string) bool {
	exemptSymbols := []string{"__match_args__", "__slots__", "__all__"}
	for _, name := range exemptSymbols {
		if name == symbolName {
			return true
		}
	}
	return false
}

// addTypeDeclarationForVariable corresponds to _addTypeDeclarationForVariable.
func (b *Binder) addTypeDeclarationForVariable(target parser.ExpressionNode, typeAnnotation parser.ExpressionNode) {
	declarationHandled := false

	switch typed := target.(type) {
	case *parser.NameNode:
		symbolWithScope := b.currentScope.LookUpSymbolRecursive(typed.D.Value, nil)
		if symbolWithScope != nil && symbolWithScope.Symbol != nil {
			finalInfo := b.isAnnotationFinal(typeAnnotation)

			typeAnnotationNode := typeAnnotation
			if finalInfo.IsFinal && finalInfo.FinalTypeNode == nil {
				typeAnnotationNode = nil
			}

			// Is this annotation indicating that the variable is a "ClassVar"?
			classVarInfo := b.isAnnotationClassVar(typeAnnotation)

			if classVarInfo.IsClassVar && classVarInfo.ClassVarTypeNode == nil {
				typeAnnotationNode = nil
			}

			// PEP 591 indicates that a Final variable initialized within a class
			// body should also be considered a ClassVar unless it's in a
			// dataclass. We can't tell at this stage whether it's a dataclass,
			// so we'll simply record whether it's a Final assigned in a class
			// body.
			isFinalAssignedInClassBody := false
			if finalInfo.IsFinal {
				containingClass := GetEnclosingClassOrFunction(target)
				if containingClass != nil && containingClass.GetNodeType() == parser.ParseNodeTypeClass {
					// Make sure it's part of an assignment.
					if isAssignmentOrChildOfAssignment(target) {
						isFinalAssignedInClassBody = true
					}
				}
			}

			symbolWithScope.Symbol.AddDeclaration(&VariableDeclaration{
				DeclarationBase: DeclarationBase{
					Type:            DeclarationTypeVariable,
					Node:            target,
					Uri:             b.fileInfo.FileUri,
					Range:           common.ConvertTextRangeToRange(typed.GetRange(), b.fileInfo.Lines),
					ModuleName:      b.fileInfo.ModuleName,
					IsInExceptSuite: b.isInExceptSuite,
				},
				IsConstant: IsConstantName(typed.D.Value),
				IsFinal:    finalInfo.IsFinal,
				// Unconditionally set here, unlike in the inferred-assignment
				// path where it is gated on isPossibleTypeAlias.
				TypeAliasName:      typed,
				TypeAnnotationNode: typeAnnotationNode,
				DocString:          b.getVariableDocString(target),
				IsExplicitBinding:  b.currentScope.GetBindingType(typed.D.Value) != NameBindingTypeNone,
			})

			if isFinalAssignedInClassBody {
				symbolWithScope.Symbol.SetIsFinalVarInClassBody()
			}

			if classVarInfo.IsClassVar {
				symbolWithScope.Symbol.SetIsClassVar()
			} else if !isFinalAssignedInClassBody {
				symbolWithScope.Symbol.SetIsInstanceMember()
			}

			// Look for an 'InitVar' either by itself or wrapped in an
			// 'Annotated'.
			if index, ok := typeAnnotation.(*parser.IndexNode); ok {
				if b.isDataclassesAnnotation(index.D.LeftExpr, "InitVar") {
					symbolWithScope.Symbol.SetIsInitVar()
				} else if b.isTypingAnnotation(index.D.LeftExpr, "Annotated") && len(index.D.Items) > 0 {
					if item0Index, ok := index.D.Items[0].D.ValueExpr.(*parser.IndexNode); ok {
						if b.isDataclassesAnnotation(item0Index.D.LeftExpr, "InitVar") {
							symbolWithScope.Symbol.SetIsInitVar()
						}
					}
				}
			}

			if b.isDataclassesAnnotation(typeAnnotation, "KW_ONLY") {
				symbolWithScope.Symbol.SetIsDataClassKeywordOnly()
			} else if index, ok := typeAnnotation.(*parser.IndexNode); ok &&
				b.isTypingAnnotation(index.D.LeftExpr, "Annotated") &&
				len(index.D.Items) > 0 &&
				b.isDataclassesAnnotation(index.D.Items[0].D.ValueExpr, "KW_ONLY") {
				symbolWithScope.Symbol.SetIsDataClassKeywordOnly()
			}
		}

		declarationHandled = true

	case *parser.MemberAccessNode:
		// We need to determine whether this expression is declaring a class or
		// instance variable. This is difficult because python doesn't provide a
		// keyword for accessing "this". Instead, it uses naming conventions of
		// "cls" and "self", but we don't want to rely on these naming
		// conventions here. Instead, we'll apply some heuristics to determine
		// whether the symbol on the LHS is a reference to the current class or
		// an instance of the current class.
		accessInfo := b.getMemberAccessInfo(typed)
		if accessInfo != nil {
			name := typed.D.Member

			symbol := accessInfo.ClassScope.LookUpSymbol(name.D.Value)
			if symbol == nil {
				symbol = accessInfo.ClassScope.AddSymbol(name.D.Value, SymbolFlagsInitiallyUnbound)
				honorPrivateNaming := b.fileInfo.DiagnosticRuleSet.ReportPrivateUsage != DiagnosticLevelNone
				if IsPrivateOrProtectedName(name.D.Value) && honorPrivateNaming {
					symbol.SetIsPrivateMember()
				}
			}

			if accessInfo.IsInstanceMember {
				symbol.SetIsInstanceMember()
			} else {
				symbol.SetIsClassMember()
			}

			finalInfo := b.isAnnotationFinal(typeAnnotation)
			typeAnnotationNode := typeAnnotation
			if finalInfo.IsFinal && finalInfo.FinalTypeNode == nil {
				typeAnnotationNode = nil
			}

			symbol.AddDeclaration(&VariableDeclaration{
				DeclarationBase: DeclarationBase{
					Type:            DeclarationTypeVariable,
					Node:            typed.D.Member,
					Uri:             b.fileInfo.FileUri,
					Range:           common.ConvertTextRangeToRange(typed.D.Member.GetRange(), b.fileInfo.Lines),
					ModuleName:      b.fileInfo.ModuleName,
					IsInExceptSuite: b.isInExceptSuite,
				},
				IsConstant:              IsConstantName(name.D.Value),
				IsDefinedByMemberAccess: true,
				IsFinal:                 finalInfo.IsFinal,
				TypeAnnotationNode:      typeAnnotationNode,
				DocString:               b.getVariableDocString(target),
			})

			declarationHandled = true
		}
	}

	if !declarationHandled {
		b.addDiagnostic(
			DiagnosticRuleReportInvalidTypeForm,
			localization.LocMessage.AnnotationNotSupported(),
			typeAnnotation.GetRange(),
		)
	}
}

// isAssignmentOrChildOfAssignment corresponds to
// `target.parent?.nodeType === Assignment || target.parent?.parent?.nodeType === Assignment`.
func isAssignmentOrChildOfAssignment(target parser.ParseNode) bool {
	parent := target.NodeBase().Parent
	if parent == nil {
		return false
	}
	if parent.GetNodeType() == parser.ParseNodeTypeAssignment {
		return true
	}
	grandparent := parent.NodeBase().Parent
	return grandparent != nil && grandparent.GetNodeType() == parser.ParseNodeTypeAssignment
}

// isTypingAnnotation corresponds to _isTypingAnnotation. It determines whether
// the expression refers to a type exported by the typing or typing_extensions
// modules. We can directly evaluate the types at binding time. We assume here
// that the code isn't making use of some custom type alias to refer to the
// typing types.
func (b *Binder) isTypingAnnotation(typeAnnotation parser.ExpressionNode, name string) bool {
	return isKnownAnnotation(typeAnnotation, name, b.typingImportAliases, b.typingSymbolAliases)
}

// isDataclassesAnnotation corresponds to _isDataclassesAnnotation.
func (b *Binder) isDataclassesAnnotation(typeAnnotation parser.ExpressionNode, name string) bool {
	return isKnownAnnotation(typeAnnotation, name, b.dataclassesImportAliases, b.dataclassesSymbolAliases)
}

// isKnownAnnotation corresponds to _isKnownAnnotation.
func isKnownAnnotation(
	typeAnnotation parser.ExpressionNode,
	name string,
	importAliases []string,
	symbolAliases *common.OrderedMap[string, string],
) bool {
	annotationNode := typeAnnotation

	// Is this a quoted annotation?
	if stringList, ok := annotationNode.(*parser.StringListNode); ok && stringList.D.Annotation != nil {
		annotationNode = stringList.D.Annotation
	}

	switch typed := annotationNode.(type) {
	case *parser.NameNode:
		alias, ok := symbolAliases.Get(typed.D.Value)
		if ok && alias == name {
			return true
		}

	case *parser.MemberAccessNode:
		if leftName, ok := typed.D.LeftExpr.(*parser.NameNode); ok && typed.D.Member.D.Value == name {
			baseName := leftName.D.Value
			for _, alias := range importAliases {
				if alias == baseName {
					return true
				}
			}
			return false
		}
	}

	return false
}

// getVariableDocString corresponds to _getVariableDocString. It returns nil
// where the TypeScript returns undefined.
func (b *Binder) getVariableDocString(node parser.ExpressionNode) *string {
	docNode := GetVariableDocStringNode(node)
	if docNode == nil {
		return nil
	}

	// A docstring can consist of multiple joined strings in a single
	// expression.
	strings := docNode.D.Strings
	if len(strings) == 1 {
		// Common case.
		value := stringOrFormatValue(strings[0])
		return &value
	}

	joined := ""
	for _, s := range strings {
		joined += stringOrFormatValue(s)
	}
	return &joined
}

// isAnnotationFinal corresponds to _isAnnotationFinal. It determines if the
// specified type annotation expression is a "Final", returning a value
// indicating whether the expression is a "Final" expression and whether it's a
// "raw" Final with no type arguments. A nil typeAnnotation stands in for
// undefined.
func (b *Binder) isAnnotationFinal(typeAnnotation parser.ExpressionNode) finalInfo {
	isFinal := false
	var finalTypeNode parser.ExpressionNode

	if typeAnnotation != nil {
		// Allow Final to be enclosed in ClassVar. Normally, Final implies
		// ClassVar, but this combination is required in the case of dataclasses.
		classVarInfo := b.isAnnotationClassVar(typeAnnotation)
		if classVarInfo.ClassVarTypeNode != nil {
			typeAnnotation = classVarInfo.ClassVarTypeNode
		}

		if b.isTypingAnnotation(typeAnnotation, "Final") {
			isFinal = true
		} else if index, ok := typeAnnotation.(*parser.IndexNode); ok &&
			len(index.D.Items) > 0 &&
			b.isTypingAnnotation(index.D.LeftExpr, "Annotated") {
			return b.isAnnotationFinal(index.D.Items[0].D.ValueExpr)
		} else if index, ok := typeAnnotation.(*parser.IndexNode); ok && len(index.D.Items) == 1 {
			// Recursively call to see if the base expression is "Final".
			inner := b.isAnnotationFinal(index.D.LeftExpr)
			if inner.IsFinal &&
				index.D.Items[0].D.ArgCategory == parser.ArgCategorySimple &&
				index.D.Items[0].D.Name == nil &&
				!index.D.TrailingComma {
				isFinal = true
				finalTypeNode = index.D.Items[0].D.ValueExpr
			}
		}
	}

	return finalInfo{IsFinal: isFinal, FinalTypeNode: finalTypeNode}
}

// isAnnotationClassVar corresponds to _isAnnotationClassVar. It determines if
// the specified type annotation expression is a "ClassVar", returning a value
// indicating whether the expression is a "ClassVar" expression and whether it's
// a "raw" ClassVar with no type arguments. A nil typeAnnotation stands in for
// undefined.
func (b *Binder) isAnnotationClassVar(typeAnnotation parser.ExpressionNode) classVarInfo {
	isClassVar := false
	var classVarTypeNode parser.ExpressionNode

	for typeAnnotation != nil {
		// Is this a quoted annotation?
		if stringList, ok := typeAnnotation.(*parser.StringListNode); ok && stringList.D.Annotation != nil {
			typeAnnotation = stringList.D.Annotation
		}

		if index, ok := typeAnnotation.(*parser.IndexNode); ok &&
			len(index.D.Items) > 0 &&
			b.isTypingAnnotation(index.D.LeftExpr, "Annotated") {
			typeAnnotation = index.D.Items[0].D.ValueExpr
		} else if b.isTypingAnnotation(typeAnnotation, "ClassVar") {
			isClassVar = true
			break
		} else if index, ok := typeAnnotation.(*parser.IndexNode); ok && len(index.D.Items) == 1 {
			// Recursively call to see if the base expression is "ClassVar".
			inner := b.isAnnotationClassVar(index.D.LeftExpr)
			if inner.IsClassVar &&
				index.D.Items[0].D.ArgCategory == parser.ArgCategorySimple &&
				index.D.Items[0].D.Name == nil &&
				!index.D.TrailingComma {
				isClassVar = true
				classVarTypeNode = index.D.Items[0].D.ValueExpr
			}
			break
		} else {
			break
		}
	}

	return classVarInfo{IsClassVar: isClassVar, ClassVarTypeNode: classVarTypeNode}
}

// getMemberAccessInfo corresponds to _getMemberAccessInfo. It determines
// whether a member access expression is referring to a member of a class
// (either a class or instance member). This will typically take the form
// "self.x" or "cls.x". It returns nil where the TypeScript returns undefined.
func (b *Binder) getMemberAccessInfo(node *parser.MemberAccessNode) *memberAccessInfo {
	// We handle only simple names on the left-hand side of the expression, not
	// calls, nested member accesses, index expressions, etc.
	leftName, ok := node.D.LeftExpr.(*parser.NameNode)
	if !ok {
		return nil
	}

	leftSymbolName := leftName.D.Value

	// Make sure the expression is within a function (i.e. a method) that's
	// within a class definition.
	methodNode := GetEnclosingFunction(node)
	if methodNode == nil {
		return nil
	}

	classNode := GetEnclosingClass(methodNode, true /* stopAtFunction */)
	if classNode == nil {
		return nil
	}

	// Determine whether the left-hand side indicates a class or instance
	// member.
	isInstanceMember := false

	if len(methodNode.D.Params) < 1 || methodNode.D.Params[0].D.Name == nil {
		return nil
	}

	className := classNode.D.Name.D.Value
	firstParamName := methodNode.D.Params[0].D.Name.D.Value

	if leftSymbolName == className {
		isInstanceMember = false
	} else {
		if leftSymbolName != firstParamName {
			return nil
		}

		// To determine whether the first parameter of the method refers to the
		// class or the instance, we need to apply some heuristics.
		implicitClassMethods := []string{"__new__", "__init_subclass__", "__class_getitem__"}
		isImplicitClassMethod := false
		for _, name := range implicitClassMethods {
			if name == methodNode.D.Name.D.Value {
				isImplicitClassMethod = true
				break
			}
		}

		if isImplicitClassMethod {
			// Several methods are special. They act as class methods even
			// though they don't have a @classmethod decorator.
			isInstanceMember = false
		} else {
			// Assume that it's an instance member unless we find a decorator
			// that tells us otherwise.
			isInstanceMember = true
			for _, decorator := range methodNode.D.Decorators {
				decoratorName := ""

				switch expr := decorator.D.Expr.(type) {
				case *parser.NameNode:
					decoratorName = expr.D.Value
				case *parser.MemberAccessNode:
					if leftExpr, ok := expr.D.LeftExpr.(*parser.NameNode); ok && leftExpr.D.Value == "builtins" {
						decoratorName = expr.D.Member.D.Value
					}
				}

				if decoratorName == "staticmethod" {
					// A static method doesn't have a "self" or "cls" parameter.
					return nil
				} else if decoratorName == "classmethod" {
					// A classmethod implies that the first parameter is "cls".
					isInstanceMember = false
					break
				}
			}
		}
	}

	classScope := GetScope(classNode)
	assert(classScope != nil, "")

	return &memberAccessInfo{
		ClassNode:        classNode,
		MethodNode:       methodNode,
		ClassScope:       classScope,
		IsInstanceMember: isInstanceMember,
	}
}

// handleTypingStubAssignmentOrAnnotation corresponds to
// _handleTypingStubAssignmentOrAnnotation. It handles some special-case
// assignment statements that are found within the typings.pyi file. node is an
// AssignmentNode or TypeAnnotationNode.
func (b *Binder) handleTypingStubAssignmentOrAnnotation(node parser.ParseNode) bool {
	if !b.fileInfo.IsTypingStubFile {
		return false
	}

	var annotationNode *parser.TypeAnnotationNode

	if typed, ok := node.(*parser.TypeAnnotationNode); ok {
		annotationNode = typed
	} else {
		assignment := node.(*parser.AssignmentNode)
		typed, ok := assignment.D.LeftExpr.(*parser.TypeAnnotationNode)
		if !ok {
			return false
		}

		annotationNode = typed
	}

	assignedNameNode, ok := annotationNode.D.ValueExpr.(*parser.NameNode)
	if !ok {
		return false
	}

	specialTypes := map[string]bool{
		"Tuple": true, "Generic": true, "Protocol": true, "Callable": true,
		"Type": true, "ClassVar": true, "Final": true, "Literal": true,
		"TypedDict": true, "Union": true, "Optional": true, "Annotated": true,
		"TypeAlias": true, "Concatenate": true, "TypeGuard": true, "Unpack": true,
		"Self": true, "NoReturn": true, "Never": true, "LiteralString": true,
		"OrderedDict": true, "TypeIs": true,
	}

	if !specialTypes[assignedNameNode.D.Value] {
		return false
	}

	specialBuiltInClassDeclaration := &SpecialBuiltInClassDeclaration{
		DeclarationBase: DeclarationBase{
			Type:            DeclarationTypeSpecialBuiltInClass,
			Node:            annotationNode,
			Uri:             b.fileInfo.FileUri,
			Range:           common.ConvertTextRangeToRange(annotationNode.GetRange(), b.fileInfo.Lines),
			ModuleName:      b.fileInfo.ModuleName,
			IsInExceptSuite: b.isInExceptSuite,
		},
	}

	symbol := b.bindNameToScope(b.currentScope, assignedNameNode, nil)
	if symbol != nil {
		symbol.AddDeclaration(specialBuiltInClassDeclaration)
	}

	SetDeclaration(node, specialBuiltInClassDeclaration)
	return true
}

// bindYield corresponds to _bindYield. Exactly one of yieldNode and
// yieldFromNode is non-nil, standing in for `YieldNode | YieldFromNode`.
func (b *Binder) bindYield(yieldNode *parser.YieldNode, yieldFromNode *parser.YieldFromNode) {
	var node parser.ParseNode
	var expr parser.ExpressionNode
	if yieldNode != nil {
		node = yieldNode
		expr = yieldNode.D.Expr
	} else {
		node = yieldFromNode
		expr = yieldFromNode.D.Expr
	}

	functionNode := GetEnclosingFunction(node)

	if functionNode == nil {
		if GetEnclosingLambda(node) == nil {
			b.addSyntaxError(localization.LocMessage.YieldOutsideFunction(), node.GetRange())
		}
	} else if functionNode.D.IsAsync && yieldFromNode != nil {
		// PEP 525 indicates that 'yield from' is not allowed in an async
		// function.
		b.addSyntaxError(localization.LocMessage.YieldFromOutsideAsync(), node.GetRange())
	}

	if b.targetFunctionDeclaration != nil {
		b.targetFunctionDeclaration.YieldStatements = append(b.targetFunctionDeclaration.YieldStatements, node)
		b.targetFunctionDeclaration.IsGenerator = true
	}

	if expr != nil {
		b.Walk(expr)
	}

	SetFlowNode(node, b.currentFlowNode)
}
