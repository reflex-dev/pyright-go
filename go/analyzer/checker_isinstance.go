/*
 * checker_isinstance.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/checker.ts (pyright 1.1.412):
 * _validateIsInstanceCall, _validateUnsafeProtocolOverlap,
 * _isTypeSupportedTypeForIsInstance and _validateNotDataProtocol.
 *
 * The original's comment: validates that a call to isinstance or issubclass are
 * necessary. This is a common source of programming errors. Also validates that
 * arguments passed to isinstance or issubclass won't generate exceptions.
 *
 * Three separate checks share the pass, and they answer different questions.
 * The first is about the *runtime*: many types that are perfectly good in an
 * annotation raise a TypeError when handed to isinstance -- a subscripted
 * generic, a TypedDict, a protocol that is not runtime-checkable, a TypeVar.
 * The second is about soundness: a runtime protocol check compares attribute
 * names only, so a class the type system rejects can still pass it, and
 * narrowing on that result is unsafe.
 *
 * The third is the "unnecessary" check, and it is the reason narrowing is run
 * twice. Narrowing positively and negatively and asking whether either result
 * is Never distinguishes a test that can never fail from one that can never
 * succeed -- one call cannot tell them apart. It is skipped inside an assert,
 * where a redundant check is deliberate.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/localization"
	"github.com/microsoft/pyright/go/parser"
)

// validateIsInstanceCall corresponds to _validateIsInstanceCall.
func (c *Checker) validateIsInstanceCall(node *parser.CallNode) {
	leftName, ok := node.D.LeftExpr.(*parser.NameNode)
	if !ok || (leftName.D.Value != "isinstance" && leftName.D.Value != "issubclass") ||
		len(node.D.Args) != 2 {
		return
	}

	isInstanceCheck := leftName.D.Value == "isinstance"

	arg0Type := c.evaluator.GetType(node.D.Args[0].D.ValueExpr)
	if arg0Type == nil {
		return
	}
	arg0Type = MapSubtypes(arg0Type, func(subtype Type) Type {
		return TransformPossibleRecursiveTypeAlias(subtype, 0)
	}, nil)
	arg0Type = c.evaluator.ExpandPromotionTypes(node, arg0Type)

	arg1Type := c.evaluator.GetType(node.D.Args[1].D.ValueExpr)
	if arg1Type == nil {
		return
	}

	c.validateIsInstanceArgTypes(node, arg1Type, isInstanceCheck)

	// The original's comment: if this call is an issubclass, check for the use of
	// a "data protocol", which PEP 544 says cannot be used in issubclass.
	if !isInstanceCheck {
		c.validateIsSubclassDataProtocol(node, arg1Type)
	}

	// The original's comment: if this call is within an assert statement, we
	// won't check whether it's unnecessary.
	if IsWithinAssertExpression(node) {
		return
	}

	classTypeList, ok := GetIsInstanceClassTypes(c.evaluator, arg1Type)
	if !ok {
		return
	}

	// The original's comment: check for unsafe protocol overlaps.
	for _, filterType := range classTypeList {
		if !IsInstantiableClass(filterType) {
			continue
		}
		testType := arg0Type
		if !isInstanceCheck {
			testType = ConvertToInstance(arg0Type, false)
		}
		c.validateUnsafeProtocolOverlap(node.D.Args[0].D.ValueExpr,
			ClassTypeCloneAsInstance(filterType.(*ClassType), true), testType)
	}

	// The original's comment: check for unnecessary isinstance or issubclass
	// calls.
	if c.fileInfo.DiagnosticRuleSet.ReportUnnecessaryIsInstance == DiagnosticLevelNone {
		return
	}

	narrowedTypeNegative := NarrowTypeForInstanceOrSubclass(
		c.evaluator, arg0Type, classTypeList, isInstanceCheck, false, false, node)
	narrowedTypePositive := NarrowTypeForInstanceOrSubclass(
		c.evaluator, arg0Type, classTypeList, isInstanceCheck, false, true, node)

	isAlwaysTrue := IsNever(narrowedTypeNegative)
	isNeverTrue := IsNever(narrowedTypePositive)

	if !isAlwaysTrue && !isNeverTrue {
		return
	}

	instances := make([]Type, 0, len(classTypeList))
	for _, t := range classTypeList {
		instances = append(instances, ConvertToInstance(t, false))
	}
	classType := CombineTypes(instances, nil)

	var message string
	switch {
	case isAlwaysTrue && isInstanceCheck:
		message = localization.LocMessage.UnnecessaryIsInstanceAlways().Format(
			c.evaluator.PrintType(arg0Type, nil), c.evaluator.PrintType(classType, nil))
	case isAlwaysTrue:
		message = localization.LocMessage.UnnecessaryIsSubclassAlways().Format(
			c.evaluator.PrintType(arg0Type, nil), c.evaluator.PrintType(classType, nil))
	case isInstanceCheck:
		message = localization.LocMessage.UnnecessaryIsInstanceNever().Format(
			c.evaluator.PrintType(arg0Type, nil), c.evaluator.PrintType(classType, nil))
	default:
		message = localization.LocMessage.UnnecessaryIsSubclassNever().Format(
			c.evaluator.PrintType(arg0Type, nil), c.evaluator.PrintType(classType, nil))
	}

	c.evaluator.AddDiagnostic(DiagnosticRuleReportUnnecessaryIsInstance, message, node, nil)
}

// validateIsInstanceArgTypes is the original's first block: is every type in the
// second argument legal to hand to isinstance at runtime?
func (c *Checker) validateIsInstanceArgTypes(
	node *parser.CallNode, arg1Type Type, isInstanceCheck bool,
) {
	isValidType := true
	diag := common.NewDiagnosticAddendum()

	DoForEachSubtype(arg1Type, func(arg1Subtype Type, _ int, _ []Type) {
		if IsClassInstance(arg1Subtype) && ClassTypeIsTupleClass(arg1Subtype.(*ClassType)) &&
			arg1Subtype.(*ClassType).Priv.TupleTypeArgs != nil {
			for _, typeArg := range arg1Subtype.(*ClassType).Priv.TupleTypeArgs {
				if !c.isTypeSupportedTypeForIsInstance(typeArg.Type, isInstanceCheck, diag) {
					isValidType = false
				}
			}
			return
		}

		if !c.isTypeSupportedTypeForIsInstance(arg1Subtype, isInstanceCheck, diag) {
			isValidType = false
		}
	})

	if isValidType {
		return
	}

	message := localization.LocMessage.IsSubclassInvalidType().
		Format(c.evaluator.PrintType(arg1Type, nil)) + diag.GetString()
	if isInstanceCheck {
		message = localization.LocMessage.IsInstanceInvalidType().
			Format(c.evaluator.PrintType(arg1Type, nil)) + diag.GetString()
	}

	c.evaluator.AddDiagnostic(DiagnosticRuleReportArgumentType, message, node.D.Args[1], nil)
}

// validateIsSubclassDataProtocol is the original's PEP 544 data-protocol block.
func (c *Checker) validateIsSubclassDataProtocol(node *parser.CallNode, arg1Type Type) {
	diag := common.NewDiagnosticAddendum()

	DoForEachSubtype(arg1Type, func(arg1Subtype Type, _ int, _ []Type) {
		if IsClassInstance(arg1Subtype) && ClassTypeIsTupleClass(arg1Subtype.(*ClassType)) &&
			arg1Subtype.(*ClassType).Priv.TupleTypeArgs != nil {
			for _, typeArg := range arg1Subtype.(*ClassType).Priv.TupleTypeArgs {
				c.validateNotDataProtocol(typeArg.Type, diag)
			}
			return
		}

		c.validateNotDataProtocol(arg1Subtype, diag)
	})

	if !diag.IsEmpty() {
		c.evaluator.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
			localization.LocMessage.DataProtocolInSubclassCheck(), node.D.Args[1], nil)
	}
}

// validateNotDataProtocol corresponds to _validateNotDataProtocol.
func (c *Checker) validateNotDataProtocol(t Type, diag *common.DiagnosticAddendum) {
	if IsInstantiableClass(t) && ClassTypeIsProtocolClass(t.(*ClassType)) &&
		!IsMethodOnlyProtocol(t.(*ClassType)) {
		diag.AddMessage(localization.LocAddendum.DataProtocolUnsupported().
			Format(t.(*ClassType).Shared.Name))
	}
}

// validateUnsafeProtocolOverlap corresponds to _validateUnsafeProtocolOverlap.
func (c *Checker) validateUnsafeProtocolOverlap(
	errorNode parser.ExpressionNode, protocol *ClassType, testType Type,
) {
	// The original's comment: if this is a protocol class, check for an "unsafe
	// overlap" with the arg0 type.
	if !ClassTypeIsProtocolClass(protocol) {
		return
	}

	isUnsafeOverlap := false
	diag := common.NewDiagnosticAddendum()

	DoForEachSubtype(testType, func(testSubtype Type, _ int, _ []Type) {
		if !IsClassInstance(testSubtype) {
			return
		}
		if IsProtocolUnsafeOverlap(c.evaluator, protocol, testSubtype.(*ClassType)) {
			isUnsafeOverlap = true
			diag.AddMessage(localization.LocAddendum.ProtocolUnsafeOverlap().
				Format(testSubtype.(*ClassType).Shared.Name))
		}
	})

	if isUnsafeOverlap {
		c.evaluator.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
			localization.LocMessage.ProtocolUnsafeOverlap().Format(protocol.Shared.Name)+
				diag.GetString(),
			errorNode, nil)
	}
}

// isTypeSupportedTypeForIsInstance corresponds to
// _isTypeSupportedTypeForIsInstance. The original's comment: determines whether
// the specified type is allowed as the second argument to an isinstance or
// issubclass check.
func (c *Checker) isTypeSupportedTypeForIsInstance(
	t Type, isInstanceCheck bool, diag *common.DiagnosticAddendum,
) bool {
	isSupported := true

	DoForEachSubtype(t, func(subtype Type, _ int, _ []Type) {
		subtype = c.evaluator.MakeTopLevelTypeVarsConcrete(subtype, false)
		subtype = TransformPossibleRecursiveTypeAlias(subtype, 0)

		if specialForm := propsSpecialForm(subtype); specialForm != nil &&
			ClassTypeIsBuiltInNamed(specialForm, "TypeAliasType") {
			diag.AddMessage(localization.LocAddendum.TypeAliasInstanceCheck())
			isSupported = false
			return
		}

		switch subtype.Base().Category {
		case TypeCategoryAny, TypeCategoryUnknown, TypeCategoryUnbound:
			// Permitted.

		case TypeCategoryClass:
			if !c.isClassSupportedForIsInstance(subtype.(*ClassType), diag) {
				isSupported = false
			}

		case TypeCategoryFunction:
			if !subtype.Base().IsInstantiable() || subtype.(*FunctionType).Priv.IsCallableWithTypeArgs {
				diag.AddMessage(localization.LocAddendum.GenericClassNotAllowed())
				isSupported = false
			}

		case TypeCategoryTypeVar:
			diag.AddMessage(localization.LocAddendum.TypeVarNotAllowed())
			isSupported = false
		}
	})

	return isSupported
}

// isClassSupportedForIsInstance is the original's TypeCategory.Class arm, lifted
// out because it is a ten-way chain.
func (c *Checker) isClassSupportedForIsInstance(
	cls *ClassType, diag *common.DiagnosticAddendum,
) bool {
	switch {
	case ClassTypeIsBuiltInNamed(cls, "TypedDict"):
		diag.AddMessage(localization.LocAddendum.TypedDictNotAllowed())

	case ClassTypeIsBuiltInNamed(cls, "NamedTuple"):
		diag.AddMessage(localization.LocAddendum.NamedTupleNotAllowed())

	case IsNoneInstance(cls):
		diag.AddMessage(localization.LocAddendum.NoneNotAllowed())

	case ClassTypeIsTypedDictClass(cls):
		diag.AddMessage(localization.LocAddendum.TypedDictClassNotAllowed())

	case cls.Priv.IsTypeArgExplicit != nil && *cls.Priv.IsTypeArgExplicit && !cls.Priv.IncludeSubclasses:
		// The original's comment: if it's a class, make sure that it has not been
		// given explicit type arguments. This will result in a TypeError
		// exception.
		diag.AddMessage(localization.LocAddendum.GenericClassNotAllowed())

	case ClassTypeIsIllegalIsinstanceClass(cls):
		diag.AddMessage(localization.LocAddendum.IsinstanceClassNotSupported().Format(cls.Shared.Name))

	case ClassTypeIsProtocolClass(cls) && !ClassTypeIsRuntimeCheckable(cls) && !cls.Priv.IncludeSubclasses:
		// The original's comment: according to PEP 544, protocol classes cannot be
		// used as the right-hand argument to isinstance or issubclass unless they
		// are annotated as "runtime checkable".
		diag.AddMessage(localization.LocAddendum.ProtocolRequiresRuntimeCheckable())

	case ClassTypeIsNewTypeClass(cls):
		diag.AddMessage(localization.LocAddendum.NewTypeClassNotAllowed())

	default:
		specialForm := propsSpecialForm(cls)
		if specialForm != nil && IsClassInstance(specialForm) &&
			ClassTypeIsBuiltInNamed(specialForm, "Annotated") {
			diag.AddMessage(localization.LocAddendum.AnnotatedNotAllowed())
			return false
		}
		if specialForm != nil && IsInstantiableClass(specialForm) &&
			ClassTypeIsBuiltInNamed(specialForm, "Literal") {
			diag.AddMessage(localization.LocAddendum.LiteralNotAllowed())
			return false
		}
		return true
	}

	return false
}

// propsSpecialForm reads type.props?.specialForm, guarding the two nil hops the
// original spells with optional chaining.
func propsSpecialForm(t Type) *ClassType {
	if t == nil {
		return nil
	}
	props := t.Base().Props
	if props == nil {
		return nil
	}
	return props.SpecialForm
}
