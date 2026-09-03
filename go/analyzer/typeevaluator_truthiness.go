/*
 * typeevaluator_truthiness.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/typeEvaluator.ts (pyright 1.1.412):
 * canBeFalsy, canBeTruthy, removeTruthinessFromType, removeFalsinessFromType,
 * stripTypeGuard, solveAndApplyConstraints, isSpecialFormClass, isFinalVariable,
 * getCachedType, getTypingType, getTypeCheckerInternalsType, getTypesType,
 * getTypeshedType, getDeclaredReturnType, getGetterTypeFromProperty.
 *
 * What `if x:` does to the type of x, and a handful of one-liners that had
 * nowhere better to live.
 *
 * The two predicates are asymmetric because Python's truthiness rules are. A
 * value is falsy if its class says so -- through `__bool__` returning False, or
 * `__len__` returning zero -- and both of those are opt-in. So canBeTruthy is
 * mostly "yes" with a short list of exceptions (None, the empty tuple, falsy
 * literals, a `__bool__` that unconditionally returns False), while canBeFalsy
 * has to go looking for evidence: a `__len__`, a `__bool__`, an empty tuple, a
 * TypedDict with no required keys.
 *
 * Two cases deserve naming. A PROTOCOL class answers "yes" to both, because a
 * conforming class could supply whatever method the protocol did not mention. And
 * a non-final class could in principle override `__bool__` to become falsy, which
 * the original notes and then pragmatically ignores for everything but `object`
 * -- the comment explaining that trade-off is preserved at both sites.
 *
 * The two removal functions are the narrowing counterparts, and they do more than
 * filter. A `bool` that survives `if x:` is not `bool` but `Literal[True]`, and
 * an `int` that survives `if not x:` is `Literal[0]`. Narrowing `str` and `bytes`
 * that way is unsound if someone overrides `__bool__` on a subclass; the original
 * calls that "extremely unlikely (and ill advised)" and does it anyway.
 */

package analyzer

import (
	"math/big"

	"github.com/microsoft/pyright/go/parser"
)

// CanBeFalsy corresponds to canBeFalsy.
func (e *typeEvaluator) CanBeFalsy(t Type) bool {
	return e.canBeFalsy(t, 0)
}

func (e *typeEvaluator) canBeFalsy(t Type, recursionCount int) bool {
	t = e.MakeTopLevelTypeVarsConcrete(t, false)

	if recursionCount > MaxTypeRecursionCount {
		return true
	}
	recursionCount++

	switch t.Base().Category {
	case TypeCategoryUnbound, TypeCategoryUnknown, TypeCategoryAny, TypeCategoryNever:
		return true

	case TypeCategoryUnion:
		return FindSubtype(t, func(subtype Type) bool {
			return e.canBeFalsy(subtype, recursionCount)
		}) != nil

	case TypeCategoryFunction, TypeCategoryOverloaded, TypeCategoryModule, TypeCategoryTypeVar:
		return false

	case TypeCategoryClass:
		return e.classCanBeFalsy(t.(*ClassType), recursionCount)
	}

	return false
}

