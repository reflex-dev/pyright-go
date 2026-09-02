/*
 * checker_variance.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/checker.ts (pyright 1.1.412):
 * _validateProtocolTypeParamVariance and _validateConstructorConsistency.
 *
 * The variance check is the more interesting of the two, because it *derives*
 * the correct variance rather than inspecting how the type parameter is used.
 * For each parameter it builds two specializations of the protocol that differ
 * only at that position -- one holding `object`, one holding the parameter
 * itself -- with every other position filled by a dummy class that relates to
 * nothing. Whether one specialization assigns to the other then answers the
 * question directly: if dest assigns to src the parameter is covariant, if src
 * assigns to dest it is contravariant, and if neither it is invariant.
 *
 * The dummy class is what makes the two specializations differ *only* at the
 * position under test; anything real in the other slots could relate them for
 * unrelated reasons and give the wrong answer. TypeVarTuples keep their own
 * value in both, since substituting a single dummy for a variadic is not
 * meaningful.
 *
 * _validateConstructorConsistency checks that __new__ and __init__ agree on
 * their parameters, since callers see one constructor signature but Python runs
 * both. The mutual assignType is deliberate -- either direction failing is a
 * mismatch. Three exemptions matter: a custom metaclass __call__ can accept
 * anything and construct however it likes; a `(*args: Any, **kwargs: Any)`
 * signature accepts everything by design; and if both methods are inherited the
 * mismatch belongs to the base class, not here.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/common/uri"
	"github.com/microsoft/pyright/go/localization"
	"github.com/microsoft/pyright/go/parser"
)

// validateProtocolTypeParamVariance corresponds to
// _validateProtocolTypeParamVariance.
func (c *Checker) validateProtocolTypeParamVariance(
	errorNode *parser.ClassNode, classType *ClassType,
) {
	// The original's comment: if this protocol has no TypeVars with specified
	// variance, there's nothing to do here.
	if len(classType.Shared.TypeParams) == 0 {
		return
	}

	objectType := c.evaluator.GetBuiltInType(errorNode, "object")
	if objectType == nil || !IsInstantiableClass(objectType) {
		return
	}

	objectObject := ClassTypeCloneAsInstance(objectType.(*ClassType), true)
	dummyTypeObject := ClassTypeCreateInstantiable(
		"__varianceDummy", "", "", uri.Empty(), 0, 0, nil, nil, nil)

	for paramIndex, param := range classType.Shared.TypeParams {
		// The original's comment: skip TypeVarTuples and ParamSpecs.
		if IsTypeVarTuple(param) || IsParamSpec(param) {
			continue
		}

		// The original's comment: skip type variables that have been internally
		// synthesized for a variety of reasons.
		if param.Shared.IsSynthesized {
			continue
		}

		// The original's comment: skip type variables with auto-variance.
		if param.Shared.DeclaredVariance == VarianceAuto {
			continue
		}

		// The original's comment: replace all type arguments with a dummy type
		// except for the TypeVar of interest, which is replaced with an object
		// instance -- and, for destTypeArgs, with itself.
		srcTypeArgs := make([]Type, 0, len(classType.Shared.TypeParams))
		destTypeArgs := make([]Type, 0, len(classType.Shared.TypeParams))
		for i, p := range classType.Shared.TypeParams {
			switch {
			case IsTypeVarTuple(p):
				srcTypeArgs = append(srcTypeArgs, p)
				destTypeArgs = append(destTypeArgs, p)
			case i == paramIndex:
				srcTypeArgs = append(srcTypeArgs, objectObject)
				destTypeArgs = append(destTypeArgs, p)
			default:
				srcTypeArgs = append(srcTypeArgs, dummyTypeObject)
				destTypeArgs = append(destTypeArgs, dummyTypeObject)
			}
		}

		srcType := ClassTypeSpecialize(classType, srcTypeArgs, nil, false, nil, nil)
		destType := ClassTypeSpecialize(classType, destTypeArgs, nil, false, nil, nil)

		expectedVariance := VarianceInvariant
		if c.evaluator.AssignClassToSelf(srcType, destType, VarianceCovariant) {
			expectedVariance = VarianceCovariant
		} else if c.evaluator.AssignClassToSelf(destType, srcType, VarianceContravariant) {
			expectedVariance = VarianceContravariant
		}

		if expectedVariance == classType.Shared.TypeParams[paramIndex].Shared.DeclaredVariance {
			continue
		}

		var message string
		switch expectedVariance {
		case VarianceCovariant:
			message = localization.LocMessage.ProtocolVarianceCovariant().
				Format(param.Shared.Name, classType.Shared.Name)
		case VarianceContravariant:
			message = localization.LocMessage.ProtocolVarianceContravariant().
				Format(param.Shared.Name, classType.Shared.Name)
		default:
			message = localization.LocMessage.ProtocolVarianceInvariant().
				Format(param.Shared.Name, classType.Shared.Name)
		}

		c.evaluator.AddDiagnostic(DiagnosticRuleReportInvalidTypeVarUse, message, errorNode.D.Name, nil)
	}
}

// constructorImplementation reduces a bound __new__ or __init__ result to the
// single FunctionType to compare, unwrapping an overload to its implementation.
// It returns nil where the original returns early.
func constructorImplementation(t Type) *FunctionType {
	if t == nil || !IsFunctionOrOverloaded(t) {
		return nil
	}

	if IsOverloaded(t) {
		// The original's comment: find the implementation, not the overloaded
		// signatures.
		impl := OverloadedTypeGetImplementation(t.(*OverloadedType))
		if impl == nil || !IsFunction(impl) {
			return nil
		}
		return impl.(*FunctionType)
	}

	if !IsFunction(t) {
		return nil
	}
	return t.(*FunctionType)
}

// validateConstructorConsistency corresponds to _validateConstructorConsistency.
// The original's comment: validates that the __init__ and __new__ method
// signatures are consistent.
func (c *Checker) validateConstructorConsistency(
	classType *ClassType, errorNode parser.ExpressionNode,
) {
	// The original's comment: if the class has a custom metaclass with a __call__
	// method, skip this check.
	if GetBoundCallMethod(c.evaluator, errorNode, classType) != nil {
		return
	}

	// The original omits additionalFlags here, and its default is
	// SkipObjectBaseClass -- not Default. Passing Default finds object.__new__
	// for every class that does not define its own, and then compares it against
	// that class's __init__, which mismatches for essentially every class.
	newMethodResult := GetBoundNewMethod(c.evaluator, errorNode, classType, nil,
		MemberAccessFlagsSkipObjectBaseClass)
	if newMethodResult == nil || newMethodResult.TypeErrors ||
		newMethodResult.ClassType == nil || !IsClass(newMethodResult.ClassType) {
		return
	}

	initMethodResult := GetBoundInitMethod(c.evaluator, errorNode,
		ClassTypeCloneAsInstance(classType, true), nil, MemberAccessFlagsSkipObjectBaseClass)
	if initMethodResult == nil || initMethodResult.TypeErrors ||
		initMethodResult.ClassType == nil || !IsClass(initMethodResult.ClassType) {
		return
	}

	initClass := initMethodResult.ClassType.(*ClassType)
	newClass := newMethodResult.ClassType.(*ClassType)

	// The original's comment: if both the __new__ and __init__ come from
	// subclasses, don't bother checking for this class.
	if !ClassTypeIsSameGenericClass(initClass, classType, 0) &&
		!ClassTypeIsSameGenericClass(newClass, classType, 0) {
		return
	}

	newMemberType := constructorImplementation(newMethodResult.Type)
	if newMemberType == nil {
		return
	}

	initMemberType := constructorImplementation(initMethodResult.Type)
	if initMemberType == nil {
		return
	}

	// The original's comment: if either of the functions has a default parameter
	// signature (*args: Any, **kwargs: Any), don't proceed with the check.
	if FunctionTypeHasDefaultParams(initMemberType) || FunctionTypeHasDefaultParams(newMemberType) {
		return
	}

	if c.evaluator.AssignType(newMemberType, initMemberType, nil, nil,
		AssignTypeFlagsSkipReturnTypeCheck, 0) &&
		c.evaluator.AssignType(initMemberType, newMemberType, nil, nil,
			AssignTypeFlagsSkipReturnTypeCheck, 0) {
		return
	}

	displayOnInit := ClassTypeIsSameGenericClass(initClass, classType, 0)
	initDecl := initMemberType.Shared.Declaration
	newDecl := newMemberType.Shared.Declaration

	if initDecl == nil || newDecl == nil {
		return
	}

	mainDecl := newDecl
	if displayOnInit {
		mainDecl = initDecl
	}

	var mainDeclNode parser.ParseNode = mainDecl.Node
	if fnNode, ok := mainDecl.Node.(*parser.FunctionNode); ok {
		mainDeclNode = fnNode.D.Name
	}

	diagAddendum := common.NewDiagnosticAddendum()
	diagAddendum.AddMessage(localization.LocAddendum.InitMethodSignature().
		Format(c.evaluator.PrintType(initMemberType, nil)))
	diagAddendum.AddMessage(localization.LocAddendum.NewMethodSignature().
		Format(c.evaluator.PrintType(newMemberType, nil)))

	reportedClass := newClass
	if displayOnInit {
		reportedClass = initClass
	}

	diagnostic := c.evaluator.AddDiagnostic(DiagnosticRuleReportInconsistentConstructor,
		localization.LocMessage.ConstructorParametersMismatch().Format(
			c.evaluator.PrintType(ClassTypeCloneAsInstance(reportedClass, true), nil))+
			diagAddendum.GetString(),
		mainDeclNode, nil)

	if diagnostic == nil {
		return
	}

	// The two addendum messages are distinct generated types, so the choice is
	// made over the formatted string rather than over the ParameterizedString.
	secondaryDecl := initDecl
	otherClass := initClass
	if displayOnInit {
		secondaryDecl = newDecl
		otherClass = newClass
	}

	otherClassText := c.evaluator.PrintType(ClassTypeCloneAsInstance(otherClass, true), nil)
	relatedMessage := localization.LocAddendum.InitMethodLocation().Format(otherClassText)
	if displayOnInit {
		relatedMessage = localization.LocAddendum.NewMethodLocation().Format(otherClassText)
	}

	diagnostic.AddRelatedInfo(relatedMessage, secondaryDecl.Uri, secondaryDecl.Range)
}
