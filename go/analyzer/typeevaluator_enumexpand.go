/*
 * typeevaluator_enumexpand.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/typeEvaluator.ts (pyright 1.1.412):
 * expandEnumTypeForLiteralComparison, containsTopLevelLiteralEnum,
 * expandEnumTypeForLiteralClasses, collectLiteralEnumClasses; and from
 * analyzer/typeGuards.ts: enumerateLiteralsForType.
 *
 * Why `Color` is assignable to `Literal[Color.RED, Color.GREEN, Color.BLUE]`
 * when those are all the members Color has. The two types are not the same, and
 * no ordinary assignability rule relates them, so assignType rewrites the source
 * first: an enum class with no literal value becomes the union of its members,
 * but only when the type it is being compared against already mentions enum
 * literals.
 *
 * That condition is what keeps the rewrite from being pervasive. Expanding every
 * enum on every comparison would be expensive and would change unrelated
 * results, so the expansion is driven entirely by what the OTHER side contains:
 * collectLiteralEnumClasses gathers the enum classes the comparison type
 * mentions literals of, and only those classes are expanded.
 *
 * The concretizeTypeVars parameter and the final identity test go together.
 * Concretizing a TypeVar to probe for a matching enum would, if no expansion
 * actually happened, silently replace the TypeVar with its bound and change an
 * unrelated comparison. So the concrete form is used only as a probe, and if it
 * came back unchanged the ORIGINAL type is returned.
 *
 * Two enum shapes are excluded from expansion, both because the member set is
 * not statically knowable: a class whose members may be modified at runtime, and
 * anything deriving from enum.Flag, whose values combine.
 */

package analyzer

// expandEnumTypeForLiteralComparison corresponds to the function of the same name.
func (e *typeEvaluator) expandEnumTypeForLiteralComparison(
	typeToExpand Type, comparisonType Type, concretizeTypeVars bool,
) Type {
	// The original's comment: assignment checks apply this at each recursive
	// assignType call so existing variance and constraint handling remains
	// intact. Keep this equivalence relation aligned with the recursive
	// normalization used by assert_type below.
	if !containsTopLevelLiteralEnum(comparisonType) {
		return typeToExpand
	}

	literalEnumClasses := e.collectLiteralEnumClasses(comparisonType, nil, false, 0)
	if len(literalEnumClasses) == 0 {
		return typeToExpand
	}

	// The original's comment: concretize top-level TypeVars only to probe for a
	// matching enum subtype. If no enum expansion actually occurs (for example,
	// an unrelated source TypeVar whose bound is not one of the comparison's enum
	// classes), preserve the original type rather than replacing the TypeVar with
	// its bound. Doing so keeps unrelated invariant comparisons like `list[T]` vs
	// `list[ColorLiterals]` unaffected.
	concreteType := typeToExpand
	if concretizeTypeVars {
		concreteType = e.MakeTopLevelTypeVarsConcrete(typeToExpand, false)
	}

	expandedType := e.expandEnumTypeForLiteralClasses(concreteType, literalEnumClasses)
	if expandedType == concreteType {
		return typeToExpand
	}
	return expandedType
}

// containsTopLevelLiteralEnum corresponds to the function of the same name.
//
// A union answers from its cached IncludesEnumLiteral flag rather than by
// walking its subtypes, which is why union construction maintains that flag.
func containsTopLevelLiteralEnum(t Type) bool {
	if IsClassInstance(t) {
		cls := t.(*ClassType)
		if ClassTypeIsEnumClass(cls) {
			if _, ok := cls.Priv.LiteralValue.(*EnumLiteral); ok {
				return true
			}
		}
	}

	union, ok := t.(*UnionType)
	if !ok {
		return false
	}

	return union.Priv.IncludesEnumLiteral
}

// expandEnumTypeForLiteralClasses corresponds to the function of the same name:
// each bare instance of one of the named enum classes becomes the union of that
// class's members.
func (e *typeEvaluator) expandEnumTypeForLiteralClasses(
	typeToExpand Type, literalEnumClasses []*ClassType,
) Type {
	if len(literalEnumClasses) == 0 {
		return typeToExpand
	}

	return MapSubtypes(typeToExpand, func(subtype Type) Type {
		if !IsClassInstance(subtype) {
			return subtype
		}

		cls := subtype.(*ClassType)
		if !ClassTypeIsEnumClass(cls) ||
			ClassTypeIsEnumMemberSetMayBeIncomplete(cls) ||
			cls.Priv.LiteralValue != nil {
			return subtype
		}

		matched := false
		for _, enumClass := range literalEnumClasses {
			if ClassTypeIsSameGenericClass(enumClass, cls, 0) {
				matched = true
				break
			}
		}
		if !matched {
			return subtype
		}

		literalTypes := EnumerateLiteralsForType(e, cls)
		if len(literalTypes) == 0 {
			return subtype
		}

		asTypes := make([]Type, len(literalTypes))
		for i, literalType := range literalTypes {
			asTypes[i] = literalType
		}
		return CombineTypes(asTypes, nil)
	}, nil)
}