func (e *typeEvaluator) classCanBeFalsy(classType *ClassType, recursionCount int) bool {
	if classType.Base().IsInstantiable() {
		return false
	}

	// The original's comment: sentinels are always truthy.
	if IsSentinelLiteral(classType) {
		return false
	}

	// The original's comment: handle tuples specially.
	if IsTupleClass(classType) && classType.Priv.TupleTypeArgs != nil {
		return IsUnboundedTupleClass(classType) || len(classType.Priv.TupleTypeArgs) == 0
	}

	// The original's comment: handle subclasses of tuple, such as NamedTuple.
	//
	// The original's `find` predicate accepts a non-class MRO entry as well, so
	// the first Unknown in the MRO ends the search rather than being skipped.
	for _, mroClass := range classType.Shared.Mro {
		mroClassType, isClass := mroClass.(*ClassType)
		if !isClass || !IsClass(mroClass) {
			break
		}
		if !IsTupleClass(mroClassType) {
			continue
		}
		if mroClassType.Priv.TupleTypeArgs != nil {
			return IsUnboundedTupleClass(mroClassType) || len(mroClassType.Priv.TupleTypeArgs) == 0
		}
		break
	}

	// The original's comment: handle TypedDicts specially. If one or more entries
	// are required or known to exist, we can say for sure that the type is not
	// falsy.
	if ClassTypeIsTypedDictClass(classType) {
		if tdEntries := GetTypedDictMembersForClass(e, classType, true); tdEntries != nil {
			for _, tdEntry := range tdEntries.KnownItems.Values() {
				if tdEntry.IsRequired || tdEntry.IsProvided {
					return false
				}
			}
		}
	}

	// The original's comment: check for bool, int, str and bytes literals that are
	// never falsy.
	if classType.Priv.LiteralValue != nil {
		if ClassTypeIsBuiltInNamed(classType, "bool", "int", "str", "bytes") {
			return !literalValueIsTruthy(classType.Priv.LiteralValue)
		}

		if enumLiteral, ok := classType.Priv.LiteralValue.(*EnumLiteral); ok {
			// The original's comment: does the Enum class forward the truthiness
			// check to the underlying member type?
			if enumLiteral.IsReprEnum {
				return e.canBeFalsy(enumLiteral.ItemType, recursionCount)
			}
		}
	}

	// The original's comment: if this is a protocol class, don't make any
	// assumptions about the absence of specific methods. These could be provided by
	// a class that conforms to the protocol.
	if ClassTypeIsProtocolClass(classType) {
		return true
	}

	if LookUpObjectMember(classType, "__len__", MemberAccessFlagsDefault, nil) != nil {
		return true
	}

	if boolMethod := LookUpObjectMember(classType, "__bool__", MemberAccessFlagsDefault, nil); boolMethod != nil {
		boolMethodType := e.GetTypeOfMember(boolMethod)

		// The original's comment: if the __bool__ function unconditionally returns
		// True, it can never be falsy.
		if fn, ok := boolMethodType.(*FunctionType); ok && fn.Shared.DeclaredReturnType != nil {
			if boolLiteralIs(fn.Shared.DeclaredReturnType, true) {
				return false
			}
		}

		return true
	}

	// The original's comment: if the class is not final, it's possible that it
	// could be overridden such that it is falsy. To be fully correct, we'd need to
	// do the following:
	//   return !ClassType.isFinal(type);
	// However, pragmatically if the class is not an `object`, it's typically OK to
	// assume that it will not be overridden in this manner.
	return ClassTypeIsBuiltInNamed(classType, "object")
}

// CanBeTruthy corresponds to canBeTruthy.
func (e *typeEvaluator) CanBeTruthy(t Type) bool {
	return e.canBeTruthy(t, 0)
}

func (e *typeEvaluator) canBeTruthy(t Type, recursionCount int) bool {
	t = e.MakeTopLevelTypeVarsConcrete(t, false)

	if recursionCount > MaxTypeRecursionCount {
		return true
	}
	recursionCount++

	switch t.Base().Category {
	case TypeCategoryUnknown, TypeCategoryFunction, TypeCategoryOverloaded,
		TypeCategoryModule, TypeCategoryTypeVar, TypeCategoryNever, TypeCategoryAny:
		return true

	case TypeCategoryUnion:
		return FindSubtype(t, func(subtype Type) bool {
			return e.canBeTruthy(subtype, recursionCount)
		}) != nil

	case TypeCategoryUnbound:
		return false

	case TypeCategoryClass:
		return e.classCanBeTruthy(t.(*ClassType), recursionCount)
	}

	return false
}

