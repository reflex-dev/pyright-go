/*
 * checker_names.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/checker.ts (pyright 1.1.412):
 * _reportUnboundName, _conditionallyReportPrivateUsage,
 * _reportDeprecatedDiagnostic, _reportDeprecatedUseForMemberAccess,
 * _reportDeprecatedUseForOperation, _validateEnumClassOverride and
 * _validateTypedDictClassSuite.
 *
 * The first two run on every name in the file, which is why they lead with
 * cheap rejections -- the rule being off, a stub file, a keyword-argument name
 * -- before asking the evaluator for anything.
 *
 * _conditionallyReportPrivateUsage is the one with real structure. Python has no
 * enforced access control, so "private" here is a naming convention with two
 * tiers, and the rules differ: a `_protected` name may be read from a subclass
 * of the class that declared it, while a `__private` name may not. Both are
 * judged by *where the declaration lives*, not where the reference does, which
 * is why the function walks from the declaration outward to find its class and
 * then asks whether the reference is inside it.
 *
 * Two exemptions are easy to miss. An import alias that renames the symbol
 * (`from m import _x as y`) is exempt, because the local name is the author's
 * choice. And a member declared in a stub file is exempt even when named
 * privately, because a stub's contents are its public contract.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/localization"
	"github.com/microsoft/pyright/go/parser"
)

// isKeywordArgumentName is the original's repeated
// `node.parent?.nodeType === ParseNodeType.Argument && node.parent.d.name === node`.
func isKeywordArgumentName(node *parser.NameNode) bool {
	argNode, ok := node.NodeBase().Parent.(*parser.ArgumentNode)
	return ok && argNode.D.Name == node
}

// reportUnboundName corresponds to _reportUnboundName.
func (c *Checker) reportUnboundName(node *parser.NameNode) {
	if c.fileInfo.DiagnosticRuleSet.ReportUnboundVariable == DiagnosticLevelNone {
		return
	}

	// The original's comment: skip this for keyword argument names.
	if isKeywordArgumentName(node) {
		return
	}

	if IsCodeUnreachable(node) {
		return
	}

	t := c.evaluator.GetType(node)
	if t == nil {
		return
	}

	if IsUnbound(t) {
		if c.evaluator.IsNodeReachable(node, nil) {
			c.evaluator.AddDiagnostic(DiagnosticRuleReportUnboundVariable,
				localization.LocMessage.SymbolIsUnbound().Format(node.D.Value), node, nil)
		}
		return
	}

	if IsPossiblyUnbound(t) {
		c.evaluator.AddDiagnostic(DiagnosticRuleReportPossiblyUnboundVariable,
			localization.LocMessage.SymbolIsPossiblyUnbound().Format(node.D.Value), node, nil)
	}
}

// conditionallyReportPrivateUsage corresponds to _conditionallyReportPrivateUsage.
func (c *Checker) conditionallyReportPrivateUsage(node *parser.NameNode) {
	if c.fileInfo.DiagnosticRuleSet.ReportPrivateUsage == DiagnosticLevelNone {
		return
	}

	// The original's comment: ignore privates in type stubs.
	if c.fileInfo.IsStubFile {
		return
	}

	// The original's comment: ignore privates in named arguments.
	if isKeywordArgumentName(node) {
		return
	}

	nameValue := node.D.Value
	isPrivateName := IsPrivateName(nameValue)
	isProtectedName := IsProtectedName(nameValue)

	// The original's comment: if it's not a protected or private name, don't
	// bother with any further checks.
	if !isPrivateName && !isProtectedName {
		return
	}

	// The original's comment: get the declarations for this name node, but filter
	// out any variable declarations that are bound using nonlocal or global
	// explicit bindings.
	declInfo := c.evaluator.GetDeclInfoForNameNode(node, nil)
	var declarations []Declaration
	if declInfo != nil {
		for _, decl := range declInfo.Decls {
			if varDecl, ok := decl.(*VariableDeclaration); ok && varDecl.IsExplicitBinding {
				continue
			}
			declarations = append(declarations, decl)
		}
	}

	var primaryDeclaration Declaration
	if len(declarations) > 0 {
		primaryDeclaration = declarations[len(declarations)-1]
	}
	if primaryDeclaration == nil || primaryDeclaration.DeclBase().Node == parser.ParseNode(node) {
		return
	}

	if aliasDecl, ok := primaryDeclaration.(*AliasDeclaration); ok {
		// The original's comment: if this symbol is an import alias (i.e. it's a
		// local name rather than the original imported name), skip the private
		// check.
		if aliasDecl.UsesLocalName {
			return
		}

		resolvedAliasInfo := c.evaluator.ResolveAliasDeclarationWithInfo(aliasDecl, true, nil)
		if resolvedAliasInfo == nil {
			return
		}

		primaryDeclaration = resolvedAliasInfo.Declaration

		// The original's comment: if the alias resolved to a stub file or py.typed
		// source file and the declaration is marked "externally visible", it is
		// exempt from private usage checks.
		if !resolvedAliasInfo.IsPrivate {
			return
		}
	}

	if primaryDeclaration == nil || primaryDeclaration.DeclBase().Node == parser.ParseNode(node) {
		return
	}

	var classNode *parser.ClassNode
	if primaryDeclaration.DeclBase().Node != nil {
		classNode = GetEnclosingClass(primaryDeclaration.DeclBase().Node, false)
	}

	// The original's comment: if this is the name of a class, find the class that
	// contains it rather than constraining the use of the class name within the
	// class itself.
	declNode := primaryDeclaration.DeclBase().Node
	if declNode != nil && declNode.NodeBase().Parent != nil && classNode != nil &&
		declNode.NodeBase().Parent == parser.ParseNode(classNode) {
		classNode = GetEnclosingClass(classNode, false)
	}

	// The original's comment: if it's a class member, check whether it's a legal
	// protected access.
	isProtectedAccess := false
	if classNode != nil && isProtectedName {
		declClassTypeInfo := c.evaluator.GetTypeOfClass(classNode)
		if declClassTypeInfo != nil && IsInstantiableClass(declClassTypeInfo.DecoratedType) {
			declClass := declClassTypeInfo.DecoratedType.(*ClassType)

			// The original's comment: if it's a member defined in a stub file,
			// we'll assume that it's part of the public contract even if it's
			// named as though it's private.
			if ClassTypeIsDefinedInStub(declClass) {
				return
			}

			// The original's comment: note that the access is to a protected class
			// member.
			isProtectedAccess = true

			if enclosingClassNode := GetEnclosingClass(node, false); enclosingClassNode != nil {
				enclosingClassTypeInfo := c.evaluator.GetTypeOfClass(enclosingClassNode)

				// The original's comment: if the referencing class is a subclass of
				// the declaring class, it's allowed to access a protected name.
				if enclosingClassTypeInfo != nil &&
					IsInstantiableClass(enclosingClassTypeInfo.DecoratedType) {
					if DerivesFromClassRecursive(
						enclosingClassTypeInfo.DecoratedType.(*ClassType), declClass, true) {
						return
					}
				}
			}
		}
	}

	if classNode == nil || IsNodeContainedWithin(node, classNode) {
		return
	}

	message := localization.LocMessage.PrivateUsedOutsideOfClass().Format(nameValue)
	if isProtectedAccess {
		message = localization.LocMessage.ProtectedUsedOutsideOfClass().Format(nameValue)
	}

	c.evaluator.AddDiagnostic(DiagnosticRuleReportPrivateUsage, message, node, nil)
}

// reportDeprecatedDiagnostic corresponds to _reportDeprecatedDiagnostic. When
// the rule is off the message still surfaces, as a "deprecated" hint rather than
// a diagnostic -- which is how an editor greys out a deprecated symbol without
// the rule being enabled.
func (c *Checker) reportDeprecatedDiagnostic(
	node parser.ParseNode, diagnosticMessage string, deprecatedMessage string,
) {
	diag := common.NewDiagnosticAddendum()
	if deprecatedMessage != "" {
		diag.AddMessage(deprecatedMessage)
	}

	if c.fileInfo.DiagnosticRuleSet.ReportDeprecated == DiagnosticLevelNone {
		c.evaluator.AddDeprecated(diagnosticMessage+diag.GetString(), node)
		return
	}

	c.evaluator.AddDiagnostic(DiagnosticRuleReportDeprecated,
		diagnosticMessage+diag.GetString(), node, nil)
}

// reportDeprecatedUseForMemberAccess corresponds to
// _reportDeprecatedUseForMemberAccess.
func (c *Checker) reportDeprecatedUseForMemberAccess(
	node *parser.NameNode, info *MemberAccessDeprecationInfo,
) {
	errorMessage := ""

	switch info.AccessType {
	case "property":
		switch info.AccessMethod {
		case "get":
			errorMessage = localization.LocMessage.DeprecatedPropertyGetter().Format(node.D.Value)
		case "set":
			errorMessage = localization.LocMessage.DeprecatedPropertySetter().Format(node.D.Value)
		default:
			errorMessage = localization.LocMessage.DeprecatedPropertyDeleter().Format(node.D.Value)
		}
	case "descriptor":
		switch info.AccessMethod {
		case "get":
			errorMessage = localization.LocMessage.DeprecatedDescriptorGetter().Format(node.D.Value)
		case "set":
			errorMessage = localization.LocMessage.DeprecatedDescriptorSetter().Format(node.D.Value)
		default:
			errorMessage = localization.LocMessage.DeprecatedDescriptorDeleter().Format(node.D.Value)
		}
	}

	if errorMessage != "" {
		c.reportDeprecatedDiagnostic(node, errorMessage, info.DeprecatedMessage)
	}
}

// reportDeprecatedUseForOperation corresponds to
// _reportDeprecatedUseForOperation.
func (c *Checker) reportDeprecatedUseForOperation(
	node parser.ExpressionNode, typeResult *TypeResult,
) {
	if typeResult == nil || typeResult.MagicMethodDeprecationInfo == nil {
		return
	}

	info := typeResult.MagicMethodDeprecationInfo
	c.reportDeprecatedDiagnostic(node,
		localization.LocMessage.DeprecatedMethod().Format(info.MethodName, info.ClassName),
		info.DeprecatedMessage)
}

// validateEnumClassOverride corresponds to _validateEnumClassOverride. The
// original's comment: validates that an enum class does not attempt to override
// another enum class that has already defined values.
func (c *Checker) validateEnumClassOverride(node *parser.ClassNode, classType *ClassType) {
	for index, baseClass := range classType.Shared.BaseClasses {
		if !IsClass(baseClass) || !IsEnumClassWithMembers(c.evaluator, baseClass.(*ClassType)) {
			continue
		}

		if index >= len(node.D.Arguments) {
			// The original indexes node.d.arguments[index] against the base-class
			// list, which can be longer when a base class is synthesized.
			continue
		}

		c.evaluator.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
			localization.LocMessage.EnumClassOverride().Format(baseClass.(*ClassType).Shared.Name),
			node.D.Arguments[index], nil)
	}
}

// validateTypedDictClassSuite corresponds to _validateTypedDictClassSuite. The
// original's comment: verifies the rules specified for TypedDict class bodies.
// They cannot have statements other than type annotations, doc strings, "pass"
// statements, ellipses, and statically evaluable if statements.
func (c *Checker) validateTypedDictClassSuite(suiteNode *parser.SuiteNode) {
	emitBadStatementError := func(node parser.ParseNode) {
		c.evaluator.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
			localization.LocMessage.TypedDictBadVar(), node, nil)
	}

	var validateStatement func(statement parser.StatementNode)
	var validateSuite func(suite *parser.SuiteNode)

	validateStatement = func(statement parser.StatementNode) {
		if IsCodeUnreachable(statement) {
			return
		}

		if stmtList, ok := statement.(*parser.StatementListNode); ok {
			for _, substatement := range stmtList.D.Statements {
				switch substatement.GetNodeType() {
				case parser.ParseNodeTypeTypeAnnotation, parser.ParseNodeTypeEllipsis,
					parser.ParseNodeTypeStringList, parser.ParseNodeTypePass:
				default:
					emitBadStatementError(substatement)
				}
			}
			return
		}

		if ifNode, ok := statement.(*parser.IfNode); ok {
			conditionValue := GetStaticConditionValue(ifNode)
			if conditionValue == nil {
				emitBadStatementError(statement)
				return
			}

			reachableSuite := ifNode.D.ElseSuite
			if *conditionValue {
				reachableSuite = ifNode.D.IfSuite
			}

			if nestedIf, ok := reachableSuite.(*parser.IfNode); ok {
				validateStatement(nestedIf)
			} else if suite, ok := reachableSuite.(*parser.SuiteNode); ok {
				validateSuite(suite)
			}

			return
		}

		emitBadStatementError(statement)
	}

	validateSuite = func(suite *parser.SuiteNode) {
		for _, statement := range suite.D.Statements {
			validateStatement(statement)
		}
	}

	validateSuite(suiteNode)
}