// collectLiteralEnumClasses corresponds to the function of the same name. It
// accumulates into the slice it is given, as the original accumulates into the
// array it is given, and returns it.
//
// `recursively` reaches into function signatures, overloads and type arguments.
// It is false at the assignType call site and true where an assert_type
// comparison needs the whole shape normalized.
func (e *typeEvaluator) collectLiteralEnumClasses(
	t Type, literalEnumClasses []*ClassType, recursively bool, recursionCount int,
) []*ClassType {
	if recursionCount > MaxTypeRecursionCount {
		return literalEnumClasses
	}
	recursionCount++

	if recursively {
		switch fn := t.(type) {
		case *FunctionType:
			for index := range fn.Shared.Parameters {
				literalEnumClasses = e.collectLiteralEnumClasses(
					FunctionTypeGetParamType(fn, index), literalEnumClasses, recursively, recursionCount)
			}

			if returnType := FunctionTypeGetEffectiveReturnType(fn, true); returnType != nil {
				literalEnumClasses = e.collectLiteralEnumClasses(
					returnType, literalEnumClasses, recursively, recursionCount)
			}

		case *OverloadedType:
			for _, overload := range OverloadedTypeGetOverloads(fn) {
				literalEnumClasses = e.collectLiteralEnumClasses(
					overload, literalEnumClasses, recursively, recursionCount)
			}

			if implementation := OverloadedTypeGetImplementation(fn); implementation != nil {
				literalEnumClasses = e.collectLiteralEnumClasses(
					implementation, literalEnumClasses, recursively, recursionCount)
			}
		}
	}

	DoForEachSubtype(t, func(subtype Type, _ int, _ []Type) {
		cls, ok := subtype.(*ClassType)
		if !ok {
			return
		}

		if IsClassInstance(subtype) && ClassTypeIsEnumClass(cls) {
			if _, isEnumLiteral := cls.Priv.LiteralValue.(*EnumLiteral); isEnumLiteral {
				alreadyPresent := false
				for _, enumClass := range literalEnumClasses {
					if ClassTypeIsSameGenericClass(enumClass, cls, 0) {
						alreadyPresent = true
						break
					}
				}
				if !alreadyPresent {
					literalEnumClasses = append(literalEnumClasses, cls)
				}
			}
		}

		if recursively && cls.Priv.TypeArgs != nil {
			if cls.Priv.TupleTypeArgs != nil {
				for _, typeArg := range cls.Priv.TupleTypeArgs {
					literalEnumClasses = e.collectLiteralEnumClasses(
						typeArg.Type, literalEnumClasses, recursively, recursionCount)
				}
			} else {
				for _, typeArg := range cls.Priv.TypeArgs {
					literalEnumClasses = e.collectLiteralEnumClasses(
						typeArg, literalEnumClasses, recursively, recursionCount)
				}
			}
		}
	})

	return literalEnumClasses
}

// EnumerateLiteralsForType corresponds to the typeGuards.ts function of the same
// name: every literal value a type can hold, or nil where there is no finite set.
//
// The original distinguishes `undefined` (not enumerable) from `[]` (enumerable
// but empty, which an enum class with no members produces). Both callers here
// treat the two the same, so this returns nil for both.
func EnumerateLiteralsForType(evaluator TypeEvaluator, t *ClassType) []*ClassType {
	if ClassTypeIsBuiltInNamed(t, "bool") {
		// The original's comment: booleans have only two types: True and False.
		return []*ClassType{
			ClassTypeCloneWithLiteral(t, LiteralBool(true)),
			ClassTypeCloneWithLiteral(t, LiteralBool(false)),
		}
	}

	if !ClassTypeIsEnumClass(t) {
		return nil
	}

	// The original's comment: enum expansion doesn't apply if the member set
	// cannot be statically enumerated or if the class derives from enum.Flag.
	if ClassTypeIsEnumMemberSetMayBeDynamicallyModified(t) {
		return nil
	}
	for _, mroClass := range t.Shared.Mro {
		if cls, ok := mroClass.(*ClassType); ok && ClassTypeIsBuiltInNamed(cls, "Flag") {
			return nil
		}
	}

	// The original's comment: enumerate all of the values in this enumeration.
	var enumList []*ClassType

	ClassTypeGetSymbolTable(t).ForEach(func(symbol *Symbol, name string) {
		if symbol.IsIgnoredForProtocolMatch() {
			return
		}

		symbolType := evaluator.GetEffectiveTypeOfSymbol(symbol)
		if transformed := TransformTypeForEnumMember(evaluator, t, name); transformed != nil {
			symbolType = transformed
		}

		if !IsClassInstance(symbolType) {
			return
		}
		memberClass := symbolType.(*ClassType)
		if ClassTypeIsSameGenericClass(t, memberClass, 0) && memberClass.Priv.LiteralValue != nil {
			enumList = append(enumList, memberClass)
		}
	})

	return enumList
}