func (e *typeEvaluator) classCanBeTruthy(classType *ClassType, recursionCount int) bool {
	if classType.Base().IsInstantiable() {
		return true
	}

	if IsNoneInstance(classType) {
		return false
	}

	// The original's comment: check for tuple[()] (an empty tuple).
	if classType.Priv.TupleTypeArgs != nil && len(classType.Priv.TupleTypeArgs) == 0 {
		return false
	}

	// The original's comment: check for bool, int, str and bytes literals that are
	// never falsy.
	if classType.Priv.LiteralValue != nil {
		if ClassTypeIsBuiltInNamed(classType, "bool", "int", "str", "bytes") {
			return literalValueIsTruthy(classType.Priv.LiteralValue)
		}

		if enumLiteral, ok := classType.Priv.LiteralValue.(*EnumLiteral); ok {
			// The original's comment: does the Enum class forward the truthiness
			// check to the underlying member type?
			if enumLiteral.IsReprEnum {
				return e.canBeTruthy(enumLiteral.ItemType, recursionCount)
			}
		}
	}

	// The original's comment: if this is a protocol class, don't make any
	// assumptions about the absence of specific methods. These could be provided by
	// a class that conforms to the protocol.
	if ClassTypeIsProtocolClass(classType) {
		return true
	}

	if boolMethod := LookUpObjectMember(classType, "__bool__", MemberAccessFlagsDefault, nil); boolMethod != nil {
		boolMethodType := e.GetTypeOfMember(boolMethod)

		// The original's comment: if the __bool__ function unconditionally returns
		// False, it can never be truthy.
		if fn, ok := boolMethodType.(*FunctionType); ok && fn.Shared.DeclaredReturnType != nil {
			if boolLiteralIs(fn.Shared.DeclaredReturnType, false) {
				return false
			}
		}
	}

	return true
}

// RemoveTruthinessFromType corresponds to removeTruthinessFromType.
//
// Its comment: filters a type such that that no part of it is definitely truthy.
// For example, if a type is a union of None and a custom class "Foo" that has no
// __len__ or __nonzero__ method, this method would strip off the "Foo" and return
// only the "None".
func (e *typeEvaluator) RemoveTruthinessFromType(t Type) Type {
	return MapSubtypes(t, func(subtype Type) Type {
		concreteSubtype := e.MakeTopLevelTypeVarsConcrete(subtype, false)

		if IsClassInstance(concreteSubtype) {
			concreteClass := concreteSubtype.(*ClassType)

			if concreteClass.Priv.LiteralValue != nil {
				var isLiteralFalsy bool

				if _, isEnum := concreteClass.Priv.LiteralValue.(*EnumLiteral); isEnum {
					isLiteralFalsy = !e.CanBeTruthy(concreteSubtype)
				} else {
					isLiteralFalsy = !literalValueIsTruthy(concreteClass.Priv.LiteralValue)
				}

				// The original's comment: if the object is already definitely falsy,
				// it's fine to include, otherwise it should be removed.
				if isLiteralFalsy {
					return subtype
				}
				return nil
			}

			// The original's comment: if the object is a sentinel, we can eliminate it.
			if IsSentinelLiteral(concreteSubtype) {
				return nil
			}

			// The original's comment: if the object is a bool, make it "false", since
			// "true" is a truthy value.
			if ClassTypeIsBuiltInNamed(concreteClass, "bool") {
				return ClassTypeCloneWithLiteral(concreteClass, LiteralBool(false))
			}

			// The original's comment: if the object is an int, str or bytes, narrow to
			// a literal type. This is slightly unsafe in that someone could subclass
			// `int`, `str` or `bytes` and override the `__bool__` method to change its
			// behavior, but this is extremely unlikely (and ill advised).
			if ClassTypeIsBuiltInNamed(concreteClass, "int") {
				return ClassTypeCloneWithLiteral(concreteClass, LiteralInt{Value: big.NewInt(0)})
			} else if ClassTypeIsBuiltInNamed(concreteClass, "str", "bytes") {
				return ClassTypeCloneWithLiteral(concreteClass, LiteralString(""))
			}
		}

		// The original's comment: if it's possible for the type to be falsy, include
		// it.
		if e.CanBeFalsy(subtype) {
			return subtype
		}

		return nil
	}, nil)
}

