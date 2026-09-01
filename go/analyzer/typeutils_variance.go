/*
 * typeutils_variance.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 * Author: Eric Traut
 *
 * The metaclass, generator and variance helpers from analyzer/typeUtils.ts
 * (pyright 1.1.412). See the header of typeutils.go for the file split.
 */

package analyzer

// DerivesFromStdlibClass corresponds to derivesFromStdlibClass.
func DerivesFromStdlibClass(classType *ClassType, className string) bool {
	for _, mroClass := range classType.Shared.Mro {
		if cls, ok := AsClass(mroClass); ok && ClassTypeIsBuiltInNamed(cls, className) {
			return true
		}
	}
	return false
}

// DerivesFromClassRecursive corresponds to derivesFromClassRecursive.
//
// If ignoreUnknown is true, an unknown base class is ignored when checking for
// derivation. If ignoreUnknown is false, a return value of true is assumed.
func DerivesFromClassRecursive(classType, baseClassToFind *ClassType, ignoreUnknown bool) bool {
	if ClassTypeIsSameGenericClass(classType, baseClassToFind, 0) {
		return true
	}

	for _, baseClass := range classType.Shared.BaseClasses {
		if cls, ok := AsInstantiableClass(baseClass); ok {
			if DerivesFromClassRecursive(cls, baseClassToFind, ignoreUnknown) {
				return true
			}
		} else if !ignoreUnknown && IsAnyOrUnknown(baseClass) {
			// If the base class is unknown, we have to make a conservative
			// assumption.
			return true
		}
	}

	return false
}

// SynthesizeTypeVarForSelfCls corresponds to synthesizeTypeVarForSelfCls.
func SynthesizeTypeVarForSelfCls(classType *ClassType, isClsParam bool) *TypeVarType {
	selfType := TypeVarTypeCreateInstance("__type_of_self__", TypeVarKindTypeVar)
	scopeID := GetTypeVarScopeID(classType)
	selfType.Shared.IsSynthesized = true
	selfType.Shared.IsSynthesizedSelf = true
	selfType.Priv.ScopeID = scopeID
	emptyScopeName := ""
	selfType.Priv.ScopeName = &emptyScopeName
	selfType.Priv.NameWithScope = TypeVarTypeMakeNameWithScope(selfType.Shared.Name, scopeID, emptyScopeName)

	isTypeArgExplicit := false
	boundType := ClassTypeSpecialize(
		classType,
		nil, // typeArgs
		&isTypeArgExplicit,
		classType.Priv.IncludeSubclasses,
		nil,
		nil,
	)

	selfType.Shared.BoundType = ClassTypeCloneAsInstance(boundType, true)

	if isClsParam {
		return TypeVarTypeCloneAsInstantiable(selfType)
	}
	return selfType
}

// GetGeneratorTypeArgs corresponds to getGeneratorTypeArgs. It returns nil
// where the TypeScript returns undefined.
func GetGeneratorTypeArgs(returnType Type) []Type {
	if cls, ok := AsClassInstance(returnType); ok {
		if ClassTypeIsBuiltInNamed(cls, "Generator", "AsyncGenerator") {
			return cls.Priv.TypeArgs
		} else if ClassTypeIsBuiltInNamed(cls, "AwaitableGenerator") {
			// AwaitableGenerator has four type arguments, and the first 3
			// correspond to the generator.
			if cls.Priv.TypeArgs == nil {
				return nil
			}
			end := 3
			if len(cls.Priv.TypeArgs) < end {
				end = len(cls.Priv.TypeArgs)
			}
			return sliceCopy(cls.Priv.TypeArgs[:end])
		}
	}

	return nil
}

// GetDeclaredGeneratorReturnType returns the declared "return" type (the type
// returned from a return statement) if it was declared, or nil otherwise.
func GetDeclaredGeneratorReturnType(functionType *FunctionType) Type {
	returnType := FunctionTypeGetEffectiveReturnType(functionType, true)
	if returnType != nil {
		generatorTypeArgs := GetGeneratorTypeArgs(returnType)

		if generatorTypeArgs != nil {
			// The send type is the third type arg.
			if len(generatorTypeArgs) >= 3 {
				return generatorTypeArgs[2]
			}
			return UnknownTypeCreate(false)
		}
	}

	return nil
}

