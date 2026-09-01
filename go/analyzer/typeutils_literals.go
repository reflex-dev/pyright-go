/*
 * typeutils_literals.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 * Author: Eric Traut
 *
 * The literal, type-form and tuple-shape predicates from analyzer/typeUtils.ts
 * (pyright 1.1.412). See the header of typeutils.go for the file split.
 */

package analyzer

// IsLiteralTypeOrUnion corresponds to isLiteralTypeOrUnion. The TypeScript
// defaults allowNone to false.
func IsLiteralTypeOrUnion(t Type, allowNone bool) bool {
	if cls, ok := AsClassInstance(t); ok {
		if allowNone && IsNoneInstance(t) {
			return true
		}

		return cls.Priv.LiteralValue != nil
	}

	if IsUnion(t) {
		return FindSubtype(t, func(subtype Type) bool {
			cls, ok := AsClassInstance(subtype)
			if !ok {
				return true
			}

			if IsNoneInstance(subtype) {
				return !allowNone
			}

			return cls.Priv.LiteralValue == nil
		}) == nil
	}

	return false
}

// IsLiteralLikeType corresponds to isLiteralLikeType.
func IsLiteralLikeType(t *ClassType) bool {
	if t.Priv.LiteralValue != nil {
		return true
	}

	if ClassTypeIsBuiltInNamed(t, "LiteralString") {
		return true
	}

	return false
}

// IsSentinelLiteral corresponds to isSentinelLiteral.
func IsSentinelLiteral(t Type) bool {
	cls, ok := AsClassInstance(t)
	if !ok {
		return false
	}
	_, isSentinel := cls.Priv.LiteralValue.(*SentinelLiteral)
	return isSentinel
}

// containsLiteralTypeWalker corresponds to the class of the same name declared
// inside containsLiteralType.
type containsLiteralTypeWalker struct {
	*TypeWalker
	*TypeWalkerDefaults

	foundLiteral    bool
	includeTypeArgs bool
}

func newContainsLiteralTypeWalker(includeTypeArgs bool) *containsLiteralTypeWalker {
	w := &containsLiteralTypeWalker{includeTypeArgs: includeTypeArgs}
	w.TypeWalker = NewTypeWalker(w)
	w.TypeWalkerDefaults = NewTypeWalkerDefaults(w.TypeWalker)
	return w
}

func (w *containsLiteralTypeWalker) VisitClass(classType *ClassType) {
	if IsClassInstance(classType) {
		if IsLiteralLikeType(classType) {
			w.foundLiteral = true
			w.CancelWalk()
		}
	}

	if w.includeTypeArgs {
		// The original's `super.visitClass(classType)`.
		w.TypeWalkerDefaults.VisitClass(classType)
	}
}

// ContainsLiteralType corresponds to containsLiteralType. The TypeScript
// defaults includeTypeArgs to false.
func ContainsLiteralType(t Type, includeTypeArgs bool) bool {
	walker := newContainsLiteralTypeWalker(includeTypeArgs)
	walker.Walk(t)
	return walker.foundLiteral
}

// GetLiteralTypeClassName returns the name of the built-in class if all of the
// subtypes are literals with the same built-in class (e.g. all 'int' or all
// 'str'). If some of the subtypes are not literals or the literal classes don't
// match, it returns nil.
//
// Note that the original only records the first class name it sees and never
// compares later ones against it, so a union of `Literal[1]` and `Literal["a"]`
// answers "int" rather than undefined. That is reproduced as written. See
// UPSTREAM-BUGS.md #3.
func GetLiteralTypeClassName(t Type) *string {
	if cls, ok := AsClassInstance(t); ok {
		if cls.Priv.LiteralValue != nil && ClassTypeIsBuiltIn(cls) {
			return &cls.Shared.Name
		}
		return nil
	}

	if IsUnion(t) {
		var className *string
		foundMismatch := false

		DoForEachSubtype(t, func(subtype Type, index int, allSubtypes []Type) {
			subtypeLiteralTypeName := GetLiteralTypeClassName(subtype)
			if subtypeLiteralTypeName == nil {
				foundMismatch = true
			} else if className == nil {
				className = subtypeLiteralTypeName
			}
		})

		if foundMismatch {
			return nil
		}
		return className
	}

	return nil
}

