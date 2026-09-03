/*
 * checker_classvalidators.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/checker.ts (pyright 1.1.412):
 * _validateFinalClassNotAbstract, _validateSlotsClassVarConflict,
 * _validateDisjointBaseClass, _validateOverloadDecoratorConsistency,
 * _validateOverloadAbstractConsistency and _validateOverloadFinalOverride.
 *
 * Four independent class-level checks, plus the two overload helpers the fourth
 * drives.
 *
 * _validateSlotsClassVarConflict catches `__slots__ = ("x",)` alongside a class
 * body assignment `x = 0`. Both create a `x` descriptor and the second silently
 * wins, so the slot never works. Recognizing it needs the *set* of declarations
 * for the name: one from the slots entry and one from the assignment. A
 * member-access declaration (`self.x = ...`) does not conflict, which is why
 * isDefinedByMemberAccess is excluded, and only a write counts.
 *
 * _validateDisjointBaseClass is about layout, not typing. Two C extension types
 * with incompatible instance layouts cannot be combined, however compatible
 * their Python-level interfaces look. Both filters that skip a base class are
 * safe in the same way and the original says so at each: an unknown base cannot
 * make two already-incompatible known bases compatible. `object` is filtered
 * from the reported names because it is a disjoint base that is compatible with
 * every other one, so naming it would mislead.
 *
 * The two overload helpers both split on whether an implementation exists,
 * because the implementation is what settles the question. With one, it decides
 * whether the function is abstract and the overloads must agree with it; without
 * one, the overloads only have to agree with each other, which is why the second
 * branch compares against overloads[0] rather than against a fixed answer.
 */

package analyzer

import (
	"strings"

	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/localization"
	"github.com/microsoft/pyright/go/parser"
)

// validateFinalClassNotAbstract corresponds to _validateFinalClassNotAbstract.
func (c *Checker) validateFinalClassNotAbstract(classType *ClassType, errorNode *parser.ClassNode) {
	if !ClassTypeIsFinal(classType) || !ClassTypeSupportsAbstractMethods(classType) {
		return
	}

	abstractSymbols := c.evaluator.GetAbstractSymbols(classType)
	if len(abstractSymbols) == 0 {
		return
	}

	diagAddendum := common.NewDiagnosticAddendum()
	const errorsToDisplay = 2

	for index, abstractMethod := range abstractSymbols {
		if index == errorsToDisplay {
			diagAddendum.AddMessage(localization.LocAddendum.MemberIsAbstractMore().
				Format(len(abstractSymbols) - errorsToDisplay))
			continue
		}
		if index > errorsToDisplay {
			continue
		}
		if IsInstantiableClass(abstractMethod.ClassType) {
			diagAddendum.AddMessage(localization.LocAddendum.MemberIsAbstract().Format(
				abstractMethod.ClassType.(*ClassType).Shared.Name, abstractMethod.SymbolName))
		}
	}

	c.evaluator.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
		localization.LocMessage.FinalClassIsAbstract().Format(classType.Shared.Name)+
			diagAddendum.GetString(),
		errorNode.D.Name, nil)
}

// validateSlotsClassVarConflict corresponds to _validateSlotsClassVarConflict.
func (c *Checker) validateSlotsClassVarConflict(classType *ClassType) {
	if classType.Shared.LocalSlotsNames == nil {
		// The original's comment: nothing to check, since this class doesn't use
		// __slots__.
		return
	}

	// The original's comment: don't apply this for dataclasses because their
	// class variables are transformed into instance variables.
	if ClassTypeIsDataClass(classType) {
		return
	}

	symbolTable := ClassTypeGetSymbolTable(classType)
	for _, name := range symbolTable.Keys() {
		symbol, _ := symbolTable.Get(name)
		decls := symbol.GetDeclarations()

		isDefinedBySlots := false
		for _, decl := range decls {
			if varDecl, ok := decl.(*VariableDeclaration); ok && varDecl.IsDefinedBySlots {
				isDefinedBySlots = true
				break
			}
		}
		if !isDefinedBySlots {
			continue
		}

		for _, decl := range decls {
			varDecl, ok := decl.(*VariableDeclaration)
			if !ok || varDecl.IsDefinedBySlots || varDecl.IsDefinedByMemberAccess {
				continue
			}

			if nameNode, ok := varDecl.Node.(*parser.NameNode); ok && IsWriteAccess(nameNode) {
				c.evaluator.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
					localization.LocMessage.SlotsClassVarConflict().Format(name), nameNode, nil)
			}
		}
	}
}