// RemoveFalsinessFromType corresponds to removeFalsinessFromType.
//
// Its comment: filters a type such that that no part of it is definitely falsy.
// For example, if a type is a union of None and an "int", this method would strip
// off the "None" and return only the "int".
func (e *typeEvaluator) RemoveFalsinessFromType(t Type) Type {
	return MapSubtypes(t, func(subtype Type) Type {
		concreteSubtype := e.MakeTopLevelTypeVarsConcrete(subtype, false)

		if IsClassInstance(concreteSubtype) {
			concreteClass := concreteSubtype.(*ClassType)

			if concreteClass.Priv.LiteralValue != nil {
				var isLiteralTruthy bool

				switch concreteClass.Priv.LiteralValue.(type) {
				case *EnumLiteral:
					isLiteralTruthy = !e.CanBeFalsy(concreteSubtype)
				case *SentinelLiteral:
					isLiteralTruthy = true
				default:
					isLiteralTruthy = literalValueIsTruthy(concreteClass.Priv.LiteralValue)
				}

				// The original's comment: if the object is already definitely truthy,
				// it's fine to include, otherwise it should be removed.
				if isLiteralTruthy {
					return subtype
				}
				return nil
			}

			// The original's comment: if the object is a bool, make it "true", since
			// "false" is a falsy value.
			if ClassTypeIsBuiltInNamed(concreteClass, "bool") {
				return ClassTypeCloneWithLiteral(concreteClass, LiteralBool(true))
			}

			// The original's comment: if the object is a "None" instance, we can
			// eliminate it.
			if IsNoneInstance(concreteSubtype) {
				return nil
			}

			// The original's comment: if this is an instance of a class that cannot be
			// subclassed, we cannot say definitively that it's not falsy because a
			// subclass could override `__bool__`. For this reason, the code should not
			// remove any classes that are not final.
			//   if (!ClassType.isFinal(concreteSubtype)) { return subtype; }
			// However, we're going to pragmatically assume that any classes other than
			// `object` will not be overridden in this manner.
			if ClassTypeIsBuiltInNamed(concreteClass, "object") {
				return subtype
			}
		}

		// The original's comment: if it's possible for the type to be truthy,
		// include it.
		if e.CanBeTruthy(subtype) {
			return subtype
		}

		return nil
	}, nil)
}

// StripTypeGuard corresponds to stripTypeGuard.
//
// Its comment: if a type contains a TypeGuard or TypeIs, convert it to a bool.
func (e *typeEvaluator) StripTypeGuard(t Type) Type {
	return MapSubtypes(t, func(subtype Type) Type {
		if IsClassInstance(subtype) &&
			ClassTypeIsBuiltInNamed(subtype.(*ClassType), "TypeGuard", "TypeIs") {
			if e.prefetched != nil && e.prefetched.BoolClass != nil {
				return ConvertToInstance(e.prefetched.BoolClass, true)
			}
			return UnknownTypeCreate(false)
		}

		return subtype
	}, nil)
}

// literalValueIsTruthy is the original's implicit JavaScript truthiness test on
// a literal value, which Go has to spell out. The original writes
// `!!type.priv.literalValue && type.priv.literalValue !== BigInt(0)`; the
// explicit zero check is needed because a `BigInt(0)` object is truthy in
// JavaScript even though the number it holds is not.
func literalValueIsTruthy(value LiteralValue) bool {
	switch v := value.(type) {
	case LiteralBool:
		return bool(v)
	case LiteralInt:
		return v.Value != nil && v.Value.Sign() != 0
	case LiteralFloat:
		return float64(v) != 0
	case LiteralString:
		return string(v) != ""
	}
	// Enum and sentinel literals are objects, which JavaScript considers truthy.
	return true
}

