/*
 * typeevaluator_variance.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/typeEvaluator.ts (pyright 1.1.412):
 * inferVarianceForClass and assignClassToSelf.
 *
 * PEP 695's `class Box[T]` declares no variance. This infers it, and does so
 * empirically rather than by analysis: it builds two specializations of the class
 * that differ in exactly one type argument and asks whether one is assignable to
 * the other.
 *
 * For parameter i, the source gets `object` at position i and a dummy class
 * everywhere else; the destination gets the TypeVar itself at position i and the
 * same dummy elsewhere. The dummy is a class with no members, so every position
 * except i compares trivially equal and the answer isolates i's contribution.
 * Then:
 *
 *   - dest assignable to src -> covariant
 *   - src assignable to dest -> contravariant
 *   - neither -> invariant
 *
 * assignClassToSelf is the comparison. It is not assignType: it walks the class's
 * own symbol table member by member, and it pushes the class onto
 * assignClassToSelfStack so that any recursive reference back to the class under
 * examination is treated as invariant rather than re-entering this inference.
 *
 * Two exemptions in that walk are worth naming. __new__ and __init__ are skipped
 * because a constructor's parameter types say nothing about variance -- every
 * class would come out contravariant otherwise. And a mutable variable forces
 * invariance, since it can be both read and written, unless its name is private
 * or protected, where the original reasons that outside code cannot write it.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/common/uri"
)

// InferVarianceForClass corresponds to inferVarianceForClass.
func (e *typeEvaluator) InferVarianceForClass(classType *ClassType) {
	if !classType.Shared.RequiresVarianceInference {
		return
	}

	// The original's comment: presumptively mark the variance inference as
	// complete. This prevents potential recursion.
	classType.Shared.RequiresVarianceInference = false

	// The original's comment: presumptively mark the computed variance to
	// "unknown". We'll replace this below once the variance has been inferred.
	for _, param := range classType.Shared.TypeParams {
		if param.Shared.DeclaredVariance == VarianceAuto {
			unknown := VarianceUnknown
			param.Priv.ComputedVariance = &unknown
		}
	}

	// A class with no members, so every position filled with it compares equal
	// and contributes nothing to the answer.
	dummyTypeObject := ClassTypeCreateInstantiable(
		"__varianceDummy", "", "", uri.Empty(), 0, 0, nil, nil, nil)

	for paramIndex, param := range classType.Shared.TypeParams {
		// The original's comment: skip TypeVarTuples and ParamSpecs.
		if IsTypeVarTuple(param) || IsParamSpec(param) {
			continue
		}

		// The original's comment: skip type variables without auto-variance.
		if param.Shared.DeclaredVariance != VarianceAuto {
			continue
		}

		// The original's comment: replace all type arguments with a dummy type
		// except for the TypeVar of interest, which is replaced with an object
		// instance.
		srcTypeArgs := make([]Type, len(classType.Shared.TypeParams))
		// ...and with itself, for the destination.
		destTypeArgs := make([]Type, len(classType.Shared.TypeParams))

		for i, p := range classType.Shared.TypeParams {
			if IsTypeVarTuple(p) {
				srcTypeArgs[i] = p
				destTypeArgs[i] = p
				continue
			}

			if i == paramIndex {
				srcTypeArgs[i] = e.GetObjectType()
				destTypeArgs[i] = p
			} else {
				srcTypeArgs[i] = dummyTypeObject
				destTypeArgs[i] = dummyTypeObject
			}
		}

		srcType := ClassTypeSpecialize(classType, srcTypeArgs, nil, false, nil, nil)
		destType := ClassTypeSpecialize(classType, destTypeArgs, nil, false, nil, nil)

		inferredVariance := VarianceInvariant
		if e.assignClassToSelf(srcType, destType, VarianceCovariant, false, 0) {
			inferredVariance = VarianceCovariant
		} else if e.assignClassToSelf(destType, srcType, VarianceContravariant, false, 0) {
			inferredVariance = VarianceContravariant
		}

		// The original's comment: we assume here that we don't need to clone the
		// type var object because it was already cloned when it was associated
		// with this class scope.
		classType.Shared.TypeParams[paramIndex].Priv.ComputedVariance = &inferredVariance
	}
}

// assignClassToSelf corresponds to the function of the same name: is one
// specialization of a class assignable to another specialization of the SAME
// class. ignoreBaseClassVariance defaults to true in the original.
func (e *typeEvaluator) assignClassToSelf(
	destType *ClassType,
	srcType *ClassType,
	assumedVariance Variance,
	ignoreBaseClassVariance bool,
	recursionCount int,
) bool {
	boundSrcType := MakeTypeVarsBound(srcType, GetTypeVarScopeIds(srcType), true).(*ClassType)
	boundDestType := MakeTypeVarsBound(destType, GetTypeVarScopeIds(destType), true).(*ClassType)

	// The original's comment: stash the current class type so any references to
	// it are treated as though all type parameters are invariant.
	//
	// The original uses try/finally to guarantee the pop; defer is the Go
	// equivalent and survives a panic the same way.
	e.assignClassToSelfStack = append(e.assignClassToSelfStack,
		&AssignClassToSelfInfo{ClassType: boundDestType, AssumedVariance: assumedVariance})
	defer func() {
		e.assignClassToSelfStack = e.assignClassToSelfStack[:len(e.assignClassToSelfStack)-1]
	}()

	if !e.assignSelfMembers(boundDestType, boundSrcType, recursionCount) {
		return false
	}

	return e.assignSelfBaseClasses(
		boundDestType, boundSrcType, assumedVariance, ignoreBaseClassVariance, recursionCount)
}

// assignSelfMembers is the symbol-table walk.
func (e *typeEvaluator) assignSelfMembers(
	destType *ClassType, srcType *ClassType, recursionCount int,
) bool {
	isAssignable := true

	ClassTypeGetSymbolTable(destType).ForEach(func(symbol *Symbol, name string) {
		if !isAssignable || symbol.IsIgnoredForProtocolMatch() {
			return
		}

		// The original's comment: constructor methods are exempt from variance
		// calculations.
		if name == "__new__" || name == "__init__" {
			return
		}

		memberInfo := LookUpClassMember(srcType, name, MemberAccessFlagsDefault, nil)
		if memberInfo == nil {
			// The original asserts here. Both classes are specializations of the
			// same generic class, so the member must exist.
			return
		}

		destMemberType := e.GetEffectiveTypeOfSymbol(symbol)
		srcMemberType := e.GetTypeOfMember(memberInfo)
		destMemberType = PartiallySpecializeType(destMemberType, destType, e.GetTypeClassType(), nil)

		// The original's comment: properties require special processing.
		if IsClassInstance(destMemberType) && ClassTypeIsPropertyClass(destMemberType.(*ClassType)) &&
			IsClassInstance(srcMemberType) && ClassTypeIsPropertyClass(srcMemberType.(*ClassType)) {
			if !e.assignProperty(
				ClassTypeCloneAsInstantiable(destMemberType.(*ClassType), false),
				ClassTypeCloneAsInstantiable(srcMemberType.(*ClassType), false),
				destType, srcType, recursionCount,
			) {
				isAssignable = false
			}
			return
		}

		flags := AssignTypeFlagsDefault

		declarations := symbol.GetDeclarations()
		if len(declarations) > 0 {
			if varDecl, ok := declarations[0].(*VariableDeclaration); ok &&
				!e.IsFinalVariableDeclaration(varDecl) && !IsMemberReadOnly(destType, name) {
				// The original's comment: class and instance variables that are
				// mutable need to enforce invariance. We will exempt variables
				// that are private or protected, since these are presumably not
				// modifiable outside of the class.
				if !IsPrivateOrProtectedName(name) {
					flags |= AssignTypeFlagsInvariant
				}
			}
		}

		if !e.AssignType(destMemberType, srcMemberType, nil, nil,
			flags|AssignTypeFlagsSkipSelfClsParamCheck, recursionCount) {
			isAssignable = false
		}
	})

	return isAssignable
}

// assignSelfBaseClasses is the original's "now handle generic base classes"
// pass. Each generic base is specialized for both sides and compared recursively.
//
// The ignoreBaseClassVariance block is the check that a base class's declared
// variance is not contradicted: if the source passes a bare TypeVar into a
// position the base declares invariant or contravariant, the relation fails
// before any recursive comparison happens.
func (e *typeEvaluator) assignSelfBaseClasses(
	destType *ClassType,
	srcType *ClassType,
	assumedVariance Variance,
	ignoreBaseClassVariance bool,
	recursionCount int,
) bool {
	isAssignable := true

	for _, baseClass := range destType.Shared.BaseClasses {
		if !isAssignable {
			break
		}

		if !IsInstantiableClass(baseClass) {
			continue
		}
		baseClassType := baseClass.(*ClassType)
		if ClassTypeIsBuiltInNamed(baseClassType, "object", "Protocol", "Generic") ||
			len(baseClassType.Shared.TypeParams) == 0 {
			continue
		}

		specializedDestBaseClass := SpecializeForBaseClass(destType, baseClassType)
		specializedSrcBaseClass := SpecializeForBaseClass(srcType, baseClassType)

		if !ignoreBaseClassVariance {
			if !baseClassVarianceHolds(specializedDestBaseClass, specializedSrcBaseClass) {
				isAssignable = false
				continue
			}
		}

		// The original's comment: handle tuples specially since their type
		// arguments are variadic.
		if ClassTypeIsTupleClass(specializedDestBaseClass) {
			continue
		}

		if !e.assignClassToSelf(specializedDestBaseClass, specializedSrcBaseClass,
			assumedVariance, ignoreBaseClassVariance, recursionCount) {
			isAssignable = false
		}
	}

	return isAssignable
}

// baseClassVarianceHolds is the per-parameter check inside that pass.
func baseClassVarianceHolds(specializedDest, specializedSrc *ClassType) bool {
	for index, param := range specializedDest.Shared.TypeParams {
		if IsParamSpec(param) || IsTypeVarTuple(param) || param.Shared.IsSynthesized {
			continue
		}

		if specializedSrc.Priv.TypeArgs == nil || index >= len(specializedSrc.Priv.TypeArgs) ||
			specializedDest.Priv.TypeArgs == nil || index >= len(specializedDest.Priv.TypeArgs) {
			continue
		}

		paramVariance := param.Shared.DeclaredVariance

		if IsTypeVar(specializedSrc.Priv.TypeArgs[index]) {
			if paramVariance == VarianceInvariant || paramVariance == VarianceContravariant {
				return false
			}
		}

		if IsTypeVar(specializedDest.Priv.TypeArgs[index]) {
			if paramVariance == VarianceInvariant || paramVariance == VarianceCovariant {
				return false
			}
		}
	}

	return true
}

// assignProperty corresponds to the properties.ts function of the same name,
// which compares a property's fget, fset and fdel individually rather than
// comparing the property objects.
//
// The original also takes diag, constraints and selfConstraints; every one of
// them is undefined at this call site, the only one that exists so far.
func (e *typeEvaluator) assignProperty(
	_ *ClassType, _ *ClassType, _ *ClassType, _ *ClassType, _ int,
) bool {
	e.unported("properties.assignProperty")
	return true
}
