/*
 * types_function.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 * Author: Eric Traut
 *
 * FunctionParam, FunctionType and the FunctionType namespace.
 *
 * Transliterated from analyzer/types.ts (pyright 1.1.412). Split out of
 * types.go only so no single Go file has to carry all 4000 lines.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/parser"
)

// sliceCopy stands in for JavaScript's Array.prototype.slice, which always
// returns a fresh array. Go's s[a:b] shares the backing store, so a later
// append through one slice can overwrite elements visible through the other --
// exactly the aliasing the original cannot produce.
func sliceCopy[T any](s []T) []T {
	if s == nil {
		return nil
	}
	out := make([]T, len(s))
	copy(out, s)
	return out
}

// FunctionParamFlags corresponds to the enum of the same name.
type FunctionParamFlags int

const (
	FunctionParamFlagsNone FunctionParamFlags = 0

	// FunctionParamFlagsNameSynthesized reports whether the name of the
	// parameter is synthesized internally.
	FunctionParamFlagsNameSynthesized FunctionParamFlags = 1 << 0

	// FunctionParamFlagsTypeDeclared reports whether the parameter has an
	// explicitly-declared type.
	FunctionParamFlagsTypeDeclared FunctionParamFlags = 1 << 1

	// FunctionParamFlagsTypeInferred reports whether the type of the parameter
	// is inferred.
	FunctionParamFlagsTypeInferred FunctionParamFlags = 1 << 2
)

// FunctionParam corresponds to the interface of the same name.
type FunctionParam struct {
	Category parser.ParamCategory
	Flags    FunctionParamFlags

	// Name is nil where the TypeScript has `string | undefined`. A nameless
	// parameter is a "/" or "*" separator, so the distinction matters.
	Name *string

	// TypeField and DefaultTypeField correspond to the original's `_type` and
	// `_defaultType`, which are named with a leading underscore precisely
	// because they must be read through FunctionTypeGetParamType and
	// FunctionTypeGetParamDefaultType -- those consult the function's
	// specializedTypes first.
	TypeField        Type
	DefaultTypeField Type

	DefaultExpr parser.ExpressionNode
}

// FunctionParamCreate corresponds to FunctionParam.create.
func FunctionParamCreate(
	category parser.ParamCategory,
	t Type,
	flags FunctionParamFlags,
	name *string,
	defaultType Type,
	defaultExpr parser.ExpressionNode,
) FunctionParam {
	return FunctionParam{
		Category:         category,
		Flags:            flags,
		Name:             name,
		TypeField:        t,
		DefaultTypeField: defaultType,
		DefaultExpr:      defaultExpr,
	}
}

func FunctionParamIsNameSynthesized(param FunctionParam) bool {
	return (param.Flags & FunctionParamFlagsNameSynthesized) != 0
}

func FunctionParamIsTypeDeclared(param FunctionParam) bool {
	return (param.Flags & FunctionParamFlagsTypeDeclared) != 0
}

func FunctionParamIsTypeInferred(param FunctionParam) bool {
	return (param.Flags & FunctionParamFlagsTypeInferred) != 0
}

// IsPositionOnlySeparator reports whether the parameter is a "/" separator: a
// simple parameter with no name.
func IsPositionOnlySeparator(param FunctionParam) bool {
	return param.Category == parser.ParamCategorySimple && (param.Name == nil || *param.Name == "")
}

// IsKeywordOnlySeparator reports whether the parameter is a "*" separator: an
// *args parameter with no name.
func IsKeywordOnlySeparator(param FunctionParam) bool {
	return param.Category == parser.ParamCategoryArgsList && (param.Name == nil || *param.Name == "")
}

// FunctionTypeFlags corresponds to the const enum of the same name.
//
// Note that bit 10 is unused: the original jumps from Async (1 << 9) to
// StubDefinition (1 << 11).
type FunctionTypeFlags int

const (
	FunctionTypeFlagsNone FunctionTypeFlags = 0

	// FunctionTypeFlagsConstructorMethod marks a __new__ method; the first
	// parameter is "cls".
	FunctionTypeFlagsConstructorMethod FunctionTypeFlags = 1 << 0

	// FunctionTypeFlagsClassMethod marks a function decorated with
	// @classmethod; the first parameter is "cls" and it can be bound to the
	// associated class.
	FunctionTypeFlagsClassMethod FunctionTypeFlags = 1 << 1

	// FunctionTypeFlagsStaticMethod marks a function decorated with
	// @staticmethod, which cannot be bound to a class.
	FunctionTypeFlagsStaticMethod FunctionTypeFlags = 1 << 2

	// FunctionTypeFlagsAbstractMethod marks a function decorated with
	// @abstractmethod.
	FunctionTypeFlagsAbstractMethod FunctionTypeFlags = 1 << 3

	// FunctionTypeFlagsGenerator marks a function containing "yield" or
	// "yield from" statements.
	FunctionTypeFlagsGenerator FunctionTypeFlags = 1 << 4

	// FunctionTypeFlagsDisableDefaultChecks skips the check that validates
	// that all parameters without default value expressions have
	// corresponding arguments; used for named tuples in some cases.
	FunctionTypeFlagsDisableDefaultChecks FunctionTypeFlags = 1 << 5

	// FunctionTypeFlagsSynthesizedMethod marks a method with no declaration in
	// user code; used for implied methods such as those used in namedtuple,
	// dataclass, etc.
	FunctionTypeFlagsSynthesizedMethod FunctionTypeFlags = 1 << 6

	// FunctionTypeFlagsTypeCheckOnly marks a function decorated with
	// @type_check_only.
	FunctionTypeFlagsTypeCheckOnly FunctionTypeFlags = 1 << 7

	// FunctionTypeFlagsOverloaded marks a function decorated with @overload.
	FunctionTypeFlagsOverloaded FunctionTypeFlags = 1 << 8

	// FunctionTypeFlagsAsync marks a function declared with the async keyword.
	FunctionTypeFlagsAsync FunctionTypeFlags = 1 << 9

	// FunctionTypeFlagsStubDefinition marks a function declared within a type
	// stub file.
	FunctionTypeFlagsStubDefinition FunctionTypeFlags = 1 << 11

	// FunctionTypeFlagsPyTypedDefinition marks a function declared within a
	// module that claims to be fully typed (i.e. a "py.typed" file is
	// present).
	FunctionTypeFlagsPyTypedDefinition FunctionTypeFlags = 1 << 12

	// FunctionTypeFlagsFinal marks a function decorated with @final.
	FunctionTypeFlagsFinal FunctionTypeFlags = 1 << 13

	// FunctionTypeFlagsUnannotatedParams marks a function with one or more
	// parameters missing type annotations.
	FunctionTypeFlagsUnannotatedParams FunctionTypeFlags = 1 << 14

	// FunctionTypeFlagsGradualCallableForm means the *args and **kwargs
	// parameters do not need to be present for this function to be compatible.
	// This is used for Callable[..., x] and ... type arguments to ParamSpec
	// and Concatenate.
	FunctionTypeFlagsGradualCallableForm FunctionTypeFlags = 1 << 15

	// FunctionTypeFlagsParamSpecValue means this function represents the value
	// bound to a ParamSpec, so its return type is not meaningful.
	FunctionTypeFlagsParamSpecValue FunctionTypeFlags = 1 << 16

	// FunctionTypeFlagsPartiallyEvaluated means the function type is in the
	// process of being evaluated and is not yet complete. This allows us to
	// detect cases where the function refers to itself (e.g. uses a type
	// annotation that contains a forward reference that requires the function
	// type itself to be evaluated first).
	FunctionTypeFlagsPartiallyEvaluated FunctionTypeFlags = 1 << 17

	// FunctionTypeFlagsOverridden marks a function decorated with @override as
	// defined in PEP 698.
	FunctionTypeFlagsOverridden FunctionTypeFlags = 1 << 18

	// FunctionTypeFlagsNoTypeCheck marks a function decorated with
	// @no_type_check.
	FunctionTypeFlagsNoTypeCheck FunctionTypeFlags = 1 << 19

	// FunctionTypeFlagsBuiltIn marks a function defined in one of the core
	// stdlib modules.
	FunctionTypeFlagsBuiltIn FunctionTypeFlags = 1 << 20
)

// InferredReturnTypeInfo corresponds to the anonymous inferredReturnType object
// in FunctionDetailsShared.
type InferredReturnTypeInfo struct {
	Type            Type
	IsIncomplete    bool
	EvaluationCount int
}

// FunctionDetailsShared corresponds to the interface of the same name. The
// original does not export it.
type FunctionDetailsShared struct {
	Name               string
	FullName           string
	ModuleName         string
	Flags              FunctionTypeFlags
	TypeParams         []*TypeVarType
	Parameters         []FunctionParam
	DeclaredReturnType Type
	Declaration        *FunctionDeclaration
	TypeVarScopeID     TypeVarScopeId
	DocString          *string
	DeprecatedMessage  *string

	// MethodClass refers to the class that contains this function, if it is a
	// method.
	MethodClass *ClassType

	// DecoratorDataClassBehaviors holds transforms to apply if this function is
	// used as a decorator.
	DecoratorDataClassBehaviors *DataClassBehaviors

	// InferredReturnType is filled in lazily.
	InferredReturnType *InferredReturnTypeInfo
}

// SpecializedFunctionTypes corresponds to the interface of the same name.
type SpecializedFunctionTypes struct {
	// ParameterTypes holds specialized types for each of the parameters in the
	// "parameters" array.
	ParameterTypes []Type

	// ParameterDefaultTypes holds specialized types of default arguments for
	// each parameter in the "parameters" array. If an entry is nil or the
	// entire slice is missing, there is no specialized type, and the original
	// "defaultType" should be used.
	ParameterDefaultTypes []Type

	// ReturnType is the specialized type of the declared return type. Nil if
	// there is no declared return type.
	ReturnType Type
}

// CallSiteInferenceTypeCacheEntry corresponds to the interface of the same
// name.
type CallSiteInferenceTypeCacheEntry struct {
	ParamTypes []Type
	ReturnType Type
}

// SignatureWithOffsets corresponds to the interface of the same name. Type
// holds a FunctionType or OverloadedType.
type SignatureWithOffsets struct {
	Type              Type
	ExpressionOffsets []int
}

// FunctionDetailsPriv corresponds to the interface of the same name.
type FunctionDetailsPriv struct {
	// ConstructorTypeVarScopeID is the TypeVar scope ID of the associated
	// class, for __new__ and __init__ methods.
	ConstructorTypeVarScopeID TypeVarScopeId

	// SpecializedTypes holds the specialization of a function type (i.e.
	// generic type variables replaced by a concrete type).
	SpecializedTypes *SpecializedFunctionTypes

	// CallSiteReturnTypeCache is the call-site return type inference cache.
	CallSiteReturnTypeCache []*CallSiteInferenceTypeCacheEntry

	// StrippedFirstParamType is the (specialized) type of the stripped
	// parameter, if this is a bound function where the first parameter was
	// stripped from the original unbound function.
	StrippedFirstParamType Type

	// BoundToType is the class or object to which the function was bound, if
	// this is a bound function where the first parameter was stripped from the
	// original unbound function.
	BoundToType *ClassType

	// PreBoundFlags holds the flags for the function prior to binding.
	PreBoundFlags FunctionTypeFlags

	// Overloaded refers back to the overloaded function type, if this function
	// is part of one.
	Overloaded *OverloadedType

	// IsCallableWithTypeArgs records whether this function was created with a
	// "Callable" annotation with type arguments. This allows us to detect and
	// report an error when it is used in an isinstance call.
	IsCallableWithTypeArgs bool

	// paramListDetails has no TypeScript counterpart: it memoizes
	// GetParamListDetails, which the original recomputes on every call-site
	// validation. See paramListDetailsCacheEntry for the validity rules.
	// Clones copy the pointer, which is safe because a hit requires the
	// fingerprint to match the clone's own state.
	paramListDetails *paramListDetailsCacheEntry
}

// FunctionType corresponds to the interface of the same name.
type FunctionType struct {
	TypeBase
	Shared *FunctionDetailsShared
	Priv   FunctionDetailsPriv
}

func (t *FunctionType) cloneSelf() Type {
	clone := *t
	clone.cloneBaseInto()
	return &clone
}

func (t *FunctionType) isUnionable() {}

// FunctionTypeCreateInstance corresponds to FunctionType.createInstance.
func FunctionTypeCreateInstance(
	name, fullName, moduleName string,
	functionFlags FunctionTypeFlags,
	docString *string,
) *FunctionType {
	return functionTypeCreate(name, fullName, moduleName, functionFlags, TypeFlagsInstance, docString)
}

// FunctionTypeCreateInstantiable corresponds to
// FunctionType.createInstantiable.
func FunctionTypeCreateInstantiable(functionFlags FunctionTypeFlags, docString *string) *FunctionType {
	return functionTypeCreate("", "", "", functionFlags, TypeFlagsInstantiable, docString)
}

// FunctionTypeCreateSynthesizedInstance corresponds to
// FunctionType.createSynthesizedInstance. The TypeScript defaults
// additionalFlags to None.
func FunctionTypeCreateSynthesizedInstance(name string, additionalFlags FunctionTypeFlags) *FunctionType {
	return functionTypeCreate(name, name, "", additionalFlags|FunctionTypeFlagsSynthesizedMethod, TypeFlagsInstance, nil)
}

// functionTypeCreate corresponds to the unexported create.
func functionTypeCreate(
	name, fullName, moduleName string,
	functionFlags FunctionTypeFlags,
	typeFlags TypeFlags,
	docString *string,
) *FunctionType {
	return &FunctionType{
		TypeBase: TypeBase{
			Category: TypeCategoryFunction,
			Flags:    typeFlags,
		},
		Shared: &FunctionDetailsShared{
			Name:       name,
			FullName:   fullName,
			ModuleName: moduleName,
			Flags:      functionFlags,
			TypeParams: []*TypeVarType{},
			Parameters: []FunctionParam{},
			DocString:  docString,
		},
	}
}

// FunctionTypeClone creates a deep copy of the function type, including a fresh
// version of the shared details. The TypeScript defaults stripFirstParam to
// false and boundToType to undefined.
func FunctionTypeClone(t *FunctionType, stripFirstParam bool, boundToType *ClassType) *FunctionType {
	newFunction := CloneType(t)

	sharedCopy := *t.Shared
	newFunction.Shared = &sharedCopy
	newFunction.Priv.PreBoundFlags = newFunction.Shared.Flags
	newFunction.Priv.BoundToType = boundToType

	if boundToType != nil {
		if t.Shared.Name == "__new__" || t.Shared.Name == "__init__" {
			newFunction.Priv.ConstructorTypeVarScopeID = boundToType.Shared.TypeVarScopeID
		}
	}

	if stripFirstParam {
		if len(t.Shared.Parameters) > 0 {
			if t.Shared.Parameters[0].Category == parser.ParamCategorySimple {
				// Stash away the effective type of the first parameter or Any
				// if it was inferred.
				//
				// The original repeats the `parameters.length > 0` check here;
				// it is already known to hold.
				if FunctionParamIsTypeInferred(t.Shared.Parameters[0]) {
					newFunction.Priv.StrippedFirstParamType = AnyTypeCreate(false)
				} else {
					newFunction.Priv.StrippedFirstParamType = FunctionTypeGetParamType(t, 0)
				}
				newFunction.Shared.Parameters = sliceCopy(t.Shared.Parameters[1:])
			}
		} else {
			stripFirstParam = false
		}
	}

	if t.Props != nil && t.Props.TypeAliasInfo != nil {
		newFunction.SetTypeAliasInfo(t.Props.TypeAliasInfo)
	}

	if t.Priv.SpecializedTypes != nil {
		specialized := &SpecializedFunctionTypes{
			ReturnType: t.Priv.SpecializedTypes.ReturnType,
		}
		if stripFirstParam {
			specialized.ParameterTypes = sliceCopy(t.Priv.SpecializedTypes.ParameterTypes[1:])
			if t.Priv.SpecializedTypes.ParameterDefaultTypes != nil {
				specialized.ParameterDefaultTypes = sliceCopy(t.Priv.SpecializedTypes.ParameterDefaultTypes[1:])
			}
		} else {
			specialized.ParameterTypes = t.Priv.SpecializedTypes.ParameterTypes
			specialized.ParameterDefaultTypes = t.Priv.SpecializedTypes.ParameterDefaultTypes
		}
		newFunction.Priv.SpecializedTypes = specialized
	}

	newFunction.Shared.InferredReturnType = t.Shared.InferredReturnType

	return newFunction
}

// FunctionTypeCloneAsInstance corresponds to FunctionType.cloneAsInstance.
func FunctionTypeCloneAsInstance(t *FunctionType) *FunctionType {
	if t.Cached != nil && t.Cached.TypeBaseInstanceType != nil {
		return t.Cached.TypeBaseInstanceType.(*FunctionType)
	}

	newInstance := CloneTypeAsInstance(t, true)
	if newInstance.Props != nil && newInstance.Props.SpecialForm != nil {
		newInstance.SetSpecialForm(nil)
	}
	return newInstance
}

// FunctionTypeCloneAsInstantiable corresponds to
// FunctionType.cloneAsInstantiable.
func FunctionTypeCloneAsInstantiable(t *FunctionType) *FunctionType {
	if t.Cached != nil && t.Cached.TypeBaseInstantiableType != nil {
		return t.Cached.TypeBaseInstantiableType.(*FunctionType)
	}

	return CloneTypeAsInstantiable(t, true)
}

// FunctionTypeSpecialize creates a shallow copy of the function type with new
// specialized types. The clone shares the shared details with the object being
// cloned.
func FunctionTypeSpecialize(t *FunctionType, specializedTypes *SpecializedFunctionTypes) *FunctionType {
	newFunction := CloneType(t)

	assert(len(specializedTypes.ParameterTypes) == len(t.Shared.Parameters), "")
	if specializedTypes.ParameterDefaultTypes != nil {
		assert(len(specializedTypes.ParameterDefaultTypes) == len(t.Shared.Parameters), "")
	}

	newFunction.Priv.SpecializedTypes = specializedTypes
	return newFunction
}

// FunctionTypeApplyParamSpecValue creates a new function based on the
// parameters of another function.
func FunctionTypeApplyParamSpecValue(t *FunctionType, paramSpecValue *FunctionType) *FunctionType {
	hasPositionalOnly := false
	for _, param := range paramSpecValue.Shared.Parameters {
		if IsPositionOnlySeparator(param) {
			hasPositionalOnly = true
			break
		}
	}

	newFunction := FunctionTypeCloneRemoveParamSpecArgsKwargs(CloneType(t), hasPositionalOnly)
	paramSpec := FunctionTypeGetParamSpecFromArgsKwargs(t)
	assert(paramSpec != nil, "")

	// Make a shallow clone of the details.
	sharedCopy := *newFunction.Shared
	newFunction.Shared = &sharedCopy

	remainingTypeParams := []*TypeVarType{}
	for _, tp := range newFunction.Shared.TypeParams {
		if !IsTypeSame(tp, paramSpec, TypeSameOptions{}, 0) {
			remainingTypeParams = append(remainingTypeParams, tp)
		}
	}
	newFunction.Shared.TypeParams = remainingTypeParams

	prevParams := append([]FunctionParam{}, newFunction.Shared.Parameters...)

	for index, param := range paramSpecValue.Shared.Parameters {
		prevParams = append(prevParams, FunctionParamCreate(
			param.Category,
			FunctionTypeGetParamType(paramSpecValue, index),
			(param.Flags&FunctionParamFlagsNameSynthesized)|FunctionParamFlagsTypeDeclared,
			param.Name,
			FunctionTypeGetParamDefaultType(paramSpecValue, index),
			param.DefaultExpr,
		))
	}
	newFunction.Shared.Parameters = prevParams

	if newFunction.Shared.DocString == nil {
		newFunction.Shared.DocString = paramSpecValue.Shared.DocString
	}

	if newFunction.Shared.DeprecatedMessage == nil {
		newFunction.Shared.DeprecatedMessage = paramSpecValue.Shared.DeprecatedMessage
	}

	origFlagsMask := FunctionTypeFlagsOverloaded | FunctionTypeFlagsParamSpecValue
	newFunction.Shared.Flags = t.Shared.Flags & origFlagsMask

	methodFlagsMask := FunctionTypeFlagsClassMethod | FunctionTypeFlagsStaticMethod | FunctionTypeFlagsConstructorMethod

	// If the original function was a method, use its method type. Otherwise use
	// the method type of the param spec.
	if t.Shared.MethodClass != nil {
		newFunction.Shared.Flags |= t.Shared.Flags & methodFlagsMask
	} else {
		newFunction.Shared.Flags |= paramSpecValue.Shared.Flags & methodFlagsMask
	}

	// Use the "..." flag from the param spec.
	newFunction.Shared.Flags |= paramSpecValue.Shared.Flags & FunctionTypeFlagsGradualCallableForm

	// Mark the function as synthesized since there is no user-defined
	// declaration for it.
	newFunction.Shared.Flags |= FunctionTypeFlagsSynthesizedMethod
	if newFunction.Shared.Declaration != nil {
		newFunction.Shared.Declaration = nil
	}

	// Update the specialized parameter types as well.
	specializedTypes := newFunction.Priv.SpecializedTypes
	if specializedTypes != nil {
		for index := range paramSpecValue.Shared.Parameters {
			specializedTypes.ParameterTypes = append(
				specializedTypes.ParameterTypes,
				FunctionTypeGetParamType(paramSpecValue, index),
			)

			if specializedTypes.ParameterDefaultTypes != nil {
				specializedTypes.ParameterDefaultTypes = append(
					specializedTypes.ParameterDefaultTypes,
					FunctionTypeGetParamDefaultType(paramSpecValue, index),
				)
			}
		}
	}

	newFunction.Priv.ConstructorTypeVarScopeID = paramSpecValue.Priv.ConstructorTypeVarScopeID

	if newFunction.Shared.MethodClass == nil && paramSpecValue.Shared.MethodClass != nil {
		newFunction.Shared.MethodClass = paramSpecValue.Shared.MethodClass
	}

	return newFunction
}

// FunctionTypeCloneWithNewFlags corresponds to FunctionType.cloneWithNewFlags.
func FunctionTypeCloneWithNewFlags(t *FunctionType, flags FunctionTypeFlags) *FunctionType {
	newFunction := CloneType(t)

	// Make a shallow clone of the details.
	sharedCopy := *t.Shared
	newFunction.Shared = &sharedCopy
	newFunction.Shared.Flags = flags

	return newFunction
}

// FunctionTypeCloneWithNewTypeVarScopeID corresponds to
// FunctionType.cloneWithNewTypeVarScopeId.
func FunctionTypeCloneWithNewTypeVarScopeID(
	t *FunctionType,
	newScopeID TypeVarScopeId,
	newConstructorScopeID TypeVarScopeId,
	typeParams []*TypeVarType,
) *FunctionType {
	newFunction := CloneType(t)

	// Make a shallow clone of the details.
	sharedCopy := *t.Shared
	newFunction.Shared = &sharedCopy
	newFunction.Shared.TypeVarScopeID = newScopeID
	newFunction.Priv.ConstructorTypeVarScopeID = newConstructorScopeID
	newFunction.Shared.TypeParams = typeParams

	return newFunction
}

// FunctionTypeCloneWithDocString corresponds to
// FunctionType.cloneWithDocString.
func FunctionTypeCloneWithDocString(t *FunctionType, docString *string) *FunctionType {
	newFunction := CloneType(t)

	// Make a shallow clone of the details.
	sharedCopy := *t.Shared
	newFunction.Shared = &sharedCopy

	newFunction.Shared.DocString = docString

	return newFunction
}

// FunctionTypeCloneWithDeprecatedMessage corresponds to
// FunctionType.cloneWithDeprecatedMessage.
func FunctionTypeCloneWithDeprecatedMessage(t *FunctionType, deprecatedMessage *string) *FunctionType {
	newFunction := CloneType(t)

	// Make a shallow clone of the details.
	sharedCopy := *t.Shared
	newFunction.Shared = &sharedCopy

	newFunction.Shared.DeprecatedMessage = deprecatedMessage

	return newFunction
}

// FunctionTypeCloneRemoveParamSpecArgsKwargs returns a clone of the input
// function with the *args and **kwargs parameters removed, if the function ends
// with "*args: P.args, **kwargs: P.kwargs". If stripPositionOnlySeparator is
// true, a trailing positional-only separator will be removed.
func FunctionTypeCloneRemoveParamSpecArgsKwargs(t *FunctionType, stripPositionOnlySeparator bool) *FunctionType {
	paramCount := len(t.Shared.Parameters)
	if paramCount < 2 {
		return t
	}

	argsParam := t.Shared.Parameters[paramCount-2]
	kwargsParam := t.Shared.Parameters[paramCount-1]

	if argsParam.Category != parser.ParamCategoryArgsList || kwargsParam.Category != parser.ParamCategoryKwargsDict {
		return t
	}

	argsType := FunctionTypeGetParamType(t, paramCount-2)
	kwargsType := FunctionTypeGetParamType(t, paramCount-1)
	if !IsParamSpec(argsType) || !IsParamSpec(kwargsType) || !IsTypeSame(argsType, kwargsType, TypeSameOptions{}, 0) {
		return t
	}

	newFunction := CloneType(t)

	// Make a shallow clone of the details.
	sharedCopy := *t.Shared
	newFunction.Shared = &sharedCopy
	details := newFunction.Shared

	paramsToDrop := 2

	// If the last remaining parameter is a position-only separator, remove it
	// as well. Always remove it if it's the only remaining parameter.
	if paramCount >= 3 && IsPositionOnlySeparator(details.Parameters[paramCount-3]) {
		if paramCount == 3 || stripPositionOnlySeparator {
			paramsToDrop = 3
		}
	}

	// Remove the last parameters, which are the *args and **kwargs.
	details.Parameters = sliceCopy(details.Parameters[:len(details.Parameters)-paramsToDrop])

	if t.Priv.SpecializedTypes != nil {
		specializedCopy := *t.Priv.SpecializedTypes
		newFunction.Priv.SpecializedTypes = &specializedCopy
		newFunction.Priv.SpecializedTypes.ParameterTypes =
			sliceCopy(newFunction.Priv.SpecializedTypes.ParameterTypes[:len(newFunction.Priv.SpecializedTypes.ParameterTypes)-paramsToDrop])
		if newFunction.Priv.SpecializedTypes.ParameterDefaultTypes != nil {
			newFunction.Priv.SpecializedTypes.ParameterDefaultTypes =
				sliceCopy(newFunction.Priv.SpecializedTypes.ParameterDefaultTypes[:len(newFunction.Priv.SpecializedTypes.ParameterDefaultTypes)-paramsToDrop])
		}
	}

	if t.Shared.InferredReturnType != nil {
		newFunction.Shared.InferredReturnType = t.Shared.InferredReturnType
	}

	return newFunction
}

// FunctionTypeGetParamSpecFromArgsKwargs returns P if the function ends with
// "*args: P.args, **kwargs: P.kwargs". Otherwise it returns nil.
func FunctionTypeGetParamSpecFromArgsKwargs(t *FunctionType) *TypeVarType {
	params := t.Shared.Parameters
	if len(params) < 2 {
		return nil
	}

	secondLastParam := params[len(params)-2]
	secondLastParamType := FunctionTypeGetParamType(t, len(params)-2)
	lastParam := params[len(params)-1]
	lastParamType := FunctionTypeGetParamType(t, len(params)-1)

	if secondLastParam.Category == parser.ParamCategoryArgsList &&
		IsParamSpec(secondLastParamType) &&
		secondLastParamType.(*TypeVarType).Priv.ParamSpecAccess == ParamSpecAccessArgs &&
		lastParam.Category == parser.ParamCategoryKwargsDict &&
		IsParamSpec(lastParamType) &&
		lastParamType.(*TypeVarType).Priv.ParamSpecAccess == ParamSpecAccessKwargs {
		return TypeVarTypeCloneForParamSpecAccess(secondLastParamType.(*TypeVarType), ParamSpecAccessNone)
	}

	return nil
}

// FunctionTypeAddParamSpecVariadics corresponds to
// FunctionType.addParamSpecVariadics.
func FunctionTypeAddParamSpecVariadics(t *FunctionType, paramSpec *TypeVarType) {
	argsName := "args"
	kwargsName := "kwargs"

	FunctionTypeAddParam(t, FunctionParamCreate(
		parser.ParamCategoryArgsList,
		TypeVarTypeCloneForParamSpecAccess(paramSpec, ParamSpecAccessArgs),
		FunctionParamFlagsTypeDeclared,
		&argsName,
		nil,
		nil,
	))

	FunctionTypeAddParam(t, FunctionParamCreate(
		parser.ParamCategoryKwargsDict,
		TypeVarTypeCloneForParamSpecAccess(paramSpec, ParamSpecAccessKwargs),
		FunctionParamFlagsTypeDeclared,
		&kwargsName,
		nil,
		nil,
	))
}

// FunctionTypeAddDefaultParams corresponds to FunctionType.addDefaultParams.
// The TypeScript defaults useUnknown to false.
func FunctionTypeAddDefaultParams(t *FunctionType, useUnknown bool) {
	for _, param := range FunctionTypeGetDefaultParams(useUnknown) {
		FunctionTypeAddParam(t, param)
	}
}

// FunctionTypeGetDefaultParams corresponds to FunctionType.getDefaultParams.
func FunctionTypeGetDefaultParams(useUnknown bool) []FunctionParam {
	argsName := "args"
	kwargsName := "kwargs"

	var paramType Type
	var paramFlags FunctionParamFlags
	if useUnknown {
		paramType = UnknownTypeCreate(false)
		paramFlags = FunctionParamFlagsNone
	} else {
		paramType = AnyTypeCreate(false)
		paramFlags = FunctionParamFlagsTypeDeclared
	}

	return []FunctionParam{
		FunctionParamCreate(parser.ParamCategoryArgsList, paramType, paramFlags, &argsName, nil, nil),
		FunctionParamCreate(parser.ParamCategoryKwargsDict, paramType, paramFlags, &kwargsName, nil, nil),
	}
}

// FunctionTypeHasDefaultParams indicates whether the input signature consists
// of (*args: Any, **kwargs: Any).
func FunctionTypeHasDefaultParams(functionType *FunctionType) bool {
	sawArgs := false
	sawKwargs := false

	for i := range functionType.Shared.Parameters {
		param := functionType.Shared.Parameters[i]

		// Ignore nameless separator parameters.
		if param.Name == nil || *param.Name == "" {
			continue
		}

		if param.Category == parser.ParamCategorySimple {
			return false
		} else if param.Category == parser.ParamCategoryArgsList {
			sawArgs = true
		} else if param.Category == parser.ParamCategoryKwargsDict {
			sawKwargs = true
		}

		if !IsAnyOrUnknown(FunctionTypeGetParamType(functionType, i)) {
			return false
		}
	}

	return sawArgs && sawKwargs
}

func FunctionTypeIsInstanceMethod(t *FunctionType) bool {
	return (t.Shared.Flags &
		(FunctionTypeFlagsConstructorMethod |
			FunctionTypeFlagsStaticMethod |
			FunctionTypeFlagsClassMethod)) == 0
}

func FunctionTypeIsConstructorMethod(t *FunctionType) bool {
	return (t.Shared.Flags & FunctionTypeFlagsConstructorMethod) != 0
}

func FunctionTypeIsStaticMethod(t *FunctionType) bool {
	return (t.Shared.Flags & FunctionTypeFlagsStaticMethod) != 0
}

func FunctionTypeIsClassMethod(t *FunctionType) bool {
	return (t.Shared.Flags & FunctionTypeFlagsClassMethod) != 0
}

func FunctionTypeIsAbstractMethod(t *FunctionType) bool {
	return (t.Shared.Flags & FunctionTypeFlagsAbstractMethod) != 0
}

func FunctionTypeIsGenerator(t *FunctionType) bool {
	return (t.Shared.Flags & FunctionTypeFlagsGenerator) != 0
}

func FunctionTypeIsSynthesizedMethod(t *FunctionType) bool {
	return (t.Shared.Flags & FunctionTypeFlagsSynthesizedMethod) != 0
}

func FunctionTypeIsTypeCheckOnly(t *FunctionType) bool {
	return (t.Shared.Flags & FunctionTypeFlagsTypeCheckOnly) != 0
}

func FunctionTypeIsOverloaded(t *FunctionType) bool {
	return (t.Shared.Flags & FunctionTypeFlagsOverloaded) != 0
}

func FunctionTypeIsDefaultParamCheckDisabled(t *FunctionType) bool {
	return (t.Shared.Flags & FunctionTypeFlagsDisableDefaultChecks) != 0
}

func FunctionTypeIsAsync(t *FunctionType) bool {
	return (t.Shared.Flags & FunctionTypeFlagsAsync) != 0
}

func FunctionTypeIsStubDefinition(t *FunctionType) bool {
	return (t.Shared.Flags & FunctionTypeFlagsStubDefinition) != 0
}

func FunctionTypeIsPyTypedDefinition(t *FunctionType) bool {
	return (t.Shared.Flags & FunctionTypeFlagsPyTypedDefinition) != 0
}

func FunctionTypeIsFinal(t *FunctionType) bool {
	return (t.Shared.Flags & FunctionTypeFlagsFinal) != 0
}

func FunctionTypeHasUnannotatedParams(t *FunctionType) bool {
	return (t.Shared.Flags & FunctionTypeFlagsUnannotatedParams) != 0
}

func FunctionTypeIsGradualCallableForm(t *FunctionType) bool {
	return (t.Shared.Flags & FunctionTypeFlagsGradualCallableForm) != 0
}

func FunctionTypeIsParamSpecValue(t *FunctionType) bool {
	return (t.Shared.Flags & FunctionTypeFlagsParamSpecValue) != 0
}

func FunctionTypeIsPartiallyEvaluated(t *FunctionType) bool {
	return (t.Shared.Flags & FunctionTypeFlagsPartiallyEvaluated) != 0
}

func FunctionTypeIsOverridden(t *FunctionType) bool {
	return (t.Shared.Flags & FunctionTypeFlagsOverridden) != 0
}

// FunctionTypeIsBuiltIn corresponds to FunctionType.isBuiltIn. Calling it with
// no names is the `name === undefined` case, which the original answers with
// the flag check alone.
func FunctionTypeIsBuiltIn(t *FunctionType, names ...string) bool {
	if (t.Shared.Flags & FunctionTypeFlagsBuiltIn) == 0 {
		return false
	}

	if len(names) == 0 {
		return true
	}

	for _, name := range names {
		if name == t.Shared.Name || name == t.Shared.FullName {
			return true
		}
	}

	return false
}

// FunctionTypeGetDeclaredParamType reads the parameter's declared type
// directly, bypassing any specialization.
func FunctionTypeGetDeclaredParamType(t *FunctionType, index int) Type {
	return t.Shared.Parameters[index].TypeField
}

// FunctionTypeGetParamType corresponds to FunctionType.getParamType.
func FunctionTypeGetParamType(t *FunctionType, index int) Type {
	assert(index < len(t.Shared.Parameters), "Parameter types array overflow")

	if t.Priv.SpecializedTypes != nil && index < len(t.Priv.SpecializedTypes.ParameterTypes) {
		return t.Priv.SpecializedTypes.ParameterTypes[index]
	}

	return t.Shared.Parameters[index].TypeField
}

// FunctionTypeGetParamDefaultType corresponds to
// FunctionType.getParamDefaultType. It returns nil where the TypeScript
// returns undefined.
func FunctionTypeGetParamDefaultType(t *FunctionType, index int) Type {
	assert(index < len(t.Shared.Parameters), "Parameter types array overflow")

	if t.Priv.SpecializedTypes != nil &&
		t.Priv.SpecializedTypes.ParameterDefaultTypes != nil &&
		index < len(t.Priv.SpecializedTypes.ParameterDefaultTypes) {
		defaultArgType := t.Priv.SpecializedTypes.ParameterDefaultTypes[index]
		if defaultArgType != nil {
			return defaultArgType
		}
	}

	return t.Shared.Parameters[index].DefaultTypeField
}

// FunctionTypeAddParam corresponds to FunctionType.addParam.
func FunctionTypeAddParam(t *FunctionType, param FunctionParam) {
	t.Shared.Parameters = append(t.Shared.Parameters, param)

	if t.Priv.SpecializedTypes != nil {
		t.Priv.SpecializedTypes.ParameterTypes = append(t.Priv.SpecializedTypes.ParameterTypes, param.TypeField)
	}
}

// FunctionTypeAddPositionOnlyParamSeparator corresponds to
// FunctionType.addPositionOnlyParamSeparator.
func FunctionTypeAddPositionOnlyParamSeparator(t *FunctionType) {
	FunctionTypeAddParam(t, FunctionParamCreate(
		parser.ParamCategorySimple, AnyTypeCreate(false), FunctionParamFlagsNone, nil, nil, nil))
}

// FunctionTypeAddKeywordOnlyParamSeparator corresponds to
// FunctionType.addKeywordOnlyParamSeparator.
func FunctionTypeAddKeywordOnlyParamSeparator(t *FunctionType) {
	FunctionTypeAddParam(t, FunctionParamCreate(
		parser.ParamCategoryArgsList, AnyTypeCreate(false), FunctionParamFlagsNone, nil, nil, nil))
}

// FunctionTypeGetEffectiveReturnType corresponds to
// FunctionType.getEffectiveReturnType. The TypeScript defaults includeInferred
// to true.
func FunctionTypeGetEffectiveReturnType(t *FunctionType, includeInferred bool) Type {
	if t.Priv.SpecializedTypes != nil && t.Priv.SpecializedTypes.ReturnType != nil {
		return t.Priv.SpecializedTypes.ReturnType
	}

	if t.Shared.DeclaredReturnType != nil {
		return t.Shared.DeclaredReturnType
	}

	if includeInferred {
		if t.Shared.InferredReturnType != nil {
			return t.Shared.InferredReturnType.Type
		}
		return nil
	}

	return nil
}
