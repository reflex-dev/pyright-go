/*
 * typeutils_partialspec.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 * Author: Eric Traut
 *
 * partiallySpecializeType and addSolutionForSelfType from
 * analyzer/typeUtils.ts (pyright 1.1.412), lines 1496-1568. See the header of
 * typeutils.go for the file split.
 */

package analyzer

// PartiallySpecializeType partially specializes a type within the context of a
// specified (presumably specialized) class. It optionally specializes the
// `Self` type variables, replacing them with selfClass.
//
// selfClass holds a ClassType or TypeVarType; nil stands in for the omitted
// optional argument.
func PartiallySpecializeType(
	t Type,
	contextClassType *ClassType,
	typeClassType *ClassType,
	selfClass Type,
) Type {
	// If the context class is not specialized (or doesn't need specialization),
	// then there's no need to do any more work.
	if ClassTypeIsUnspecialized(contextClassType) && selfClass == nil {
		return t
	}

	// Partially specialize the type using the specialized class type vars.
	solution := BuildSolutionFromSpecializedClass(contextClassType)

	if selfClass != nil {
		AddSolutionForSelfType(solution, contextClassType, selfClass)
	}

	result := ApplySolvedTypeVars(t, solution, &ApplyTypeVarOptions{TypeClassType: typeClassType})

	// If this is a property, we may need to partially specialize the access
	// methods associated with it.
	if cls, ok := AsClass(result); ok {
		if cls.Priv.FgetInfo() != nil || cls.Priv.FsetInfo() != nil || cls.Priv.FdelInfo() != nil {
			updatePropertyMethodInfo := func(methodInfo *PropertyMethodInfo) *PropertyMethodInfo {
				if methodInfo == nil {
					return nil
				}

				return &PropertyMethodInfo{
					MethodType: PartiallySpecializeType(
						methodInfo.MethodType,
						contextClassType,
						typeClassType,
						selfClass,
					),
					ClassType: methodInfo.ClassType,
				}
			}

			cls = CloneType(cls)
			cls.Priv.ensureRare().FgetInfo = updatePropertyMethodInfo(cls.Priv.FgetInfo())
			cls.Priv.ensureRare().FsetInfo = updatePropertyMethodInfo(cls.Priv.FsetInfo())
			cls.Priv.ensureRare().FdelInfo = updatePropertyMethodInfo(cls.Priv.FdelInfo())
			result = cls
		}
	}

	return result
}

// AddSolutionForSelfType corresponds to addSolutionForSelfType. selfClass holds
// a ClassType or TypeVarType.
func AddSolutionForSelfType(solution *ConstraintSolution, contextClassType *ClassType, selfClass Type) {
	synthesizedSelfTypeVar := SynthesizeTypeVarForSelfCls(contextClassType, false)
	selfInstance := ConvertToInstance(selfClass, true)

	// We can't call stripLiteralValue here because that method requires the
	// type evaluator. Instead, we'll do a simplified version of it here.
	selfWithoutLiteral := MapSubtypes(selfInstance, func(subtype Type) Type {
		if cls, ok := AsClass(subtype); ok {
			if cls.Priv.LiteralValue != nil {
				return ClassTypeCloneWithLiteral(cls, nil)
			}
		}

		return subtype
	}, nil)

	solution.SetType(synthesizedSelfTypeVar, selfWithoutLiteral)
}
