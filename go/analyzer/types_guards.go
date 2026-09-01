/*
 * types_guards.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 * Author: Eric Traut
 *
 * The free functions at the end of types.ts: the type guards, getTypeAliasInfo
 * and isTypeSame.
 *
 * Transliterated from analyzer/types.ts (pyright 1.1.412).
 *
 * TypeScript's `function isClass(type: Type): type is ClassType` both answers a
 * question and narrows the static type. Go can do one or the other, so each
 * narrowing guard appears twice: IsX reports the boolean, and AsX returns the
 * concrete pointer with an ok result. They are the same predicate; use whichever
 * the call site needs.
 */

package analyzer

// IsNever corresponds to isNever.
func IsNever(t Type) bool {
	return t.Base().Category == TypeCategoryNever
}

// AsNever narrows to *NeverType.
func AsNever(t Type) (*NeverType, bool) {
	v, ok := t.(*NeverType)
	return v, ok
}

// IsAny corresponds to isAny.
func IsAny(t Type) bool {
	return t.Base().Category == TypeCategoryAny
}

// AsAny narrows to *AnyType.
func AsAny(t Type) (*AnyType, bool) {
	v, ok := t.(*AnyType)
	return v, ok
}

// IsUnknown corresponds to isUnknown.
func IsUnknown(t Type) bool {
	return t.Base().Category == TypeCategoryUnknown
}

// AsUnknown narrows to *UnknownType.
func AsUnknown(t Type) (*UnknownType, bool) {
	v, ok := t.(*UnknownType)
	return v, ok
}

// IsAnyOrUnknown corresponds to isAnyOrUnknown. Note that it also answers true
// for a union whose every subtype is Any or Unknown.
func IsAnyOrUnknown(t Type) bool {
	if t.Base().Category == TypeCategoryAny || t.Base().Category == TypeCategoryUnknown {
		return true
	}

	if union, ok := AsUnion(t); ok {
		for _, subtype := range union.Priv.Subtypes {
			if !IsAnyOrUnknown(subtype) {
				return false
			}
		}
		return true
	}

	return false
}

// IsUnbound corresponds to isUnbound.
func IsUnbound(t Type) bool {
	return t.Base().Category == TypeCategoryUnbound
}

// AsUnbound narrows to *UnboundType.
func AsUnbound(t Type) (*UnboundType, bool) {
	v, ok := t.(*UnboundType)
	return v, ok
}

// IsUnion corresponds to isUnion.
func IsUnion(t Type) bool {
	return t.Base().Category == TypeCategoryUnion
}

// AsUnion narrows to *UnionType.
func AsUnion(t Type) (*UnionType, bool) {
	v, ok := t.(*UnionType)
	return v, ok
}

// IsPossiblyUnbound corresponds to isPossiblyUnbound.
func IsPossiblyUnbound(t Type) bool {
	if IsUnbound(t) {
		return true
	}

	if union, ok := AsUnion(t); ok {
		for _, subtype := range union.Priv.Subtypes {
			if IsPossiblyUnbound(subtype) {
				return true
			}
		}
		return false
	}

	return false
}

// IsClass corresponds to isClass.
func IsClass(t Type) bool {
	return t.Base().Category == TypeCategoryClass
}

// AsClass narrows to *ClassType.
func AsClass(t Type) (*ClassType, bool) {
	v, ok := t.(*ClassType)
	return v, ok
}

// IsInstantiableClass corresponds to isInstantiableClass.
func IsInstantiableClass(t Type) bool {
	return t.Base().Category == TypeCategoryClass && t.Base().IsInstantiable()
}

// AsInstantiableClass narrows to *ClassType when the type is an instantiable
// class.
func AsInstantiableClass(t Type) (*ClassType, bool) {
	v, ok := t.(*ClassType)
	if !ok || !v.IsInstantiable() {
		return nil, false
	}
	return v, true
}

// IsClassInstance corresponds to isClassInstance.
func IsClassInstance(t Type) bool {
	return t.Base().Category == TypeCategoryClass && t.Base().IsInstance()
}

// AsClassInstance narrows to *ClassType when the type is a class instance.
func AsClassInstance(t Type) (*ClassType, bool) {
	v, ok := t.(*ClassType)
	if !ok || !v.IsInstance() {
		return nil, false
	}
	return v, true
}

// IsModule corresponds to isModule.
func IsModule(t Type) bool {
	return t.Base().Category == TypeCategoryModule
}

// AsModule narrows to *ModuleType.
func AsModule(t Type) (*ModuleType, bool) {
	v, ok := t.(*ModuleType)
	return v, ok
}

// IsTypeVar corresponds to isTypeVar.
func IsTypeVar(t Type) bool {
	return t.Base().Category == TypeCategoryTypeVar
}

// AsTypeVar narrows to *TypeVarType.
func AsTypeVar(t Type) (*TypeVarType, bool) {
	v, ok := t.(*TypeVarType)
	return v, ok
}

