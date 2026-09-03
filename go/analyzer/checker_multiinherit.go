/*
 * checker_multiinherit.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/checker.ts (pyright 1.1.412):
 * _validateMultipleInheritanceCompatibility,
 * _validateMultipleInheritanceOverride,
 * _validateMultipleInheritancePropertyOverride,
 * _getCallableVariableOverrideComparison, _markFunctionStatic and
 * _addMultipleInheritanceRelatedInfo.
 *
 * The single-base override checks ask "does this class's member match its
 * base's?". These ask the sibling question: when a class has several bases that
 * each define the same name, do those *bases* agree with each other? The class
 * itself may define nothing at all, and the conflict is still real, because a
 * caller holding the class through either base's interface will disagree about
 * the member's type.
 *
 * That is why the outer loop starts at index 1 and compares each later base's
 * fields against the first base's view, and why a member the child class
 * overrides itself is skipped -- the ordinary single-base checks already cover
 * that case and would report it twice.
 *
 * _getCallableVariableOverrideComparison is the subtle part, and its comment in
 * the original explains why. When one base declares a member as a plain callable
 * *variable* (`cb: Callable[[int], None]`) and a sibling base overrides it with
 * a real *method* (`def cb(self, value: int)`), the two must be compared as
 * plain callables. Binding the method supplies its `self`, and marking both
 * sides static then stops the override comparison from skipping the first
 * parameter as an unbound receiver -- without that, the method's remaining
 * parameter would be silently dropped and an arity mismatch invented.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/localization"
	"github.com/microsoft/pyright/go/parser"
)

// validateMultipleInheritanceCompatibility corresponds to
// _validateMultipleInheritanceCompatibility. The original's comment: validates
// that any methods and variables in multiple base classes are compatible with
// each other.
func (c *Checker) validateMultipleInheritanceCompatibility(
	classType *ClassType, errorNode *parser.NameNode,
) {
	// The original's comment: skip this check if reportIncompatibleMethodOverride
	// and reportIncompatibleVariableOverride are disabled because it's a
	// relatively expensive check.
	if c.fileInfo.DiagnosticRuleSet.ReportIncompatibleMethodOverride == DiagnosticLevelNone &&
		c.fileInfo.DiagnosticRuleSet.ReportIncompatibleVariableOverride == DiagnosticLevelNone {
		return
	}

	// The original's comment: filter any unknown base classes. Also remove
	// Generic and Protocol base classes.
	baseClasses := []*ClassType{}
	for _, baseClass := range classType.Shared.BaseClasses {
		if IsClass(baseClass) &&
			!ClassTypeIsBuiltInNamed(baseClass.(*ClassType), "Generic") &&
			!ClassTypeIsBuiltInNamed(baseClass.(*ClassType), "Protocol") {
			baseClasses = append(baseClasses, baseClass.(*ClassType))
		}
	}

	// The original's comment: if there is only one base class, there's nothing to
	// do.
	if len(baseClasses) < 2 {
		return
	}

	// The original's comment: build maps of symbols for each of the base classes.
	baseClassSymbolMaps := make([]*common.OrderedMap[string, *ClassMember], 0, len(baseClasses))
	for _, baseClass := range baseClasses {
		var specializedBaseClass Type
		for _, mroClass := range classType.Shared.Mro {
			if IsClass(mroClass) && ClassTypeIsSameGenericClass(mroClass.(*ClassType), baseClass, 0) {
				specializedBaseClass = mroClass
				break
			}
		}

		if specializedBaseClass == nil || !IsClass(specializedBaseClass) {
			baseClassSymbolMaps = append(baseClassSymbolMaps,
				common.NewOrderedMap[string, *ClassMember]())
			continue
		}

		// The original's comment: retrieve all of the specialized symbols from the
		// base class and its ancestors.
		baseClassSymbolMaps = append(baseClassSymbolMaps,
			GetClassFieldsRecursive(specializedBaseClass.(*ClassType)))
	}

	childClassSymbolMap := GetClassFieldsRecursive(classType)

	for symbolMapBaseIndex := 1; symbolMapBaseIndex < len(baseClassSymbolMaps); symbolMapBaseIndex++ {
		baseSymbolMap := baseClassSymbolMaps[symbolMapBaseIndex]

		for _, name := range baseSymbolMap.Keys() {
			overriddenClassAndSymbol, _ := baseSymbolMap.Get(name)

			// The original's comment: special-case dundered methods, which can
			// differ in signature. Also exempt private symbols.
			if IsDunderName(name) || IsPrivateName(name) {
				continue
			}

			if !IsClass(overriddenClassAndSymbol.ClassType) {
				continue
			}

			overrideClassAndSymbol, ok := childClassSymbolMap.Get(name)
			if !ok || overrideClassAndSymbol == nil {
				continue
			}

			// The original's comment: if the override is the same as the
			// overridden, then there's nothing to check. If the override is the
			// child class, then we can also skip the check because the normal
			// override checks will report the error.
			if !IsClass(overrideClassAndSymbol.ClassType) ||
				ClassTypeIsSameGenericClass(overrideClassAndSymbol.ClassType.(*ClassType),
					overriddenClassAndSymbol.ClassType.(*ClassType), 0) ||
				ClassTypeIsSameGenericClass(overrideClassAndSymbol.ClassType.(*ClassType), classType, 0) {
				continue
			}

			c.validateMultipleInheritanceOverride(
				overriddenClassAndSymbol, overrideClassAndSymbol, classType, name, errorNode)
		}
	}
}

// markFunctionStatic corresponds to _markFunctionStatic.
func markFunctionStatic(functionType *FunctionType) *FunctionType {
	return FunctionTypeCloneWithNewFlags(functionType,
		functionType.Shared.Flags|FunctionTypeFlagsStaticMethod)
}

// getCallableVariableOverrideComparison corresponds to
// _getCallableVariableOverrideComparison.
func (c *Checker) getCallableVariableOverrideComparison(
	baseSymbol *Symbol,
	overrideSymbol *Symbol,
	baseType Type,
	overrideType Type,
	childClassType *ClassType,
	childClassSelf *ClassType,
) (Type, Type) {
	baseDecls := baseSymbol.GetDeclarations()
	baseIsCallableVariable := len(baseDecls) > 0
	for _, decl := range baseDecls {
		if _, ok := decl.(*VariableDeclaration); !ok {
			baseIsCallableVariable = false
			break
		}
	}

	overrideIsMethod := false
	for _, decl := range overrideSymbol.GetDeclarations() {
		if _, ok := decl.(*FunctionDeclaration); ok {
			overrideIsMethod = true
			break
		}
	}

	if !baseIsCallableVariable || !overrideIsMethod {
		return baseType, overrideType
	}

	boundOverrideType := c.evaluator.BindFunctionToClassOrObject(
		childClassSelf, overrideType, childClassType, false, nil, nil, 0)
	if boundOverrideType == nil {
		return baseType, overrideType
	}

	// The original's comment: mark both the base callable and the bound override
	// as static so the override comparison does not skip the first parameter as
	// an unbound "self". Without this, the bound override keeps its
	// instance-method flag and its first real parameter would be silently dropped
	// during the comparison.
	staticBaseType := baseType
	if IsFunction(baseType) {
		staticBaseType = markFunctionStatic(baseType.(*FunctionType))
	}

	if IsFunction(boundOverrideType) {
		return staticBaseType, markFunctionStatic(boundOverrideType.(*FunctionType))
	}

	if IsOverloaded(boundOverrideType) {
		// The original's comment: an overloaded method binds to an overloaded
		// callable. Apply the same static normalization to every overload (and the
		// implementation, if present) so an incompatible first parameter in an
		// overloaded override is still caught rather than skipped as "self".
		overloadedType := boundOverrideType.(*OverloadedType)
		originalOverloads := OverloadedTypeGetOverloads(overloadedType)
		staticOverloads := make([]*FunctionType, 0, len(originalOverloads))
		for _, overload := range originalOverloads {
			staticOverloads = append(staticOverloads, markFunctionStatic(overload))
		}

		staticImpl := OverloadedTypeGetImplementation(overloadedType)
		if staticImpl != nil && IsFunction(staticImpl) {
			staticImpl = markFunctionStatic(staticImpl.(*FunctionType))
		}

		return staticBaseType, OverloadedTypeCreate(staticOverloads, staticImpl)
	}

	return baseType, boundOverrideType
}

// addMultipleInheritanceRelatedInfo corresponds to
// _addMultipleInheritanceRelatedInfo.
func (c *Checker) addMultipleInheritanceRelatedInfo(
	diag *common.Diagnostic,
	overriddenClass *ClassType,
	overriddenType Type,
	overriddenDecl Declaration,
	overrideClass *ClassType,
	overrideType Type,
	overrideDecl Declaration,
) {
	diag.AddRelatedInfo(
		localization.LocAddendum.BaseClassOverriddenType().Format(
			c.evaluator.PrintType(ConvertToInstance(overriddenClass, true), nil),
			c.evaluator.PrintType(overriddenType, nil)),
		overriddenDecl.DeclBase().Uri, overriddenDecl.DeclBase().Range)

	diag.AddRelatedInfo(
		localization.LocAddendum.BaseClassOverridesType().Format(
			c.evaluator.PrintType(ConvertToInstance(overrideClass, true), nil),
			c.evaluator.PrintType(overrideType, nil)),
		overrideDecl.DeclBase().Uri, overrideDecl.DeclBase().Range)
}

// validateMultipleInheritanceOverride corresponds to
// _validateMultipleInheritanceOverride.
func (c *Checker) validateMultipleInheritanceOverride(
	overriddenClassAndSymbol *ClassMember,
	overrideClassAndSymbol *ClassMember,
	childClassType *ClassType,
	memberName string,
	errorNode parser.ParseNode,
) {
	if !IsClass(overriddenClassAndSymbol.ClassType) || !IsClass(overrideClassAndSymbol.ClassType) {
		return
	}

	// The original's comment: special case the '_' symbol, which is used in
	// single dispatch code and other cases where the name does not matter.
	if memberName == "_" {
		return
	}

	overriddenClass := overriddenClassAndSymbol.ClassType.(*ClassType)
	overrideClass := overrideClassAndSymbol.ClassType.(*ClassType)

	overriddenType := PartiallySpecializeType(
		c.evaluator.GetEffectiveTypeOfSymbol(overriddenClassAndSymbol.Symbol),
		overriddenClass, c.evaluator.GetTypeClassType(), nil)

	overrideSymbol := overrideClassAndSymbol.Symbol
	overrideType := PartiallySpecializeType(
		c.evaluator.GetEffectiveTypeOfSymbol(overrideSymbol),
		overrideClass, c.evaluator.GetTypeClassType(), nil)

	var childOverrideType Type
	if childOverrideSymbol, ok := ClassTypeGetSymbolTable(childClassType).Get(memberName); ok {
		childOverrideType = c.evaluator.GetEffectiveTypeOfSymbol(childOverrideSymbol)
	}

	var diag *common.Diagnostic
	overrideDecl := GetLastTypedDeclarationForSymbol(overrideClassAndSymbol.Symbol)
	overriddenDecl := GetLastTypedDeclarationForSymbol(overriddenClassAndSymbol.Symbol)

	switch {
	case IsFunctionOrOverloaded(overriddenType):
		if IsFunctionOrOverloaded(overrideType) {
			diag = c.compareInheritedMethods(
				overriddenClassAndSymbol, overrideClassAndSymbol,
				overriddenType, overrideType, childClassType, memberName, errorNode, overrideDecl)
		}

	case IsProperty(overriddenType):
		// The original's comment: handle properties specially.
		if !IsProperty(overrideType) && !IsAnyOrUnknown(overrideType) {
			if len(overrideSymbol.GetDeclarations()) > 0 {
				diag = c.evaluator.AddDiagnostic(DiagnosticRuleReportIncompatibleVariableOverride,
					localization.LocMessage.BaseClassVariableTypeIncompatible().Format(
						childClassType.Shared.Name, memberName),
					errorNode, nil)
			}
		} else {
			c.validateMultipleInheritancePropertyOverride(
				overriddenClass, childClassType, overriddenType, overrideType,
				overrideSymbol, memberName, errorNode)
		}

	default:
		diag = c.compareInheritedVariables(
			overriddenClassAndSymbol, overrideClassAndSymbol, overriddenType, overrideType,
			childOverrideType, childClassType, memberName, errorNode)
	}

	if diag != nil && overrideDecl != nil && overriddenDecl != nil {
		c.addMultipleInheritanceRelatedInfo(diag,
			overriddenClass, overriddenType, overriddenDecl,
			overrideClass, overrideType, overrideDecl)
	}
}

// compareInheritedMethods is the original's isFunctionOrOverloaded arm.
func (c *Checker) compareInheritedMethods(
	overriddenClassAndSymbol *ClassMember,
	overrideClassAndSymbol *ClassMember,
	overriddenType Type,
	overrideType Type,
	childClassType *ClassType,
	memberName string,
	errorNode parser.ParseNode,
	overrideDecl Declaration,
) *common.Diagnostic {
	diagAddendum := common.NewDiagnosticAddendum()

	// The original's comment: if one base defines the member as a plain callable
	// variable and a sibling base overrides it with a real method, bind the
	// method's "self" so the two are compared as plain callables.
	childClassSelf := ClassTypeCloneAsInstance(
		SelfSpecializeClass(childClassType, &SelfSpecializeOptions{UseBoundTypeVars: true}), true)

	baseForComparison, overrideForComparison := c.getCallableVariableOverrideComparison(
		overriddenClassAndSymbol.Symbol, overrideClassAndSymbol.Symbol,
		overriddenType, overrideType, childClassType, childClassSelf)

	enforceParamNames := true
	if c.evaluator.ValidateOverrideMethod(baseForComparison, overrideForComparison,
		nil, diagAddendum, &enforceParamNames) {
		return nil
	}

	if _, isFunc := overrideDecl.(*FunctionDeclaration); !isFunc {
		return nil
	}

	return c.evaluator.AddDiagnostic(DiagnosticRuleReportIncompatibleMethodOverride,
		localization.LocMessage.BaseClassMethodTypeIncompatible().Format(
			childClassType.Shared.Name, memberName)+diagAddendum.GetString(),
		errorNode, nil)
}

// compareInheritedVariables is the original's final else arm.
func (c *Checker) compareInheritedVariables(
	overriddenClassAndSymbol *ClassMember,
	overrideClassAndSymbol *ClassMember,
	overriddenType Type,
	overrideType Type,
	childOverrideType Type,
	childClassType *ClassType,
	memberName string,
	errorNode parser.ParseNode,
) *common.Diagnostic {
	// The original's comment: this check can be expensive, so don't perform it if
	// the corresponding rule is disabled.
	if c.fileInfo.DiagnosticRuleSet.ReportIncompatibleVariableOverride == DiagnosticLevelNone {
		return nil
	}

	overriddenClass := overriddenClassAndSymbol.ClassType.(*ClassType)
	overrideClass := overrideClassAndSymbol.ClassType.(*ClassType)

	primaryDecl := GetLastTypedDeclarationForSymbol(overriddenClassAndSymbol.Symbol)
	isInvariant := false
	if varDecl, ok := primaryDecl.(*VariableDeclaration); ok && !varDecl.IsFinal {
		isInvariant = true
	}

	// The original's comment: if the entry is a member of a frozen dataclass, it
	// is immutable, so it does not need to be invariant.
	if ClassTypeIsDataClassFrozen(overriddenClass) && overriddenClass.Shared.DataClassEntries != nil {
		for _, entry := range overriddenClass.Shared.DataClassEntries {
			if entry.Name == memberName {
				isInvariant = false
				break
			}
		}
	}

	overriddenTDEntry := c.typedDictEntryFor(overriddenClass, memberName)
	if overriddenTDEntry != nil && overriddenTDEntry.IsReadOnly {
		isInvariant = false
	}
	overrideTDEntry := c.typedDictEntryFor(overrideClass, memberName)

	srcType := childOverrideType
	if srcType == nil {
		srcType = overrideType
	}

	flags := AssignTypeFlagsDefault
	if isInvariant {
		flags = AssignTypeFlagsInvariant
	}

	if !c.evaluator.AssignType(overriddenType, srcType, nil, nil, flags, 0) {
		return c.evaluator.AddDiagnostic(DiagnosticRuleReportIncompatibleVariableOverride,
			localization.LocMessage.BaseClassVariableTypeIncompatible().Format(
				childClassType.Shared.Name, memberName),
			errorNode, nil)
	}

	if overriddenTDEntry == nil || overrideTDEntry == nil {
		return nil
	}

	// The original's comment: if both classes are TypedDicts and they both define
	// this field, make sure the attributes are compatible. A read-only field
	// relaxes the requirement -- a required override of a non-required read-only
	// field is fine, since nobody can write through it.
	isRequiredCompatible := true
	isReadOnlyCompatible := true

	if overriddenTDEntry.IsReadOnly {
		isRequiredCompatible = overrideTDEntry.IsRequired || !overriddenTDEntry.IsRequired
	} else {
		isReadOnlyCompatible = !overrideTDEntry.IsReadOnly
		isRequiredCompatible = overrideTDEntry.IsRequired == overriddenTDEntry.IsRequired
	}

	if !isRequiredCompatible {
		message := localization.LocMessage.TypedDictFieldNotRequiredRedefinition().Format(memberName)
		if overrideTDEntry.IsRequired {
			message = localization.LocMessage.TypedDictFieldRequiredRedefinition().Format(memberName)
		}
		return c.evaluator.AddDiagnostic(DiagnosticRuleReportIncompatibleVariableOverride,
			message, errorNode, nil)
	}

	if !isReadOnlyCompatible {
		return c.evaluator.AddDiagnostic(DiagnosticRuleReportIncompatibleVariableOverride,
			localization.LocMessage.TypedDictFieldReadOnlyRedefinition().Format(memberName),
			errorNode, nil)
	}

	return nil
}

// typedDictEntryFor is the original's repeated
// `knownItems.get(name) ?? extraItems ?? getEffectiveExtraItemsEntryType(...)`.
func (c *Checker) typedDictEntryFor(classType *ClassType, memberName string) *TypedDictEntry {
	if classType.Shared.TypedDictEntries == nil {
		return nil
	}

	if entry, ok := classType.Shared.TypedDictEntries.KnownItems.Get(memberName); ok {
		return entry
	}
	if classType.Shared.TypedDictEntries.ExtraItems != nil {
		return classType.Shared.TypedDictEntries.ExtraItems
	}
	return GetEffectiveExtraItemsEntryType(c.evaluator, classType)
}

// propMethodAccessor is one entry of the original's propMethodInfo table.
type propMethodAccessor struct {
	Name string
	Get  func(*ClassType) Type
}

// propMethodAccessors is the original's propMethodInfo.
var propMethodAccessors = []propMethodAccessor{
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

// validateMultipleInheritancePropertyOverride corresponds to
// _validateMultipleInheritancePropertyOverride. A property is compared accessor
// by accessor rather than as a whole, because a subclass may legitimately
// redeclare only the getter while inheriting the setter.
func (c *Checker) validateMultipleInheritancePropertyOverride(
	overriddenClassType *ClassType,
	overrideClassType *ClassType,
	overriddenSymbolType Type,
	overrideSymbolType Type,
	overrideSymbol *Symbol,
	memberName string,
	errorNode parser.ParseNode,
) {
	overriddenProp, ok1 := overriddenSymbolType.(*ClassType)
	overrideProp, ok2 := overrideSymbolType.(*ClassType)
	if !ok1 || !ok2 {
		return
	}

	for _, info := range propMethodAccessors {
		diagAddendum := common.NewDiagnosticAddendum()
		baseClassPropMethod := info.Get(overriddenProp)
		subclassPropMethod := info.Get(overrideProp)

		// The original's comment: is the method present on the base class but
		// missing in the subclass?
		if baseClassPropMethod == nil {
			continue
		}

		baseClassMethodType := PartiallySpecializeType(baseClassPropMethod,
			overriddenClassType, c.evaluator.GetTypeClassType(), nil)

		// The original's comment: overloaded accessors are represented as an
		// OverloadedType rather than a FunctionType. Override-compatibility
		// checking for overloaded accessors is not yet performed; only
		// single-function accessors are validated here.
		if !IsFunction(baseClassMethodType) {
			continue
		}

		if subclassPropMethod == nil {
			// The original's comment: the method is missing.
			diagAddendum.AddMessage(localization.LocAddendum.PropertyMethodMissing().Format(info.Name))

			decls := overrideSymbol.GetDeclarations()
			if len(decls) == 0 {
				continue
			}
			lastDecl := decls[len(decls)-1]

			diag := c.evaluator.AddDiagnostic(DiagnosticRuleReportIncompatibleMethodOverride,
				localization.LocMessage.PropertyOverridden().Format(
					memberName, overriddenClassType.Shared.Name)+diagAddendum.GetString(),
				errorNode, nil)

			origDecl := baseClassMethodType.(*FunctionType).Shared.Declaration
			if diag != nil && origDecl != nil {
				c.addMultipleInheritanceRelatedInfo(diag,
					overriddenClassType, overriddenSymbolType, origDecl,
					overrideClassType, overrideSymbolType, lastDecl)
			}
			continue
		}

		subclassMethodType := PartiallySpecializeType(subclassPropMethod,
			overrideClassType, c.evaluator.GetTypeClassType(), nil)

		if !IsFunction(subclassMethodType) {
			continue
		}

		if c.evaluator.ValidateOverrideMethod(baseClassMethodType, subclassMethodType,
			overrideClassType, diagAddendum.CreateAddendum(), nil) {
			continue
		}

		diagAddendum.AddMessage(localization.LocAddendum.PropertyMethodIncompatible().Format(info.Name))

		decl := subclassMethodType.(*FunctionType).Shared.Declaration
		if decl == nil {
			continue
		}

		diag := c.evaluator.AddDiagnostic(DiagnosticRuleReportIncompatibleMethodOverride,
			localization.LocMessage.PropertyOverridden().Format(
				memberName, overriddenClassType.Shared.Name)+diagAddendum.GetString(),
			errorNode, nil)

		origDecl := baseClassMethodType.(*FunctionType).Shared.Declaration
		if diag != nil && origDecl != nil {
			c.addMultipleInheritanceRelatedInfo(diag,
				overriddenClassType, overriddenSymbolType, origDecl,
				overrideClassType, overrideSymbolType, decl)
		}
	}
}
