/*
 * typeevaluator_boundmember.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/typeEvaluator.ts (pyright 1.1.412):
 * getTypeOfBoundMember; and from analyzer/constructors.ts: getBoundNewMethod,
 * getBoundInitMethod and getBoundCallMethod.
 *
 * Fetching a member from a class or instance with the binding already applied --
 * a method comes back with `self` removed, a descriptor comes back as what its
 * __get__ returns.
 *
 * The metaclass is consulted TWICE, for two different reasons, and the order
 * matters.
 *
 * First, before the object itself: if the metaclass has a data descriptor of
 * this name, the Python runtime satisfies the lookup from the descriptor and
 * never reaches the instance dictionary. That is why the first lookup can set
 * skipObjectTypeLookup. It is guarded on the metaclass not being plain `type`,
 * which the original notes is the common case and has no descriptors.
 *
 * Second, after the object, as an ordinary fallback for a member the class does
 * not define.
 *
 * The two lookups carry different flags in each direction. Reaching a member on
 * an instantiable class skips instance members and disallows generic instance
 * variable access; reaching it on an instance disallows ClassVar writes. And a
 * class's metaclass members are not reachable through an INSTANCE of the class,
 * so that direction restricts itself to the metaclass's instance members.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/localization"
	"github.com/microsoft/pyright/go/parser"
)

// GetTypeOfBoundMember corresponds to getTypeOfBoundMember. The original's
// recursionCount defaults to 0; the interface method has no such parameter.
func (e *typeEvaluator) GetTypeOfBoundMember(
	errorNode parser.ExpressionNode,
	objectType *ClassType,
	memberName string,
	usage *EvaluatorUsage,
	diag *common.DiagnosticAddendum,
	flags MemberAccessFlags,
	selfType Type,
) *TypeResult {
	return e.getTypeOfBoundMember(errorNode, objectType, memberName, usage, diag, flags, selfType, 0)
}

func (e *typeEvaluator) getTypeOfBoundMember(
	errorNode parser.ExpressionNode,
	objectType *ClassType,
	memberName string,
	usage *EvaluatorUsage,
	diag *common.DiagnosticAddendum,
	flags MemberAccessFlags,
	selfType Type,
	recursionCount int,
) *TypeResult {
	if usage == nil {
		usage = &EvaluatorUsage{Method: "get"}
	}

	if ClassTypeIsPartiallyEvaluated(objectType) {
		if errorNode != nil {
			e.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
				localization.LocMessage.ClassDefinitionCycle().Format(objectType.Shared.Name),
				errorNode, nil)
		}
		return &TypeResult{Type: UnknownTypeCreate(false)}
	}

	// The original's comment: if this is an unspecialized generic class,
	// specialize it using the default values for its type parameters.
	if IsInstantiableClass(objectType) && !objectType.Priv.IncludeSubclasses &&
		len(objectType.Shared.TypeParams) > 0 {
		// The original's comment: skip this if we're suppressing the use of
		// attribute access override, such as with dundered methods (like __call__).
		if (flags & MemberAccessFlagsSkipAttributeAccessOverride) == 0 {
			objectType = SpecializeWithDefaultTypeArgs(objectType)
		}
	}

	// The original's comment: determine the class that was used to instantiate the
	// objectType. If the objectType is a class itself, then the class used to
	// instantiate it is the metaclass.
	objectTypeIsInstantiable := objectType.Base().IsInstantiable()
	metaclass := objectType.Shared.EffectiveMetaclass

	// The original's comment: if the object type is an instantiable (i.e. it
	// derives from "type") and we've been asked not to consider instance members,
	// don't look in the class. Consider only the metaclass class variables in this
	// case.
	skipObjectTypeLookup := objectTypeIsInstantiable &&
		(flags&MemberAccessFlagsSkipInstanceMembers) != 0

	if e.metaclassDescriptorSatisfiesLookup(
		errorNode, objectType, memberName, usage, flags, objectTypeIsInstantiable, metaclass, recursionCount) {
		skipObjectTypeLookup = true
	}

	var memberInfo *ClassMemberLookup
	var subDiag *common.DiagnosticAddendum

	if !skipObjectTypeLookup {
		effectiveFlags := flags | MemberAccessFlagsSkipTypedDictEntries

		if objectTypeIsInstantiable {
			effectiveFlags |= MemberAccessFlagsSkipInstanceMembers |
				MemberAccessFlagsSkipAttributeAccessOverride |
				MemberAccessFlagsDisallowGenericInstanceVariableAccess
			effectiveFlags &^= MemberAccessFlagsSkipClassMembers
		} else {
			effectiveFlags |= MemberAccessFlagsDisallowClassVarWrites
		}

		if diag != nil {
			subDiag = common.NewDiagnosticAddendum()
		}

		// The original's comment: see if the member is present in the object itself.
		memberInfo = e.getTypeOfClassMemberName(errorNode, objectType, memberName, usage,
			subDiag, effectiveFlags, selfType, recursionCount)
	}

	// The original's comment: if it wasn't found on the object, see if it's part
	// of the metaclass.
	if memberInfo == nil && metaclass != nil && IsInstantiableClass(metaclass) {
		effectiveFlags := flags

		// The original's comment: class members cannot be accessed on a class's
		// metaclass through an instance of a class. Limit access to metaclass
		// instance members in this case.
		if !objectTypeIsInstantiable {
			effectiveFlags |= MemberAccessFlagsSkipClassMembers |
				MemberAccessFlagsSkipAttributeAccessOverride |
				MemberAccessFlagsSkipTypeBaseClass
			effectiveFlags &^= MemberAccessFlagsSkipInstanceMembers
		}

		var metaclassDiag *common.DiagnosticAddendum
		if diag != nil {
			metaclassDiag = common.NewDiagnosticAddendum()
		}

		var effectiveSelfType Type = objectType
		if !objectTypeIsInstantiable {
			effectiveSelfType = ClassTypeCloneAsInstantiable(objectType, false)
		}

		memberInfo = e.getTypeOfClassMemberName(errorNode,
			ClassTypeCloneAsInstance(metaclass.(*ClassType), true), memberName, usage,
			metaclassDiag, effectiveFlags, effectiveSelfType, recursionCount)

		// The original's comment: if there was a descriptor error (as opposed to an
		// error where the members was simply not found), use this diagnostic
		// message.
		if memberInfo != nil && memberInfo.IsDescriptorError {
			subDiag = metaclassDiag
		}
	}

	if memberInfo != nil {
		if memberInfo.IsDescriptorError && diag != nil && subDiag != nil {
			diag.AddAddendum(subDiag)
		}

		return &TypeResult{
			Type:                        memberInfo.Type,
			ClassType:                   memberInfo.ClassType,
			IsIncomplete:                memberInfo.IsTypeIncomplete,
			IsAsymmetricAccessor:        memberInfo.IsAsymmetricAccessor,
			NarrowedTypeForSet:          memberInfo.NarrowedTypeForSet,
			MemberAccessDeprecationInfo: memberInfo.MemberAccessDeprecationInfo,
			TypeErrors:                  memberInfo.IsDescriptorError,
		}
	}

	// The original's comment: if this is a type[Any] or type[Unknown], allow any
	// other members.
	if IsClassInstance(objectType) && ClassTypeIsBuiltInNamed(objectType, "type") &&
		objectType.Priv.IncludeSubclasses {
		if (flags & (MemberAccessFlagsSkipTypeBaseClass | MemberAccessFlagsSkipAttributeAccessOverride)) == 0 {
			var typeArg Type = UnknownTypeCreate(false)
			if len(objectType.Priv.TypeArgs) >= 1 {
				typeArg = objectType.Priv.TypeArgs[0]
			}

			if IsAnyOrUnknown(typeArg) {
				return &TypeResult{Type: typeArg, ClassType: UnknownTypeCreate(false)}
			}
		}
	}

	if diag != nil && subDiag != nil {
		diag.AddAddendum(subDiag)
	}

	return nil
}

// metaclassDescriptorSatisfiesLookup is the original's first metaclass probe.
//
// Its comment: look up the attribute in the metaclass first. If the member is a
// descriptor (an object with a __get__ and __set__ method) and the access is a
// 'get', the Python runtime uses this descriptor to satisfy the lookup. Skip
// this costly lookup in the common case where the metaclass is 'type' since we
// know that `type` doesn't have any attributes that are descriptors.
func (e *typeEvaluator) metaclassDescriptorSatisfiesLookup(
	errorNode parser.ExpressionNode,
	objectType *ClassType,
	memberName string,
	usage *EvaluatorUsage,
	flags MemberAccessFlags,
	objectTypeIsInstantiable bool,
	metaclass Type,
	recursionCount int,
) bool {
	if usage.Method != "get" || !objectTypeIsInstantiable ||
		metaclass == nil || !IsInstantiableClass(metaclass) {
		return false
	}

	metaclassType := metaclass.(*ClassType)
	if ClassTypeIsBuiltInNamed(metaclassType, "type") ||
		ClassTypeIsSameGenericClass(metaclassType, objectType, 0) {
		return false
	}

	descMemberInfo := e.getTypeOfClassMemberName(errorNode, metaclassType, memberName, usage, nil,
		flags|MemberAccessFlagsSkipAttributeAccessOverride|MemberAccessFlagsSkipTypedDictEntries,
		objectType, recursionCount)
	if descMemberInfo == nil {
		return false
	}

	isProperty := IsClassInstance(descMemberInfo.Type) &&
		ClassTypeIsPropertyClass(descMemberInfo.Type.(*ClassType))

	// requireSetter: only a DATA descriptor takes precedence over the instance
	// dictionary. A non-data descriptor does not, so it must not skip the lookup.
	return IsDescriptorInstance(descMemberInfo.Type, true) || isProperty
}

/*
 * The three constructors.ts wrappers, which differ only in their flags.
 */