// boolLiteralIs asks whether a type is exactly `Literal[True]` or
// `Literal[False]`.
func boolLiteralIs(t Type, want bool) bool {
	if !IsClassInstance(t) {
		return false
	}
	classType := t.(*ClassType)
	if !ClassTypeIsBuiltInNamed(classType, "bool") {
		return false
	}
	literal, ok := classType.Priv.LiteralValue.(LiteralBool)
	return ok && bool(literal) == want
}

/*
 * One-liners from typeEvaluator.ts that had nowhere better to live.
 */

// SolveAndApplyConstraints corresponds to solveAndApplyConstraints.
func (e *typeEvaluator) SolveAndApplyConstraints(
	t Type,
	constraints *ConstraintTracker,
	applyOptions *ApplyTypeVarOptions,
	solveOptions *SolveConstraintsOptions,
) Type {
	solution := SolveConstraints(e, constraints, solveOptions)
	return ApplySolvedTypeVars(t, solution, applyOptions)
}

// IsSpecialFormClass corresponds to isSpecialFormClass.
func (e *typeEvaluator) IsSpecialFormClass(classType *ClassType, flags AssignTypeFlags) bool {
	if (flags & AssignTypeFlagsAllowIsinstanceSpecialForms) != 0 {
		return false
	}

	return ClassTypeIsSpecialFormClass(classType)
}

// IsFinalVariable corresponds to isFinalVariable.
func (e *typeEvaluator) IsFinalVariable(symbol *Symbol) bool {
	for _, decl := range symbol.GetDeclarations() {
		if e.IsFinalVariableDeclaration(decl) {
			return true
		}
	}
	return false
}

// GetCachedType corresponds to getCachedType. It returns nil where the original
// returns undefined.
func (e *typeEvaluator) GetCachedType(node parser.ExpressionNode) Type {
	// The original's comment: prefer the ordinary runtime type when both caches
	// contain an entry for this node. Fall back to the contextual cache so
	// TypeForm-only evaluations remain discoverable.
	cacheEntry := e.readTypeCacheEntry(node)
	if cacheEntry == nil {
		cacheEntry = e.readContextualTypeCacheEntryForNode(node)
	}
	if cacheEntry == nil || cacheEntry.TypeResult.IsIncomplete {
		return nil
	}

	return cacheEntry.TypeResult.Type
}

// GetTypingType corresponds to getTypingType.
func (e *typeEvaluator) GetTypingType(node parser.ParseNode, symbolName string) Type {
	if t := e.getTypeOfModule(node, symbolName, []string{"typing"}); t != nil {
		return t
	}
	return e.getTypeOfModule(node, symbolName, []string{"typing_extensions"})
}

// GetTypeCheckerInternalsType corresponds to getTypeCheckerInternalsType.
func (e *typeEvaluator) GetTypeCheckerInternalsType(node parser.ParseNode, symbolName string) Type {
	return e.getTypeOfModule(node, symbolName, []string{"_typeshed", "_type_checker_internals"})
}

// GetDeclaredReturnType corresponds to getDeclaredReturnType.
func (e *typeEvaluator) GetDeclaredReturnType(node *parser.FunctionNode) Type {
	functionTypeInfo := e.GetTypeOfFunction(node)
	if functionTypeInfo == nil {
		return nil
	}

	returnType := functionTypeInfo.FunctionType.Shared.DeclaredReturnType
	if returnType == nil {
		return nil
	}

	if FunctionTypeIsGenerator(functionTypeInfo.FunctionType) {
		return GetDeclaredGeneratorReturnType(functionTypeInfo.FunctionType)
	}

	return returnType
}

// GetGetterTypeFromProperty corresponds to getGetterTypeFromProperty.
func (e *typeEvaluator) GetGetterTypeFromProperty(propertyClass *ClassType) Type {
	if !ClassTypeIsPropertyClass(propertyClass) {
		return nil
	}

	if propertyClass.Priv.FgetInfo != nil {
		if fn, ok := propertyClass.Priv.FgetInfo.MethodType.(*FunctionType); ok {
			return e.GetEffectiveReturnType(fn)
		}
	}

	return nil
}