// validateDisjointBaseClass corresponds to _validateDisjointBaseClass.
func (c *Checker) validateDisjointBaseClass(classType *ClassType, errorNode *parser.NameNode) {
	if len(classType.Shared.BaseClasses) < 2 {
		return
	}

	candidates := []*ClassType{}

	for _, baseClass := range classType.Shared.BaseClasses {
		if !IsInstantiableClass(baseClass) {
			// The original's comment: an unknown base may introduce an unknown
			// disjoint base, but it cannot make two already-incompatible known
			// bases compatible, so keep collecting the known candidates.
			continue
		}

		candidate := ClassTypeGetDisjointBase(baseClass.(*ClassType))
		if candidate == nil {
			// The original's comment: the base class is invalid or its disjoint
			// base is unknown; an unknown disjoint base cannot relate two
			// otherwise-incompatible known candidates, so keep collecting.
			continue
		}

		seen := false
		for _, existing := range candidates {
			if ClassTypeIsSameGenericClass(existing, candidate, 0) {
				seen = true
				break
			}
		}
		if !seen {
			candidates = append(candidates, candidate)
		}
	}

	if len(candidates) <= 1 || ClassTypeGetMostDerivedDisjointBase(candidates) != nil {
		return
	}

	// The original's comment: `object` is a disjoint base but is compatible with
	// every other disjoint base, so including it in the reported names would be
	// misleading. Filter it out.
	names := []string{}
	for _, candidate := range candidates {
		if ClassTypeIsBuiltInNamed(candidate, "object") {
			continue
		}
		names = append(names, `"`+candidate.Shared.Name+`"`)
	}

	c.evaluator.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
		localization.LocMessage.DisjointBaseIncompatible().Format(strings.Join(names, ", ")),
		errorNode, nil)
}

// validateOverloadDecoratorConsistency corresponds to
// _validateOverloadDecoratorConsistency.
func (c *Checker) validateOverloadDecoratorConsistency(classType *ClassType) {
	symbolTable := ClassTypeGetSymbolTable(classType)

	for _, name := range symbolTable.Keys() {
		symbol, _ := symbolTable.Get(name)

		primaryDecl := GetLastTypedDeclarationForSymbol(symbol)
		if primaryDecl == nil {
			continue
		}
		if _, isFunc := primaryDecl.(*FunctionDeclaration); !isFunc {
			continue
		}

		typeOfSymbol := c.evaluator.GetEffectiveTypeOfSymbol(symbol)
		if !IsOverloaded(typeOfSymbol) {
			continue
		}

		overloads := OverloadedTypeGetOverloads(typeOfSymbol.(*OverloadedType))
		implementation := OverloadedTypeGetImplementation(typeOfSymbol.(*OverloadedType))

		c.validateOverloadFinalOverride(overloads, implementation)
		c.validateOverloadAbstractConsistency(overloads, implementation)
	}
}

// overloadDeclErrorNode is the original's repeated
// `getNameNodeForDeclaration(decl) ?? decl.node`.
func overloadDeclErrorNode(decl *FunctionDeclaration) parser.ParseNode {
	if nameNode := GetNameNodeForDeclaration(decl); nameNode != nil {
		return nameNode
	}
	return decl.Node
}

// validateOverloadAbstractConsistency corresponds to
// _validateOverloadAbstractConsistency.
func (c *Checker) validateOverloadAbstractConsistency(
	overloads []*FunctionType, implementation Type,
) {
	// The original's comment: if there's an implementation, it will determine
	// whether the function is abstract.
	if implementation != nil && IsFunction(implementation) {
		if FunctionTypeIsAbstractMethod(implementation.(*FunctionType)) {
			return
		}

		for _, overload := range overloads {
			decl := overload.Shared.Declaration
			if FunctionTypeIsAbstractMethod(overload) && decl != nil {
				c.evaluator.AddDiagnostic(DiagnosticRuleReportInconsistentOverload,
					localization.LocMessage.OverloadAbstractImplMismatch().Format(overload.Shared.Name),
					overloadDeclErrorNode(decl), nil)
			}
		}
		return
	}

	if len(overloads) < 2 {
		return
	}

	// The original's comment: if there was no implementation, make sure all
	// overloads are either abstract or not abstract.
	isFirstOverloadAbstract := FunctionTypeIsAbstractMethod(overloads[0])

	for _, overload := range overloads[1:] {
		if FunctionTypeIsAbstractMethod(overload) != isFirstOverloadAbstract &&
			overload.Shared.Declaration != nil {
			c.evaluator.AddDiagnostic(DiagnosticRuleReportInconsistentOverload,
				localization.LocMessage.OverloadAbstractMismatch().Format(overload.Shared.Name),
				overloadDeclErrorNode(overload.Shared.Declaration), nil)
		}
	}
}

