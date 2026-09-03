/*
 * checker_baseoverride.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/checker.ts (pyright 1.1.412):
 * _validateBaseClassOverrides, _validateOverrideDecoratorPresent,
 * _validateOverrideDecoratorNotPresent, _isMethodExemptFromLsp and
 * _isFinalFunction.
 *
 * The driver walks this class's own symbols and, for each, finds the same name
 * in every base class. Note that it looks the base up in the *MRO* rather than
 * using the base class object directly: the MRO entry is the same generic class
 * already specialized with this class's type arguments, so `Base[int]` is
 * compared as `Base[int]` rather than as `Base[T]`.
 *
 * The two @override decorator checks are opposites and share their shape.
 * Present-but-shouldn't-be is an error because no base declares the name;
 * absent-but-should-be is a style diagnostic. Both dig the underlying function
 * out of whatever the symbol resolved to -- a plain function, an overload's
 * implementation, or a property's getter -- and both then confirm the function's
 * declaration is still one of the symbol's own. That last test is what stops a
 * decorator that *replaced* the function from being blamed for the decorator it
 * no longer carries.
 *
 * They differ in one detail worth keeping: when an overload has no
 * implementation, only the not-present check falls back to the first overload.
 * The present check leaves overrideFunction unset and returns.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/localization"
	"github.com/microsoft/pyright/go/parser"
)

// validateBaseClassOverrides corresponds to _validateBaseClassOverrides.
func (c *Checker) validateBaseClassOverrides(classType *ClassType) {
	symbolTable := ClassTypeGetSymbolTable(classType)

	for _, name := range symbolTable.Keys() {
		symbol, _ := symbolTable.Get(name)

		// The original's comment: private symbols do not need to match in type
		// since their names are mangled, and subclasses can't access the value in
		// the parent class.
		if IsPrivateName(name) {
			continue
		}

		// The original's comment: if the symbol has no declaration, and the type
		// is inferred, skip the type validation but still check for other issues
		// like Final overrides and class/instance variable mismatches.
		validateType := symbol.HasTypedDeclarations()

		// The original's comment: get the symbol type defined in this class.
		typeOfSymbol := c.evaluator.GetEffectiveTypeOfSymbol(symbol)

		// The original's comment: if the type of the override symbol isn't known,
		// stop here.
		if IsAnyOrUnknown(typeOfSymbol) {
			continue
		}

		var firstOverride *ClassMember

		for _, baseClass := range classType.Shared.BaseClasses {
			if !IsClass(baseClass) {
				continue
			}

			// The original's comment: look up the base class in the MRO list. It's
			// the same generic class but has already been specialized using the
			// type variables of the classType.
			var mroBaseClass Type
			for _, mroClass := range classType.Shared.Mro {
				if IsClass(mroClass) && ClassTypeIsSameGenericClass(
					mroClass.(*ClassType), baseClass.(*ClassType), 0) {
					mroBaseClass = mroClass
					break
				}
			}
			if mroBaseClass == nil {
				continue
			}

			// The original asserts isClass(mroBaseClass) here; the search above
			// only matches class entries.
			baseClassAndSymbol := LookUpClassMember(mroBaseClass.(*ClassType), name,
				MemberAccessFlagsDefault, nil)
			if baseClassAndSymbol == nil {
				continue
			}

			if firstOverride == nil {
				firstOverride = baseClassAndSymbol
			}

			overrideType := typeOfSymbol
			if !validateType {
				overrideType = AnyTypeCreate(false)
			}

			c.validateBaseClassOverride(baseClassAndSymbol, symbol, overrideType, classType, name)
		}

		if firstOverride == nil {
			// The original's comment: if this is a method decorated with
			// @override, validate that there is a base class method of the same
			// name.
			c.validateOverrideDecoratorNotPresent(symbol, typeOfSymbol)
		} else {
			c.validateOverrideDecoratorPresent(symbol, typeOfSymbol, firstOverride)
		}
	}
}

// overrideFunctionFor digs the underlying function out of whatever an override
// symbol resolved to. useFirstOverloadIfNoImpl distinguishes the two decorator
// checks: only the not-present check falls back to the first overload.
func overrideFunctionFor(overrideType Type, useFirstOverloadIfNoImpl bool) *FunctionType {
	if IsFunction(overrideType) {
		return overrideType.(*FunctionType)
	}

	if IsOverloaded(overrideType) {
		overloaded := overrideType.(*OverloadedType)
		impl := OverloadedTypeGetImplementation(overloaded)
		if impl != nil && IsFunction(impl) {
			return impl.(*FunctionType)
		}

		// The original's comment: if there is no implementation present, use the
		// first overload.
		if impl == nil && useFirstOverloadIfNoImpl {
			if overloads := OverloadedTypeGetOverloads(overloaded); len(overloads) > 0 {
				return overloads[0]
			}
		}
		return nil
	}

	if IsClassInstance(overrideType) && ClassTypeIsPropertyClass(overrideType.(*ClassType)) {
		fgetInfo := overrideType.(*ClassType).Priv.FgetInfo
		if fgetInfo != nil && IsFunction(fgetInfo.MethodType) {
			return fgetInfo.MethodType.(*FunctionType)
		}
	}

	return nil
}

// symbolOwnsDeclaration is the original's repeated check that the function's
// declaration is still one of the symbol's own -- if it is not, a decorator
// replaced the function and should not be blamed.
func symbolOwnsDeclaration(symbol *Symbol, decl *FunctionDeclaration) bool {
	for _, symbolDecl := range symbol.GetDeclarations() {
		if symbolDecl == Declaration(decl) {
			return true
		}
	}
	return false
}

// isMethodExemptFromLsp corresponds to _isMethodExemptFromLsp. The original's
// comment: determines whether the name is exempt from Liskov Substitution
// Principle rules.
func isMethodExemptFromLsp(name string) bool {
	switch name {
	case "__init__", "__new__", "__init_subclass__", "__post_init__":
		return true
	}
	return false
}

// validateOverrideDecoratorPresent corresponds to
// _validateOverrideDecoratorPresent.
func (c *Checker) validateOverrideDecoratorPresent(
	symbol *Symbol, overrideType Type, baseMember *ClassMember,
) {
	// The original's comment: skip this check if disabled.
	if c.fileInfo.DiagnosticRuleSet.ReportImplicitOverride == DiagnosticLevelNone {
		return
	}

	overrideFunction := overrideFunctionFor(overrideType, false)
	if overrideFunction == nil || overrideFunction.Shared.Declaration == nil ||
		FunctionTypeIsOverridden(overrideFunction) {
		return
	}

	// The original's comment: constructors are exempt.
	if isMethodExemptFromLsp(overrideFunction.Shared.Name) {
		return
	}

	// The original's comment: if the declaration for the override function is not
	// the same as the declaration for the symbol, the function was probably
	// replaced by a decorator.
	if !symbolOwnsDeclaration(symbol, overrideFunction.Shared.Declaration) {
		return
	}

	// The original's comment: if the base class is unknown, don't report a
	// missing decorator.
	if IsAnyOrUnknown(baseMember.ClassType) {
		return
	}

	funcNode, ok := overrideFunction.Shared.Declaration.Node.(*parser.FunctionNode)
	if !ok {
		return
	}

	c.evaluator.AddDiagnostic(DiagnosticRuleReportImplicitOverride,
		localization.LocMessage.OverrideDecoratorMissing().Format(
			funcNode.D.Name.D.Value,
			c.evaluator.PrintType(ConvertToInstance(baseMember.ClassType, true), nil)),
		funcNode.D.Name, nil)
}

// validateOverrideDecoratorNotPresent corresponds to
// _validateOverrideDecoratorNotPresent. The original's comment: determines
// whether the type is a function or overloaded function with an @override
// decorator. In this case, an error is reported because no base class has
// declared a method of the same name.
func (c *Checker) validateOverrideDecoratorNotPresent(symbol *Symbol, overrideType Type) {
	overrideFunction := overrideFunctionFor(overrideType, true)
	if overrideFunction == nil || overrideFunction.Shared.Declaration == nil ||
		!FunctionTypeIsOverridden(overrideFunction) {
		return
	}

	// The original's comment: if the declaration for the override function is not
	// the same as the declaration for the symbol, the function was probably
	// replaced by a decorator.
	if !symbolOwnsDeclaration(symbol, overrideFunction.Shared.Declaration) {
		return
	}

	funcNode, ok := overrideFunction.Shared.Declaration.Node.(*parser.FunctionNode)
	if !ok {
		return
	}

	c.evaluator.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
		localization.LocMessage.OverriddenMethodNotFound().Format(funcNode.D.Name.D.Value),
		funcNode.D.Name, nil)
}

// isFinalFunction corresponds to _isFinalFunction. It asks whether the symbol
// was declared with a `def` carrying @final, which is deliberately checked
// against the *undecorated* function type -- a decorator may wrap the function
// into something that no longer carries the flag.
func (c *Checker) isFinalFunction(name string, symbol *Symbol, _ Type) bool {
	if IsPrivateName(name) {
		return false
	}

	// The original's comment: was this declared with a "def" statement?
	for _, decl := range symbol.GetDeclarations() {
		funcDecl, ok := decl.(*FunctionDeclaration)
		if !ok {
			continue
		}
		funcNode, ok := funcDecl.Node.(*parser.FunctionNode)
		if !ok {
			continue
		}

		// The original's comment: locate all final function declarations.
		typeInfo := c.evaluator.GetTypeOfFunction(funcNode)
		if typeInfo == nil {
			continue
		}
		if FunctionTypeIsFinal(typeInfo.FunctionType) {
			return true
		}
	}

	return false
}

// validateTypedDictOverrides corresponds to _validateTypedDictOverrides. The
// original's comment: for a TypedDict class that derives from another TypedDict
// class that is closed, verify that any new keys are compatible with the base
// class.
//
// A closed TypedDict fixes what may appear in it. A subclass adding a key is
// therefore only legal if the base declared `extra_items` and the new key fits
// it -- invariantly when those extra items are mutable, covariantly when they
// are read-only, which is the same reasoning that governs any mutable container.
func (c *Checker) validateTypedDictOverrides(classType *ClassType) {
	if !ClassTypeIsTypedDictClass(classType) {
		return
	}

	typedDictEntries := GetTypedDictMembersForClass(c.evaluator, classType, false)

	for _, baseClass := range classType.Shared.BaseClasses {
		diag := common.NewDiagnosticAddendum()

		if !IsClass(baseClass) || !ClassTypeIsTypedDictClass(baseClass.(*ClassType)) ||
			!ClassTypeIsTypedDictEffectivelyClosed(baseClass.(*ClassType)) {
			continue
		}

		baseCls := baseClass.(*ClassType)
		baseTypedDictEntries := GetTypedDictMembersForClass(c.evaluator, baseCls, false)
		solution := BuildSolutionFromSpecializedClass(baseCls)

		var baseExtraItemsType Type = UnknownTypeCreate(false)
		if baseTypedDictEntries.ExtraItems != nil {
			baseExtraItemsType = ApplySolvedTypeVars(
				baseTypedDictEntries.ExtraItems.ValueType, solution, nil)
		}

		// extraItemsFlags is the original's repeated
		// `!extraItems.isReadOnly ? Invariant : Default`.
		extraItemsFlags := AssignTypeFlagsDefault
		if baseTypedDictEntries.ExtraItems != nil && !baseTypedDictEntries.ExtraItems.IsReadOnly {
			extraItemsFlags = AssignTypeFlagsInvariant
		}

		for _, name := range typedDictEntries.KnownItems.Keys() {
			entry, _ := typedDictEntries.KnownItems.Get(name)

			if _, exists := baseTypedDictEntries.KnownItems.Get(name); exists {
				continue
			}

			switch {
			case baseTypedDictEntries.ExtraItems == nil ||
				IsNever(baseTypedDictEntries.ExtraItems.ValueType):
				diag.AddMessage(localization.LocAddendum.TypedDictClosedExtraNotAllowed().Format(name))

			case !c.evaluator.AssignType(baseExtraItemsType, entry.ValueType,
				nil, nil, extraItemsFlags, 0):
				diag.AddMessage(localization.LocAddendum.TypedDictClosedExtraTypeMismatch().
					Format(name, c.evaluator.PrintType(entry.ValueType, nil)))

			case !baseTypedDictEntries.ExtraItems.IsReadOnly && entry.IsReadOnly:
				diag.AddMessage(localization.LocAddendum.TypedDictClosedFieldNotReadOnly().Format(name))

			case !baseTypedDictEntries.ExtraItems.IsReadOnly && entry.IsRequired:
				diag.AddMessage(localization.LocAddendum.TypedDictClosedFieldNotRequired().Format(name))
			}
		}

		if typedDictEntries.ExtraItems != nil && baseTypedDictEntries.ExtraItems != nil {
			if !c.evaluator.AssignType(baseExtraItemsType, typedDictEntries.ExtraItems.ValueType,
				nil, nil, extraItemsFlags, 0) {
				diag.AddMessage(localization.LocAddendum.TypedDictClosedExtraTypeMismatch().
					Format("extra_items", c.evaluator.PrintType(typedDictEntries.ExtraItems.ValueType, nil)))
			}
		}

		if diag.IsEmpty() || classType.Shared.Declaration == nil {
			continue
		}

		declNode := GetNameNodeForDeclaration(classType.Shared.Declaration)
		if declNode == nil {
			continue
		}

		if baseTypedDictEntries.ExtraItems != nil {
			c.evaluator.AddDiagnostic(DiagnosticRuleReportIncompatibleVariableOverride,
				localization.LocMessage.TypedDictClosedExtras().Format(
					baseCls.Shared.Name, c.evaluator.PrintType(baseExtraItemsType, nil))+diag.GetString(),
				declNode, nil)
		} else {
			c.evaluator.AddDiagnostic(DiagnosticRuleReportIncompatibleVariableOverride,
				localization.LocMessage.TypedDictClosedNoExtras().Format(baseCls.Shared.Name)+
					diag.GetString(),
				declNode, nil)
		}
	}
}