// GetBoundNewMethod corresponds to the function of the same name. The original's
// comment on the family: fetches and binds the __new__ method from a class.
//
// TreatConstructorAsClassMethod is what makes __new__ bind its first parameter to
// the class: at runtime it is an implicit staticmethod, but for type checking it
// behaves like a classmethod.
// additionalFlags has no default in Go, and the original's is NOT zero: it is
// MemberAccessFlags.SkipObjectBaseClass. Passing MemberAccessFlagsDefault here
// is meaningful and deliberate at the two sites that do it -- the original
// spells `MemberAccessFlags.Default` explicitly at both -- but it is the wrong
// answer anywhere the original simply omits the argument.
func GetBoundNewMethod(
	evaluator TypeEvaluator,
	errorNode parser.ExpressionNode,
	t *ClassType,
	diag *common.DiagnosticAddendum,
	additionalFlags MemberAccessFlags,
) *TypeResult {
	flags := MemberAccessFlagsSkipClassMembers |
		MemberAccessFlagsSkipAttributeAccessOverride |
		MemberAccessFlagsTreatConstructorAsClassMethod |
		additionalFlags

	return evaluator.GetTypeOfBoundMember(errorNode, t, "__new__",
		&EvaluatorUsage{Method: "get"}, diag, flags, nil)
}