// validateOverloadFinalOverride corresponds to _validateOverloadFinalOverride.
func (c *Checker) validateOverloadFinalOverride(overloads []*FunctionType, implementation Type) {
	// The original's comment: if there's an implementation, the overloads are not
	// allowed to be marked final or override.
	if implementation != nil {
		for _, overload := range overloads {
			decl := overload.Shared.Declaration
			if decl == nil || decl.Node == nil {
				continue
			}

			if FunctionTypeIsFinal(overload) {
				c.evaluator.AddDiagnostic(DiagnosticRuleReportInconsistentOverload,
					localization.LocMessage.OverloadFinalImpl(), overloadDeclErrorNode(decl), nil)
			}

			if FunctionTypeIsOverridden(overload) {
				c.evaluator.AddDiagnostic(DiagnosticRuleReportInconsistentOverload,
					localization.LocMessage.OverloadOverrideImpl(), overloadDeclErrorNode(decl), nil)
			}
		}

		return
	}

	// The original's comment: if there's not an implementation, only the first
	// overload can be marked final.
	if len(overloads) == 0 {
		return
	}

	for _, overload := range overloads[1:] {
		decl := overload.Shared.Declaration
		if decl == nil || decl.Node == nil {
			continue
		}

		if FunctionTypeIsFinal(overload) {
			c.evaluator.AddDiagnostic(DiagnosticRuleReportInconsistentOverload,
				localization.LocMessage.OverloadFinalNoImpl(), overloadDeclErrorNode(decl), nil)
		}

		if FunctionTypeIsOverridden(overload) {
			c.evaluator.AddDiagnostic(DiagnosticRuleReportInconsistentOverload,
				localization.LocMessage.OverloadOverrideNoImpl(), overloadDeclErrorNode(decl), nil)
		}
	}
}

// validateMultipleInheritanceBaseClasses corresponds to
// _validateMultipleInheritanceBaseClasses. The original's comment: verifies that
// classes that have more than one base class do not have conflicting type
// arguments.
//
// The conflict it looks for is a shared generic ancestor specialized
// differently by two bases -- `class C(Sequence[int], Sequence[str])`, or the
// same thing reached indirectly. Finding it means specializing each base's MRO
// through that base's own solution and then locating the matching entry in the
// derived class's MRO, because only there are the two specializations of one
// ancestor visible side by side.
func (c *Checker) validateMultipleInheritanceBaseClasses(
	classType *ClassType, errorNode *parser.NameNode,
) {
	// The original's comment: skip this check if the class has only one base
	// class or one or more of the base classes are Any. Note the early *return*
	// on a non-class base rather than a skip: one unknown base abandons the whole
	// check.
	filteredBaseClasses := []*ClassType{}
	for _, baseClass := range classType.Shared.BaseClasses {
		if !IsClass(baseClass) {
			return
		}

		cls := baseClass.(*ClassType)
		if !ClassTypeIsBuiltInNamed(cls, "Generic", "Protocol", "object") {
			filteredBaseClasses = append(filteredBaseClasses, cls)
		}
	}

	if len(filteredBaseClasses) < 2 {
		return
	}

	diagAddendum := common.NewDiagnosticAddendum()

	for _, baseClass := range filteredBaseClasses {
		solution := BuildSolutionFromSpecializedClass(baseClass)

		for _, baseClassMroClass := range baseClass.Shared.Mro {
			// The original's comment: there's no need to check for conflicts if
			// this class isn't generic.
			if !IsClass(baseClassMroClass) ||
				len(baseClassMroClass.(*ClassType).Shared.TypeParams) == 0 {
				continue
			}

			specialized, ok := ApplySolvedTypeVars(baseClassMroClass, solution, nil).(*ClassType)
			if !ok {
				continue
			}

			// The original's comment: find the corresponding class in the derived
			// class's MRO list.
			var matchingMroClass Type
			for _, mroClass := range classType.Shared.Mro {
				if IsClass(mroClass) &&
					ClassTypeIsSameGenericClass(mroClass.(*ClassType), specialized, 0) {
					matchingMroClass = mroClass
					break
				}
			}

			if matchingMroClass == nil || !IsInstantiableClass(matchingMroClass) {
				continue
			}

			scopeIds := GetTypeVarScopeIds(classType)
			matchingMroObject := MakeTypeVarsBound(
				ClassTypeCloneAsInstance(matchingMroClass.(*ClassType), true), scopeIds, true)
			baseClassMroObject := MakeTypeVarsBound(
				ClassTypeCloneAsInstance(specialized, true), scopeIds, true)

			if c.evaluator.AssignType(matchingMroObject, baseClassMroObject,
				nil, nil, AssignTypeFlagsDefault, 0) {
				continue
			}

			diag := common.NewDiagnosticAddendum()
			baseClassObject := ConvertToInstance(baseClass, true)

			if IsTypeSame(baseClassObject, baseClassMroObject, TypeSameOptions{}, 0) {
				diag.AddMessage(localization.LocAddendum.BaseClassIncompatible().Format(
					c.evaluator.PrintType(baseClassObject, nil),
					c.evaluator.PrintType(matchingMroObject, nil)))
			} else {
				diag.AddMessage(localization.LocAddendum.BaseClassIncompatibleSubclass().Format(
					c.evaluator.PrintType(baseClassObject, nil),
					c.evaluator.PrintType(baseClassMroObject, nil),
					c.evaluator.PrintType(matchingMroObject, nil)))
			}

			diagAddendum.AddAddendum(diag)

			// The original's comment: break out of the inner loop so we don't
			// report any redundant errors for this base class.
			break
		}
	}

	if !diagAddendum.IsEmpty() {
		c.evaluator.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
			localization.LocMessage.BaseClassIncompatible().Format(classType.Shared.Name)+
				diagAddendum.GetString(),
			errorNode, nil)
	}
}