// StripTypeForm corresponds to stripTypeForm.
func StripTypeForm(t Type) Type {
	if t.Base().Props != nil && t.Base().Props.TypeForm != nil {
		return CloneWithTypeForm(t, nil)
	}

	return t
}

// StripTypeFormRecursive corresponds to stripTypeFormRecursive. The TypeScript
// defaults recursionCount to 0.
func StripTypeFormRecursive(t Type, recursionCount int) Type {
	if recursionCount > MaxTypeRecursionCount {
		return t
	}
	recursionCount++

	if t.Base().Props != nil && t.Base().Props.TypeForm != nil {
		t = CloneWithTypeForm(t, nil)
	}

	return MapSubtypes(t, func(subtype Type) Type {
		return StripTypeFormRecursive(subtype, recursionCount)
	}, nil)
}

// GetUnionSubtypeCount corresponds to getUnionSubtypeCount.
func GetUnionSubtypeCount(t Type) int {
	if union, ok := AsUnion(t); ok {
		return len(union.Priv.Subtypes)
	}

	return 1
}

// IsEllipsisType corresponds to isEllipsisType.
func IsEllipsisType(t Type) bool {
	any, ok := AsAny(t)
	return ok && any.Priv.IsEllipsis
}

// IsProperty corresponds to isProperty.
func IsProperty(t Type) bool {
	cls, ok := AsClassInstance(t)
	return ok && ClassTypeIsPropertyClass(cls)
}

// IsTupleGradualForm corresponds to isTupleGradualForm.
func IsTupleGradualForm(t Type) bool {
	cls, ok := AsClassInstance(t)
	return ok &&
		IsTupleClass(cls) &&
		cls.Priv.TupleTypeArgs != nil &&
		len(cls.Priv.TupleTypeArgs) == 1 &&
		IsAnyOrUnknown(cls.Priv.TupleTypeArgs[0].Type) &&
		cls.Priv.TupleTypeArgs[0].IsUnbounded
}

// IsStubOnlySubscriptable returns true for classes that are generic in stubs
// but not subscriptable at runtime (e.g. operator.attrgetter,
// operator.itemgetter). These lack __class_getitem__ and are not builtins.
func IsStubOnlySubscriptable(classType *ClassType) bool {
	return ClassTypeIsDefinedInStub(classType) &&
		!ClassTypeIsBuiltIn(classType) &&
		!classType.Shared.Fields.Has("__class_getitem__")
}

// IsUnboundedTupleClass indicates whether the type is a tuple class of the form
// tuple[x, ...] where the number of elements in the tuple is unknown.
func IsUnboundedTupleClass(t *ClassType) bool {
	for _, arg := range t.Priv.TupleTypeArgs {
		if arg.IsUnbounded || IsUnpackedTypeVarTuple(arg.Type) || IsUnpackedTypeVar(arg.Type) {
			return true
		}
	}
	return false
}

// IsTupleIndexUnambiguous indicates whether the specified index is within range
// and its type is unambiguous, in that it doesn't involve any element ranges
// that are of indeterminate length.
func IsTupleIndexUnambiguous(t *ClassType, index int) bool {
	if t.Priv.TupleTypeArgs == nil {
		return false
	}

	unboundedIndex := -1
	for i, arg := range t.Priv.TupleTypeArgs {
		if arg.IsUnbounded || IsUnpackedTypeVarTuple(arg.Type) || IsUnpackedTypeVar(arg.Type) {
			unboundedIndex = i
			break
		}
	}

	if index < 0 {
		lowerIndexLimit := unboundedIndex
		if unboundedIndex < 0 {
			lowerIndexLimit = 0
		}
		index += len(t.Priv.TupleTypeArgs)
		return index >= lowerIndexLimit
	}

	upperIndexLimit := unboundedIndex
	if unboundedIndex < 0 {
		upperIndexLimit = len(t.Priv.TupleTypeArgs)
	}
	return index < upperIndexLimit
}