// IsParamSpec corresponds to isParamSpec.
func IsParamSpec(t Type) bool {
	v, ok := t.(*TypeVarType)
	return ok && v.Shared.Kind == TypeVarKindParamSpec
}

// AsParamSpec narrows to *ParamSpecType.
func AsParamSpec(t Type) (*ParamSpecType, bool) {
	v, ok := t.(*TypeVarType)
	if !ok || v.Shared.Kind != TypeVarKindParamSpec {
		return nil, false
	}
	return v, true
}

// IsTypeVarTuple corresponds to isTypeVarTuple.
func IsTypeVarTuple(t Type) bool {
	v, ok := t.(*TypeVarType)
	return ok && v.Shared.Kind == TypeVarKindTypeVarTuple
}

// AsTypeVarTuple narrows to *TypeVarTupleType.
func AsTypeVarTuple(t Type) (*TypeVarTupleType, bool) {
	v, ok := t.(*TypeVarType)
	if !ok || v.Shared.Kind != TypeVarKindTypeVarTuple {
		return nil, false
	}
	return v, true
}

// IsUnpackedTypeVarTuple corresponds to isUnpackedTypeVarTuple.
func IsUnpackedTypeVarTuple(t Type) bool {
	v, ok := AsTypeVarTuple(t)
	return ok && v.Priv.IsUnpacked && !v.Priv.IsInUnion
}

// IsUnpackedTypeVar corresponds to isUnpackedTypeVar.
func IsUnpackedTypeVar(t Type) bool {
	v, ok := AsTypeVar(t)
	return ok && !IsTypeVarTuple(t) && v.Priv.IsUnpacked
}

// IsUnpackedClass corresponds to isUnpackedClass.
func IsUnpackedClass(t Type) bool {
	v, ok := AsClass(t)
	if !ok || !v.Priv.IsUnpacked {
		return false
	}

	return true
}

// IsUnpacked corresponds to isUnpacked.
func IsUnpacked(t Type) bool {
	return IsUnpackedTypeVarTuple(t) || IsUnpackedTypeVar(t) || IsUnpackedClass(t)
}

// IsFunction corresponds to isFunction.
func IsFunction(t Type) bool {
	return t.Base().Category == TypeCategoryFunction
}

// AsFunction narrows to *FunctionType.
func AsFunction(t Type) (*FunctionType, bool) {
	v, ok := t.(*FunctionType)
	return v, ok
}

// IsOverloaded corresponds to isOverloaded.
func IsOverloaded(t Type) bool {
	return t.Base().Category == TypeCategoryOverloaded
}

// AsOverloaded narrows to *OverloadedType.
func AsOverloaded(t Type) (*OverloadedType, bool) {
	v, ok := t.(*OverloadedType)
	return v, ok
}

// IsFunctionOrOverloaded corresponds to isFunctionOrOverloaded.
func IsFunctionOrOverloaded(t Type) bool {
	return t.Base().Category == TypeCategoryFunction || t.Base().Category == TypeCategoryOverloaded
}

// IsMethodType corresponds to isMethodType. The argument is a FunctionType or
// OverloadedType.
func IsMethodType(t Type) bool {
	var funcType *FunctionType

	if fn, ok := AsFunction(t); ok {
		funcType = fn
	} else {
		overloaded := t.(*OverloadedType)
		if len(overloaded.Priv.Overloads) == 0 {
			return false
		}
		funcType = overloaded.Priv.Overloads[0]
	}

	// __new__ methods are never really bound at runtime.
	//
	// The original guards on `preBoundFlags !== undefined`. Go has no
	// undefined here: the zero value is FunctionTypeFlagsNone, and the mask
	// test below already answers false for it, so the guard is redundant.
	if (funcType.Priv.PreBoundFlags & FunctionTypeFlagsConstructorMethod) != 0 {
		return false
	}

	// If the function type has a stripped first parameter type, it was bound
	// to a class or object and is therefore a MethodType rather than a
	// FunctionType.
	return funcType.Priv.StrippedFirstParamType != nil
}

// GetTypeAliasInfo corresponds to getTypeAliasInfo. It returns nil where the
// TypeScript returns undefined.
func GetTypeAliasInfo(t Type) *TypeAliasInfo {
	if t.Base().Props != nil && t.Base().Props.TypeAliasInfo != nil {
		return t.Base().Props.TypeAliasInfo
	}

	if tv, ok := AsTypeVar(t); ok &&
		tv.Shared.RecursiveAlias != nil &&
		tv.Shared.BoundType != nil &&
		tv.Shared.BoundType.Base().Props != nil &&
		tv.Shared.BoundType.Base().Props.TypeAliasInfo != nil {
		return tv.Shared.BoundType.Base().Props.TypeAliasInfo
	}

	return nil
}