// GetGeneratorYieldType returns the yield type if the declared return type is a
// Generator, Iterable, Iterator or the async counterparts. If the type is
// invalid for a generator, it returns nil.
func GetGeneratorYieldType(declaredReturnType Type, isAsync bool) Type {
	isLegalGeneratorType := true

	// Each pair is {async name, sync name}; the empty async name for
	// AwaitableGenerator never matches a built-in, which is what the original
	// intends.
	expectedClasses := [][2]string{
		{"AsyncIterable", "Iterable"},
		{"AsyncIterator", "Iterator"},
		{"AsyncGenerator", "Generator"},
		{"", "AwaitableGenerator"},
	}

	yieldType := MapSubtypes(declaredReturnType, func(subtype Type) Type {
		if IsAnyOrUnknown(subtype) {
			return subtype
		}

		if cls, ok := AsClassInstance(subtype); ok {
			matched := false
			for _, classes := range expectedClasses {
				name := classes[1]
				if isAsync {
					name = classes[0]
				}
				if ClassTypeIsBuiltInNamed(cls, name) {
					matched = true
					break
				}
			}

			if matched {
				if cls.Priv.TypeArgs != nil && len(cls.Priv.TypeArgs) >= 1 {
					return cls.Priv.TypeArgs[0]
				}
				return UnknownTypeCreate(false)
			}
		}

		isLegalGeneratorType = false
		return nil
	}, nil)

	if isLegalGeneratorType {
		return yieldType
	}
	return nil
}

// IsInstantiableMetaclass corresponds to isInstantiableMetaclass.
func IsInstantiableMetaclass(t Type) bool {
	cls, ok := AsInstantiableClass(t)
	if !ok {
		return false
	}
	for _, mroClass := range cls.Shared.Mro {
		if mroCls, ok := AsClass(mroClass); ok && ClassTypeIsBuiltInNamed(mroCls, "type") {
			return true
		}
	}
	return false
}

// IsMetaclassInstance corresponds to isMetaclassInstance.
func IsMetaclassInstance(t Type) bool {
	cls, ok := AsClassInstance(t)
	if !ok {
		return false
	}
	for _, mroClass := range cls.Shared.Mro {
		if mroCls, ok := AsClass(mroClass); ok && ClassTypeIsBuiltInNamed(mroCls, "type") {
			return true
		}
	}
	return false
}

// IsEffectivelyInstantiable corresponds to isEffectivelyInstantiable. A nil
// options stands in for the omitted argument; the TypeScript defaults
// recursionCount to 0.
func IsEffectivelyInstantiable(t Type, options *IsInstantiableOptions, recursionCount int) bool {
	if recursionCount > MaxTypeRecursionCount {
		return false
	}

	recursionCount++

	if t.Base().IsInstantiable() {
		return true
	}

	if options != nil && options.HonorTypeVarBounds {
		if tv, ok := AsTypeVar(t); ok && tv.Shared.BoundType != nil {
			if IsEffectivelyInstantiable(tv.Shared.BoundType, options, recursionCount) {
				return true
			}
		}
	}

	// Handle the special case of 'type' (or subclasses thereof), which are
	// instantiable.
	if IsMetaclassInstance(t) {
		return true
	}

	if union, ok := AsUnion(t); ok {
		for _, subtype := range union.Priv.Subtypes {
			if !IsEffectivelyInstantiable(subtype, options, recursionCount) {
				return false
			}
		}
		return true
	}

	return false
}

// InvertVariance corresponds to invertVariance.
func InvertVariance(variance Variance) Variance {
	if variance == VarianceContravariant {
		return VarianceCovariant
	}

	if variance == VarianceCovariant {
		return VarianceContravariant
	}

	return variance
}

// CombineVariances combines two variances to produce a resulting variance.
func CombineVariances(variance1, variance2 Variance) Variance {
	if variance1 == VarianceUnknown {
		return variance2
	}

	if variance2 == VarianceInvariant ||
		(variance2 == VarianceCovariant && variance1 == VarianceContravariant) ||
		(variance2 == VarianceContravariant && variance1 == VarianceCovariant) {
		return VarianceInvariant
	}

	return variance1
}
