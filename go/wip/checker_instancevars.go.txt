/*
 * checker_instancevars.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/checker.ts (pyright 1.1.412):
 * _validateInstanceVariableInitialization. The original's comment: reports the
 * case where an instance variable is not declared or initialized within the
 * class body or constructor method.
 *
 * "Initialized" here is looser than it sounds, and the set of things that count
 * is the substance of the check. A declaration inside the class body counts if
 * it is part of an assignment; a bare annotation does not. A declaration inside
 * `__init__` counts wherever it appears. A declaration in a class that is a
 * dataclass, a NamedTuple or a TypedDict counts even as a bare annotation,
 * because in each of those the class variable becomes an instance variable by
 * synthesis. And a declaration anywhere outside a class or function counts,
 * because there is no enclosing scope to have initialized it in.
 *
 * The second half is a separate question with the same diagnostic: a final class
 * inheriting from an abstract base must initialize the *base's* variables, since
 * nothing else will. Those are collected up front and then deleted as this
 * class's own symbols are walked, so what remains is exactly the uninitialized
 * inherited set. A dataclass field is exempt when it is included in __init__,
 * since the synthesized constructor assigns it.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/localization"
	"github.com/microsoft/pyright/go/parser"
)

// validateInstanceVariableInitialization corresponds to
// _validateInstanceVariableInitialization.
func (c *Checker) validateInstanceVariableInitialization(
	node *parser.ClassNode, classType *ClassType,
) {
	// The original's comment: this check doesn't apply to stub files.
	if c.fileInfo.IsStubFile {
		return
	}

	// The original's comment: this check can be expensive, so don't perform it if
	// the corresponding rule is disabled.
	if c.fileInfo.DiagnosticRuleSet.ReportUninitializedInstanceVariable == DiagnosticLevelNone {
		return
	}

	// The original's comment: protocol classes and ABCs are exempted from this
	// check unless they are marked @final.
	if ClassTypeIsProtocolClass(classType) ||
		(ClassTypeSupportsAbstractMethods(classType) && !ClassTypeIsFinal(classType)) {
		return
	}

	// The original's comment: if the class is final, see if it has any abstract
	// base classes that define variables. We need to make sure these are
	// initialized.
	abstractSymbols := common.NewOrderedMap[string, *ClassMember]()
	if ClassTypeIsFinal(classType) {
		GetProtocolSymbolsRecursive(classType, abstractSymbols,
			ClassTypeFlagsSupportsAbstractMethods, 0)
	}

	// The original's comment: if this is a dataclass, get all of the entries so we
	// can tell which ones are initialized by the synthesized __init__ method.
	dataClassEntries := []*DataClassEntry{}
	if ClassTypeIsDataClass(classType) {
		AddInheritedDataClassEntries(classType, &dataClassEntries)
	}

	symbolTable := ClassTypeGetSymbolTable(classType)
	for _, name := range symbolTable.Keys() {
		localSymbol, _ := symbolTable.Get(name)
		abstractSymbols.Delete(name)

		// The original's comment: this applies only to instance members.
		if !localSymbol.IsInstanceMember() {
			continue
		}

		decls := localSymbol.GetDeclarations()

		if c.isInstanceVarInitialized(decls, classType, name) {
			continue
		}

		// The original's comment: if the symbol is declared by its parent, we can
		// assume it is initialized there.
		if LookUpClassMember(classType, name, MemberAccessFlagsSkipOriginalClass, nil) != nil {
			continue
		}

		if len(decls) == 0 {
			continue
		}

		// The original's comment: report the variable as uninitialized only on the
		// first decl.
		c.evaluator.AddDiagnostic(DiagnosticRuleReportUninitializedInstanceVariable,
			localization.LocMessage.UninitializedInstanceVariable().Format(name),
			decls[0].DeclBase().Node, nil)
	}

	c.reportUninitializedAbstractVariables(node, classType, abstractSymbols, dataClassEntries)
}

// isInstanceVarInitialized is the original's `decls.find(...)` predicate: does
// any declaration of this name count as initializing it?
func (c *Checker) isInstanceVarInitialized(
	decls []Declaration, classType *ClassType, name string,
) bool {
	for _, decl := range decls {
		containingClass := GetEnclosingClassOrFunction(decl.DeclBase().Node)
		if containingClass == nil {
			// Nothing encloses it, so there is no constructor that failed to
			// initialize it.
			return true
		}

		if classNode, ok := containingClass.(*parser.ClassNode); ok {
			_ = classNode

			parent := decl.DeclBase().Node.NodeBase().Parent

			// The original's comment: if this is part of an assignment statement,
			// assume it has been initialized as a class variable.
			if parent != nil && parent.GetNodeType() == parser.ParseNodeTypeAssignment {
				return true
			}

			if parent != nil && parent.GetNodeType() == parser.ParseNodeTypeTypeAnnotation &&
				parent.NodeBase().Parent != nil &&
				parent.NodeBase().Parent.GetNodeType() == parser.ParseNodeTypeAssignment {
				return true
			}

			// The original's comment: if this is part of a dataclass, a class
			// handled by a dataclass_transform, or a NamedTuple, exempt it because
			// the class variable will be transformed into an instance variable in
			// this case.
			if ClassTypeIsDataClass(classType) || ClassTypeHasNamedTupleEntry(classType, name) {
				return true
			}

			// The original's comment: if this is part of a TypedDict, exempt it
			// because the class variables are not actually class variables in a
			// TypedDict.
			if ClassTypeIsTypedDictClass(classType) {
				return true
			}

			continue
		}

		if fnNode, ok := containingClass.(*parser.FunctionNode); ok {
			if fnNode.D.Name.D.Value == "__init__" {
				return true
			}
		}
	}

	return false
}

// reportUninitializedAbstractVariables is the original's second half: variables
// inherited from an abstract base that nothing initializes.
func (c *Checker) reportUninitializedAbstractVariables(
	node *parser.ClassNode,
	classType *ClassType,
	abstractSymbols *common.OrderedMap[string, *ClassMember],
	dataClassEntries []*DataClassEntry,
) {
	diagAddendum := common.NewDiagnosticAddendum()

	for _, name := range abstractSymbols.Keys() {
		member, _ := abstractSymbols.Get(name)
		decls := member.Symbol.GetDeclarations()

		if len(decls) == 0 || !IsClass(member.ClassType) {
			continue
		}

		if _, ok := decls[0].(*VariableDeclaration); !ok {
			continue
		}

		// The original's comment: dataclass fields are typically exempted from
		// this check because they have synthesized __init__ methods that
		// initialize these variables.
		var dcEntry *DataClassEntry
		for _, entry := range dataClassEntries {
			if entry.Name == name {
				dcEntry = entry
				break
			}
		}

		if dcEntry != nil {
			if dcEntry.IncludeInInit {
				continue
			}
		} else {
			// The original's comment: do one or more declarations involve
			// assignments?
			assigned := false
			for _, decl := range decls {
				if varDecl, ok := decl.(*VariableDeclaration); ok && varDecl.InferredTypeSource != nil {
					assigned = true
					break
				}
			}
			if assigned {
				continue
			}
		}

		diagAddendum.AddMessage(localization.LocAddendum.UninitializedAbstractVariable().
			Format(name, member.ClassType.(*ClassType).Shared.Name))
	}

	if !diagAddendum.IsEmpty() {
		c.evaluator.AddDiagnostic(DiagnosticRuleReportUninitializedInstanceVariable,
			localization.LocMessage.UninitializedAbstractVariables().Format(classType.Shared.Name)+
				diagAddendum.GetString(),
			node.D.Name, nil)
	}
}