// GetBoundInitMethod corresponds to the function of the same name. The original's
// comment: fetches and binds the __init__ method from a class instance.
// See GetBoundNewMethod on additionalFlags: the original's default is
// SkipObjectBaseClass, not Default.
func GetBoundInitMethod(
	evaluator TypeEvaluator,
	errorNode parser.ExpressionNode,
	t *ClassType,
	diag *common.DiagnosticAddendum,
	additionalFlags MemberAccessFlags,
) *TypeResult {
	flags := MemberAccessFlagsSkipInstanceMembers |
		MemberAccessFlagsSkipAttributeAccessOverride |
		additionalFlags

	return evaluator.GetTypeOfBoundMember(errorNode, t, "__init__",
		&EvaluatorUsage{Method: "get"}, diag, flags, nil)
}

// GetBoundCallMethod corresponds to the function of the same name. The original's
// comment: fetches and binds the __call__ method from a class or its metaclass.
//
// SkipTypeBaseClass matters here: every class inherits `type.__call__`, and
// finding that would make every class look like it has a custom __call__.
func GetBoundCallMethod(
	evaluator TypeEvaluator, errorNode parser.ExpressionNode, t *ClassType,
) *TypeResult {
	return evaluator.GetTypeOfBoundMember(errorNode, t, "__call__",
		&EvaluatorUsage{Method: "get"}, nil,
		MemberAccessFlagsSkipInstanceMembers|
			MemberAccessFlagsSkipTypeBaseClass|
			MemberAccessFlagsSkipAttributeAccessOverride,
		nil)
}
