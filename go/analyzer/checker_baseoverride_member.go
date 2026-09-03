/*
 * checker_baseoverride_member.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/checker.ts (pyright 1.1.412):
 * _validateBaseClassOverride and _validatePropertyOverride.
 *
 * This is the per-member half of the override check: given one symbol in a
 * subclass and the corresponding symbol found in a base class, decide whether
 * the override is legal. It splits three ways on what the *base* declares --
 * method, property, or plain variable -- and each arm has a different notion of
 * compatibility.
 *
 * Methods are checked with validateOverrideMethod, which is contravariant in
 * parameters and covariant in the return type. Properties are checked one
 * accessor at a time, because a subclass may legally narrow the getter while
 * keeping the setter, and because dropping an accessor the base provides is its
 * own distinct error. Variables are checked with plain assignability, but
 * *invariantly*: a mutable attribute typed `int` in the base cannot become
 * `bool` in the subclass, since a caller holding the base type could assign an
 * arbitrary int through it.
 *
 * Three exceptions to that invariance are worth naming, since each looks
 * arbitrary in isolation:
 *
 *   - A `Final` variable is not invariant, because nothing can write to it.
 *   - A member of a *frozen dataclass* is not invariant, for the same reason --
 *     the frozen decorator makes every field write-once. This is the branch that
 *     reads Shared.DataClassEntries, and it only became reachable once
 *     dataClasses.synthesizeDataClassMethods started populating that field.
 *   - A read-only TypedDict item is not invariant, again because it cannot be
 *     written through.
 *
 * The Self handling at the top is not decoration. Both types are specialized
 * against the *child* class's Self before comparison, except that a protocol
 * base also uses the child's Self -- the original's comment flags this as not
 * clearly specified. Without it, a base method returning `Self` and an override
 * returning `Self` would compare as two unrelated type variables.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/localization"
	"github.com/microsoft/pyright/go/parser"
)

// validateBaseClassOverride corresponds to _validateBaseClassOverride.
func (c *Checker) validateBaseClassOverride(
	baseClassAndSymbol *ClassMember,
	overrideSymbol *Symbol,
	overrideType Type,
	childClassType *ClassType,
	memberName string,
) {
	if !IsInstantiableClass(baseClassAndSymbol.ClassType) {
		return
	}

	if baseClassAndSymbol.Symbol.IsIgnoredForOverrideChecks() || overrideSymbol.IsIgnoredForOverrideChecks() {
		return
	}

	// The original's comment: if the base class doesn't provide a type
	// declaration, we won't bother proceeding with additional checks. Type
	// inference is too inaccurate in this case, plus it would be very slow.
	if !baseClassAndSymbol.Symbol.HasTypedDeclarations() {
		return
	}

	// The original's comment: special case the '_' symbol, which is used in
	// single dispatch code and other cases where the name does not matter.
	if memberName == "_" {
		return
	}

	baseClass := baseClassAndSymbol.ClassType.(*ClassType)
	childClassSelf := ClassTypeCloneAsInstance(
		SelfSpecializeClass(childClassType, &SelfSpecializeOptions{UseBoundTypeVars: true}), false)

	// The original's comment: the "Self" value for the base class depends on
	// whether it's a protocol or not. It's not clear from the typing spec whether
	// this is the correct behavior.
	baseClassSelf := childClassSelf
	if !ClassTypeIsProtocolClass(baseClass) {
		baseClassSelf = ClassTypeCloneAsInstance(
			SelfSpecializeClass(baseClass, &SelfSpecializeOptions{UseBoundTypeVars: true}), false)
	}

	baseType := PartiallySpecializeType(
		c.evaluator.GetEffectiveTypeOfSymbol(baseClassAndSymbol.Symbol),
		baseClass,
		c.evaluator.GetTypeClassType(),
		baseClassSelf,
	)

	overrideType = PartiallySpecializeType(
		overrideType,
		childClassType,
		c.evaluator.GetTypeClassType(),
		childClassSelf,
	)

	if childClassType.Shared.TypeVarScopeID != "" {
		scopeIDs := []TypeVarScopeId{childClassType.Shared.TypeVarScopeID}
		overrideType = MakeTypeVarsBound(overrideType, scopeIDs, true)
		baseType = MakeTypeVarsBound(baseType, scopeIDs, true)
	}

	// The original's comment: determine whether this is an attempt to override a
	// method marked @final.
	if c.isFinalFunction(memberName, baseClassAndSymbol.Symbol, baseType) {
		if decl := GetLastTypedDeclarationForSymbol(overrideSymbol); decl != nil {
			if funcDecl, ok := decl.(*FunctionDeclaration); ok {
				diag := c.evaluator.AddDiagnostic(
					DiagnosticRuleReportIncompatibleMethodOverride,
					localization.LocMessage.FinalMethodOverride().Format(memberName, baseClass.Shared.Name),
					funcDecl.Node.(*parser.FunctionNode).D.Name,
					nil,
				)

				origDecl := GetLastTypedDeclarationForSymbol(baseClassAndSymbol.Symbol)
				if diag != nil && origDecl != nil {
					diag.AddRelatedInfo(localization.LocAddendum.FinalMethod(),
						origDecl.DeclBase().Uri, origDecl.DeclBase().Range)
				}
			}
		}
	}

	if IsFunctionOrOverloaded(baseType) {
		c.validateMethodOverrideAgainstBase(
			baseClassAndSymbol, overrideSymbol, overrideType, childClassType,
			childClassSelf, baseClass, baseType, memberName)
		return
	}

	if IsProperty(baseType) {
		// The original's comment: handle properties specially.
		if !IsProperty(overrideType) {
			decls := overrideSymbol.GetDeclarations()
			if len(decls) > 0 && overrideSymbol.IsClassMember() {
				lastDecl := decls[len(decls)-1]
				c.evaluator.AddDiagnostic(
					DiagnosticRuleReportIncompatibleMethodOverride,
					localization.LocMessage.PropertyOverridden().Format(memberName, baseClass.Shared.Name),
					declErrorNode(lastDecl),
					nil,
				)
			}
		} else {
			c.validatePropertyOverride(
				baseClass, childClassType, baseType, overrideType, overrideSymbol, memberName)
		}
		return
	}

	// The original's comment: this check can be expensive, so don't perform it if
	// the corresponding rule is disabled.
	if c.fileInfo.DiagnosticRuleSet.ReportIncompatibleVariableOverride == DiagnosticLevelNone {
		return
	}

	c.validateVariableOverride(
		baseClassAndSymbol, overrideSymbol, overrideType, childClassType, baseClass, baseType, memberName)
}

// validateMethodOverrideAgainstBase is the original's `isFunctionOrOverloaded`
// arm, split out only because Go has no early-return-from-block.
func (c *Checker) validateMethodOverrideAgainstBase(
	baseClassAndSymbol *ClassMember,
	overrideSymbol *Symbol,
	overrideType Type,
	childClassType *ClassType,
	childClassSelf *ClassType,
	baseClass *ClassType,
	baseType Type,
	memberName string,
) {
	diagAddendum := common.NewDiagnosticAddendum()

	// The original's comment: don't check certain magic functions or private
	// symbols. Also, skip this check if the class is a TypedDict. The methods for
	// a TypedDict are synthesized, and they can result in many overloads. We
	// assume they are correct and will not produce any errors.
	if isMethodExemptFromLsp(memberName) || IsPrivateName(memberName) ||
		ClassTypeIsTypedDictClass(childClassType) {
		return
	}

	if IsFunctionOrOverloaded(overrideType) {
		// The original's comment: don't enforce parameter names for dundered
		// methods. Many of them are misnamed in typeshed stubs, so this would
		// result in many false positives.
		enforceParamNameMatch := !IsDunderName(memberName)

		// The original's comment: if the base class member is a plain callable
		// variable (e.g. `x: Callable[..., None]`) rather than an actual method,
		// bind the override method's "self" so the two are compared as plain
		// callables.
		baseTypeForComparison, overrideTypeForComparison := c.getCallableVariableOverrideComparison(
			baseClassAndSymbol.Symbol, overrideSymbol, baseType, overrideType,
			childClassType, childClassSelf)

		if c.evaluator.ValidateOverrideMethod(
			baseTypeForComparison, overrideTypeForComparison, childClassType,
			diagAddendum, &enforceParamNameMatch) {
			return
		}

		decl := GetLastTypedDeclarationForSymbol(overrideSymbol)
		if decl == nil {
			return
		}

		diag := c.evaluator.AddDiagnostic(
			DiagnosticRuleReportIncompatibleMethodOverride,
			localization.LocMessage.IncompatibleMethodOverride().Format(memberName, baseClass.Shared.Name)+
				diagAddendum.GetString(),
			declErrorNode(decl),
			nil,
		)

		origDecl := GetLastTypedDeclarationForSymbol(baseClassAndSymbol.Symbol)
		if diag != nil && origDecl != nil {
			diag.AddRelatedInfo(localization.LocAddendum.OverriddenMethod(),
				origDecl.DeclBase().Uri, origDecl.DeclBase().Range)
		}
		return
	}

	if IsAnyOrUnknown(overrideType) {
		return
	}

	// The original's comment: special-case overrides of methods in '_TypedDict',
	// since TypedDict attributes aren't manifest as attributes but rather as
	// named keys.
	if ClassTypeIsBuiltInNamed(baseClass, "_TypedDict", "TypedDictFallback") {
		return
	}

	decls := overrideSymbol.GetDeclarations()
	if len(decls) == 0 {
		return
	}

	lastDecl := decls[len(decls)-1]
	diag := c.evaluator.AddDiagnostic(
		DiagnosticRuleReportIncompatibleMethodOverride,
		localization.LocMessage.MethodOverridden().Format(
			memberName, baseClass.Shared.Name, c.evaluator.PrintType(overrideType, nil)),
		declErrorNode(lastDecl),
		nil,
	)

	origDecl := GetLastTypedDeclarationForSymbol(baseClassAndSymbol.Symbol)
	if diag != nil && origDecl != nil {
		diag.AddRelatedInfo(localization.LocAddendum.OverriddenMethod(),
			origDecl.DeclBase().Uri, origDecl.DeclBase().Range)
	}
}

// validateVariableOverride is the original's
// `reportIncompatibleVariableOverride !== 'none'` block.
func (c *Checker) validateVariableOverride(
	baseClassAndSymbol *ClassMember,
	overrideSymbol *Symbol,
	overrideType Type,
	childClassType *ClassType,
	baseClass *ClassType,
	baseType Type,
	memberName string,
) {
	decls := overrideSymbol.GetDeclarations()
	if len(decls) == 0 {
		return
	}

	lastDecl := decls[len(decls)-1]
	primaryDecl := decls[0]

	// The original's comment: verify that the override type is assignable to
	// (same or narrower than) the declared type of the base symbol.
	isInvariant := false
	if varDecl, ok := primaryDecl.(*VariableDeclaration); ok {
		isInvariant = !varDecl.IsFinal
	}

	// The original's comment: if the entry is a member of a frozen dataclass, it
	// is immutable, so it does not need to be invariant.
	if ClassTypeIsDataClassFrozen(baseClass) && baseClass.Shared.DataClassEntries != nil {
		for _, entry := range baseClass.Shared.DataClassEntries {
			if entry.Name == memberName {
				isInvariant = false
				break
			}
		}
	}

	var overriddenTDEntry *TypedDictEntry
	var overrideTDEntry *TypedDictEntry

	if !overrideSymbol.IsIgnoredForProtocolMatch() {
		if baseClass.Shared.TypedDictEntries != nil {
			overriddenTDEntry = typedDictEntryFor(c.evaluator, baseClass, memberName)

			if overriddenTDEntry != nil && overriddenTDEntry.IsReadOnly {
				isInvariant = false
			}
		}

		if childClassType.Shared.TypedDictEntries != nil {
			overrideTDEntry = typedDictEntryFor(c.evaluator, childClassType, memberName)
		}
	}

	diagAddendum := common.NewDiagnosticAddendum()
	assignFlags := AssignTypeFlagsDefault
	if isInvariant {
		assignFlags = AssignTypeFlagsInvariant
	}

	if !c.evaluator.AssignType(baseType, overrideType, diagAddendum, nil, assignFlags, 0) {
		if isInvariant {
			// The invariance failure gets its own addendum rather than the
			// assignability one, because "int is not bool" is misleading when the
			// real reason is that the attribute is writable.
			diagAddendum = common.NewDiagnosticAddendum()
			diagAddendum.AddMessage(localization.LocAddendum.OverrideIsInvariant())
			diagAddendum.CreateAddendum().AddMessage(
				localization.LocAddendum.OverrideInvariantMismatch().Format(
					c.evaluator.PrintType(overrideType, nil),
					c.evaluator.PrintType(baseType, nil)))
		}

		diag := c.evaluator.AddDiagnostic(
			DiagnosticRuleReportIncompatibleVariableOverride,
			localization.LocMessage.SymbolOverridden().Format(memberName, baseClass.Shared.Name)+
				diagAddendum.GetString(),
			declErrorNode(lastDecl),
			nil,
		)

		origDecl := GetLastTypedDeclarationForSymbol(baseClassAndSymbol.Symbol)
		if diag != nil && origDecl != nil {
			diag.AddRelatedInfo(localization.LocAddendum.OverriddenSymbol(),
				origDecl.DeclBase().Uri, origDecl.DeclBase().Range)
		}
	} else if overriddenTDEntry != nil && overrideTDEntry != nil {
		// The original's comment: make sure the required/not-required attribute is
		// compatible.
		isRequiredCompatible := true
		if overriddenTDEntry.IsReadOnly {
			// The original's comment: if the read-only flag is set, a not-required
			// field can be overridden by a required field, but not vice versa.
			isRequiredCompatible = overrideTDEntry.IsRequired || !overriddenTDEntry.IsRequired
		} else {
			isRequiredCompatible = overrideTDEntry.IsRequired == overriddenTDEntry.IsRequired
		}

		if !isRequiredCompatible {
			message := localization.LocMessage.TypedDictFieldNotRequiredRedefinition().Format(memberName)
			if overrideTDEntry.IsRequired {
				message = localization.LocMessage.TypedDictFieldRequiredRedefinition().Format(memberName)
			}
			c.evaluator.AddDiagnostic(
				DiagnosticRuleReportGeneralTypeIssues,
				message,
				declErrorNode(lastDecl),
				nil,
			)
		}

		// The original's comment: make sure that the derived class isn't marking a
		// previously writable entry as read-only.
		if !overriddenTDEntry.IsReadOnly && overrideTDEntry.IsReadOnly {
			c.evaluator.AddDiagnostic(
				DiagnosticRuleReportGeneralTypeIssues,
				localization.LocMessage.TypedDictFieldReadOnlyRedefinition().Format(memberName),
				declErrorNode(lastDecl),
				nil,
			)
		}
	}

	// The original's comment: verify that there is not a Final mismatch.
	isBaseVarFinal := c.evaluator.IsFinalVariable(baseClassAndSymbol.Symbol)
	var overrideFinalVarDecl Declaration
	for _, d := range decls {
		if c.evaluator.IsFinalVariableDeclaration(d) {
			overrideFinalVarDecl = d
			break
		}
	}

	if !isBaseVarFinal && overrideFinalVarDecl != nil {
		diag := c.evaluator.AddDiagnostic(
			DiagnosticRuleReportIncompatibleVariableOverride,
			// Format takes (className, name); the message names the variable
			// first but the class appears first in the format string.
			localization.LocMessage.VariableFinalOverride().Format(baseClass.Shared.Name, memberName),
			declErrorNode(lastDecl),
			nil,
		)

		if diag != nil {
			diag.AddRelatedInfo(localization.LocAddendum.OverriddenSymbol(),
				overrideFinalVarDecl.DeclBase().Uri, overrideFinalVarDecl.DeclBase().Range)
		}
	}

	// The original's comment: verify that a class variable isn't overriding an
	// instance variable or vice versa.
	isBaseClassVar := baseClassAndSymbol.Symbol.IsClassVar()
	isClassVar := overrideSymbol.IsClassVar()

	if isBaseClassVar && !isClassVar {
		// The original's comment: if the subclass doesn't redeclare the type but
		// simply assigns it without declaring its type, we won't consider it an
		// instance variable.
		if !overrideSymbol.HasTypedDeclarations() {
			isClassVar = true
		}

		// The original's comment: if the subclass is declaring an inner class,
		// we'll consider that to be a ClassVar.
		typedDecls := overrideSymbol.GetTypedDeclarations()
		allClassDecls := true
		for _, d := range typedDecls {
			if _, ok := d.(*ClassDeclaration); !ok {
				allClassDecls = false
				break
			}
		}
		if allClassDecls {
			isClassVar = true
		}
	}

	// The original's comment: allow TypedDict members to have the same name as
	// class variables in the base class because TypedDict members are not really
	// instance members.
	ignoreTypedDictOverride := ClassTypeIsTypedDictClass(childClassType) && !isClassVar

	if isBaseClassVar != isClassVar && !ignoreTypedDictOverride {
		formattedMessage := localization.LocMessage.InstanceVarOverridesClassVar().
			Format(memberName, baseClass.Shared.Name)
		if overrideSymbol.IsClassVar() {
			formattedMessage = localization.LocMessage.ClassVarOverridesInstanceVar().
				Format(memberName, baseClass.Shared.Name)
		}

		diag := c.evaluator.AddDiagnostic(
			DiagnosticRuleReportIncompatibleVariableOverride,
			formattedMessage,
			declErrorNode(lastDecl),
			nil,
		)

		origDecl := GetLastTypedDeclarationForSymbol(baseClassAndSymbol.Symbol)
		if diag != nil && origDecl != nil {
			diag.AddRelatedInfo(localization.LocAddendum.OverriddenSymbol(),
				origDecl.DeclBase().Uri, origDecl.DeclBase().Range)
		}
	}
}

// typedDictEntryFor is the original's
// `knownItems.get(name) ?? extraItems ?? getEffectiveExtraItemsEntryType(...)`.
func typedDictEntryFor(evaluator TypeEvaluator, classType *ClassType, memberName string) *TypedDictEntry {
	if entry, ok := classType.Shared.TypedDictEntries.KnownItems.Get(memberName); ok && entry != nil {
		return entry
	}
	if classType.Shared.TypedDictEntries.ExtraItems != nil {
		return classType.Shared.TypedDictEntries.ExtraItems
	}
	return GetEffectiveExtraItemsEntryType(evaluator, classType)
}

// propertyAccessor names one of the three property methods along with the
// accessor that reads it, matching the original's propMethodInfo array.
type propertyAccessor struct {
	Name     string
	Accessor func(*ClassType) Type
}

var propertyAccessors = []propertyAccessor{
	{"fget", func(c *ClassType) Type {
		if c.Priv.FgetInfo == nil {
			return nil
		}
		return c.Priv.FgetInfo.MethodType
	}},
	{"fset", func(c *ClassType) Type {
		if c.Priv.FsetInfo == nil {
			return nil
		}
		return c.Priv.FsetInfo.MethodType
	}},
	{"fdel", func(c *ClassType) Type {
		if c.Priv.FdelInfo == nil {
			return nil
		}
		return c.Priv.FdelInfo.MethodType
	}},
}

// validatePropertyOverride corresponds to _validatePropertyOverride. Each of the
// three accessors is checked independently: a subclass may legally override the
// getter alone, but dropping a setter the base provides is an error in its own
// right and is reported differently from an incompatible one.
func (c *Checker) validatePropertyOverride(
	baseClassType *ClassType,
	childClassType *ClassType,
	baseType Type,
	childType Type,
	overrideSymbol *Symbol,
	memberName string,
) {
	baseAsClass, baseOk := baseType.(*ClassType)
	childAsClass, childOk := childType.(*ClassType)
	if !baseOk || !childOk {
		return
	}

	for _, info := range propertyAccessors {
		c.validatePropertyAccessorOverride(
			info, baseClassType, childClassType, baseAsClass, childAsClass, overrideSymbol, memberName)
	}
}

// validatePropertyAccessorOverride is one iteration of the original's forEach.
// It is its own function because the original's callback returns early in
// several places, which in Go must be a `return` from a function rather than a
// `continue`.
func (c *Checker) validatePropertyAccessorOverride(
	info propertyAccessor,
	baseClassType *ClassType,
	childClassType *ClassType,
	baseAsClass *ClassType,
	childAsClass *ClassType,
	overrideSymbol *Symbol,
	memberName string,
) {
	diagAddendum := common.NewDiagnosticAddendum()
	baseClassPropMethod := info.Accessor(baseAsClass)
	subclassPropMethod := info.Accessor(childAsClass)

	// The original's comment: is the method present on the base class but missing
	// in the subclass?
	if baseClassPropMethod == nil {
		return
	}

	baseClassMethodTypeAny := PartiallySpecializeType(
		baseClassPropMethod, baseClassType, c.evaluator.GetTypeClassType(), nil)

	// The original's comment: overloaded accessors (e.g. overloaded property
	// setters) are represented as an OverloadedType rather than a FunctionType.
	// Override-compatibility checking for overloaded accessors is not yet
	// performed; skip them rather than misreporting.
	if !IsFunction(baseClassMethodTypeAny) {
		return
	}
	baseClassMethodType := baseClassMethodTypeAny.(*FunctionType)

	if subclassPropMethod == nil {
		// The original's comment: the method is missing.
		diagAddendum.AddMessage(localization.LocAddendum.PropertyMethodMissing().Format(info.Name))

		decls := overrideSymbol.GetDeclarations()
		if len(decls) == 0 {
			return
		}

		lastDecl := decls[len(decls)-1]
		diag := c.evaluator.AddDiagnostic(
			DiagnosticRuleReportIncompatibleMethodOverride,
			localization.LocMessage.PropertyOverridden().Format(memberName, baseClassType.Shared.Name)+
				diagAddendum.GetString(),
			declErrorNode(lastDecl),
			nil,
		)

		origDecl := baseClassMethodType.Shared.Declaration
		if diag != nil && origDecl != nil {
			diag.AddRelatedInfo(localization.LocAddendum.OverriddenMethod(),
				origDecl.DeclBase().Uri, origDecl.DeclBase().Range)
		}

		return
	}

	subclassMethodTypeAny := PartiallySpecializeType(
		subclassPropMethod, childClassType, c.evaluator.GetTypeClassType(), nil)

	if !IsFunction(subclassMethodTypeAny) {
		return
	}
	subclassMethodType := subclassMethodTypeAny.(*FunctionType)

	if c.evaluator.ValidateOverrideMethod(
		baseClassMethodType, subclassMethodType, childClassType, diagAddendum.CreateAddendum(), nil) {
		return
	}

	diagAddendum.AddMessage(localization.LocAddendum.PropertyMethodIncompatible().Format(info.Name))

	funcDecl := subclassMethodType.Shared.Declaration
	if funcDecl == nil {
		return
	}
	funcNode, ok := funcDecl.Node.(*parser.FunctionNode)
	if !ok {
		return
	}

	var diagLocation parser.ParseNode = funcNode.D.Name

	// The original's comment: make sure the method decl is contained within the
	// class suite. If not, it probably comes from a decorator in another class. We
	// don't want to report the error in the wrong location.
	childClassDecl := childClassType.Shared.Declaration
	inSuite := false
	if childClassDecl != nil {
		if classDecl, ok := childClassDecl.(*ClassDeclaration); ok {
			if classNode, ok := classDecl.Node.(*parser.ClassNode); ok {
				inSuite = IsNodeContainedWithin(funcDecl.Node, classNode.D.Suite)
			}
		}
	}
	if !inSuite {
		symbolDecls := overrideSymbol.GetDeclarations()
		if len(symbolDecls) == 0 {
			return
		}
		diagLocation = declErrorNode(symbolDecls[len(symbolDecls)-1])
	}

	diag := c.evaluator.AddDiagnostic(
		DiagnosticRuleReportIncompatibleMethodOverride,
		localization.LocMessage.PropertyOverridden().Format(memberName, baseClassType.Shared.Name)+
			diagAddendum.GetString(),
		diagLocation,
		nil,
	)

	origDecl := baseClassMethodType.Shared.Declaration
	if diag != nil && origDecl != nil {
		diag.AddRelatedInfo(localization.LocAddendum.OverriddenMethod(),
			origDecl.DeclBase().Uri, origDecl.DeclBase().Range)
	}
}