// IsCallableType corresponds to isCallableType.
func IsCallableType(t Type) bool {
	if IsFunctionOrOverloaded(t) || IsAnyOrUnknown(t) {
		return true
	}

	if IsEffectivelyInstantiable(t, nil, 0) {
		return true
	}

	if cls, ok := AsClass(t); ok {
		if cls.IsInstantiable() {
			return true
		}

		callMember := LookUpObjectMember(cls, "__call__", MemberAccessFlagsSkipInstanceMembers, nil)
		return callMember != nil
	}

	if union, ok := AsUnion(t); ok {
		for _, subtype := range union.Priv.Subtypes {
			if !IsCallableType(subtype) {
				return false
			}
		}
		return true
	}

	return false
}

// IsDescriptorInstance corresponds to isDescriptorInstance. The TypeScript
// defaults requireSetter to false.
//
// Note that the union branch requires *every* subtype to be a descriptor while
// delegating to IsMaybeDescriptorInstance, which itself answers true if *any*
// subtype is. The asymmetry is in the original.
func IsDescriptorInstance(t Type, requireSetter bool) bool {
	if union, ok := AsUnion(t); ok {
		for _, subtype := range union.Priv.Subtypes {
			if !IsMaybeDescriptorInstance(subtype, requireSetter) {
				return false
			}
		}
		return true
	}

	return IsMaybeDescriptorInstance(t, requireSetter)
}

// IsMaybeDescriptorInstance corresponds to isMaybeDescriptorInstance. The
// TypeScript defaults requireSetter to false.
func IsMaybeDescriptorInstance(t Type, requireSetter bool) bool {
	if union, ok := AsUnion(t); ok {
		for _, subtype := range union.Priv.Subtypes {
			if IsMaybeDescriptorInstance(subtype, requireSetter) {
				return true
			}
		}
		return false
	}

	cls, ok := AsClassInstance(t)
	if !ok {
		return false
	}

	// Traverse the MRO so descriptor subclasses are detected.
	getMember := LookUpObjectMember(cls, "__get__", MemberAccessFlagsDefault, nil)
	if getMember == nil {
		return false
	}

	if requireSetter {
		setMember := LookUpObjectMember(cls, "__set__", MemberAccessFlagsDefault, nil)
		if setMember == nil {
			return false
		}
	}

	return true
}

// IsMaybeDescriptorClass checks whether an instantiable class type (i.e. the
// class itself, not an instance of it) is a descriptor class -- one that
// defines __get__.
//
// The original notes: this is the counterpart to IsMaybeDescriptorInstance,
// which handles declared types in instance form (ClassInstance), while this one
// handles declared types in instantiable form (InstantiableClass), which occurs
// when a type annotation refers to the class object itself. Unlike
// IsMaybeDescriptorInstance, which uses LookUpObjectMember (which only produces
// results for ClassInstance arguments), this function calls LookUpClassMember
// directly -- because LookUpObjectMember returns nil for non-ClassInstance
// types, making it unsuitable for the InstantiableClass argument this function
// receives.
func IsMaybeDescriptorClass(t Type) bool {
	if union, ok := AsUnion(t); ok {
		for _, subtype := range union.Priv.Subtypes {
			if IsMaybeDescriptorClass(subtype) {
				return true
			}
		}
		return false
	}

	cls, ok := AsInstantiableClass(t)
	if !ok {
		return false
	}

	return LookUpClassMember(cls, "__get__", MemberAccessFlagsDefault, nil) != nil
}
