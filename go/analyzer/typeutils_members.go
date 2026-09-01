/*
 * typeutils_members.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 * Author: Eric Traut
 *
 * The class member lookup section of analyzer/typeUtils.ts (pyright 1.1.412),
 * lines 1748-2032. See the header of typeutils.go for the file split.
 *
 * getClassMemberIterator and getClassIterator are TypeScript generators, and
 * their callers depend on lazy evaluation: lookUpClassMember pulls exactly one
 * value and abandons the rest, and getClassMemberIterator breaks out of the
 * class iterator early. Go's range-over-func (iter.Seq) has exactly those
 * semantics -- returning false from the yield function stops the producer --
 * so the translation is direct rather than materializing a slice, which would
 * change both the cost and (because partiallySpecializeType is called per
 * class) the observable work done.
 */

package analyzer

import (
	"iter"
)

// LookUpObjectMember corresponds to lookUpObjectMember. The TypeScript defaults
// flags to Default and skipMroClass to undefined. It returns nil where the
// TypeScript returns undefined.
func LookUpObjectMember(
	objectType *ClassType,
	memberName string,
	flags MemberAccessFlags,
	skipMroClass *ClassType,
) *ClassMember {
	if IsClassInstance(objectType) {
		return LookUpClassMember(objectType, memberName, flags, skipMroClass)
	}

	return nil
}

// LookUpClassMember looks up a member in a class using the multiple-inheritance
// rules defined by Python.
func LookUpClassMember(
	classType *ClassType,
	memberName string,
	flags MemberAccessFlags,
	skipMroClass *ClassType,
) *ClassMember {
	// Look in the metaclass first.
	metaclass := classType.Shared.EffectiveMetaclass

	// Skip the "type" class as an optimization because it is known to not
	// define any instance variables, and it's by far the most common metaclass.
	if metaclass != nil {
		if metaCls, ok := AsClass(metaclass); ok && !ClassTypeIsBuiltInNamed(metaCls, "type") {
			var metaMember *ClassMember
			for member := range GetClassMemberIterator(metaclass, memberName, MemberAccessFlagsSkipClassMembers, nil) {
				metaMember = member
				break
			}

			// If the metaclass defines the member and we didn't hit an Unknown
			// class in the metaclass MRO, use the metaclass member.
			if metaMember != nil && !IsAnyOrUnknown(metaMember.ClassType) {
				// Set isClassMember to true because it's a class member from
				// the perspective of the classType.
				metaMember.IsClassMember = true
				return metaMember
			}
		}
	}

	for member := range GetClassMemberIterator(classType, memberName, flags, skipMroClass) {
		return member
	}
	return nil
}

// GetClassMemberIterator iterates members in a class matching memberName using
// the multiple-inheritance rules.
//
// The original notes: for more details, see this note on method resolution
// order: https://www.python.org/download/releases/2.3/mro/. As it traverses the
// inheritance tree, it applies partial specialization to the base class and
// member. For example, if ClassA inherits from ClassB[str] which inherits from
// Dict[_T1, int], a search for '__iter__' would return a class type of
// Dict[str, int] and a symbolType of (self) -> Iterator[str]. If skipMroClass
// is defined, all MRO classes up to and including that class are skipped.
//
// classType holds a ClassType, AnyType or UnknownType.
func GetClassMemberIterator(
	classType Type,
	memberName string,
	flags MemberAccessFlags,
	skipMroClass *ClassType,
) iter.Seq[*ClassMember] {
	return func(yield func(*ClassMember) bool) {
		declaredTypesOnly := (flags & MemberAccessFlagsDeclaredTypesOnly) != 0
		skippedUndeclaredType := false

		if cls, ok := AsClass(classType); ok {
			classFlags := ClassIteratorFlagsDefault
			if flags&MemberAccessFlagsSkipOriginalClass != 0 {
				// The original re-tests isClass(classType) here, which is
				// already known to hold.
				if IsClassInstance(cls) {
					skipMroClass = ClassTypeCloneAsInstantiable(cls, true)
				} else {
					skipMroClass = cls
				}
			}
			if flags&MemberAccessFlagsSkipBaseClasses != 0 {
				classFlags |= ClassIteratorFlagsSkipBaseClasses
			}
			if flags&MemberAccessFlagsSkipObjectBaseClass != 0 {
				classFlags |= ClassIteratorFlagsSkipObjectBaseClass
			}
			if flags&MemberAccessFlagsSkipTypeBaseClass != 0 {
				classFlags |= ClassIteratorFlagsSkipTypeBaseClass
			}

			for pair := range GetClassIterator(cls, classFlags, skipMroClass) {
				mroClass, specializedMroClass := pair.MroClass, pair.SpecializedMroClass

				if !IsInstantiableClass(mroClass) {
					if !declaredTypesOnly {
						var memberClassType Type = UnknownTypeCreate(false)
						if IsAnyOrUnknown(mroClass) {
							memberClassType = mroClass
						}

						// The class derives from an unknown type, so all bets
						// are off when trying to find a member. Return an
						// unknown symbol.
						cm := &ClassMember{
							Symbol:                 SymbolCreateWithType(SymbolFlagsNone, mroClass, nil),
							IsInstanceMember:       false,
							IsClassMember:          true,
							IsClassVar:             false,
							IsSlotsMember:          false,
							ClassType:              memberClassType,
							UnspecializedClassType: memberClassType,
							IsReadOnly:             false,
							IsTypeDeclared:         false,
							SkippedUndeclaredType:  false,
						}
						if !yield(cm) {
							return
						}
					}
					continue
				}

				specializedCls, ok := AsInstantiableClass(specializedMroClass)
				if !ok {
					continue
				}

				memberFields := ClassTypeGetSymbolTable(specializedCls)
				skipTdEntry := (flags&MemberAccessFlagsSkipTypedDictEntries) != 0 &&
					specializedCls.Shared.TypedDictEntries != nil &&
					specializedCls.Shared.TypedDictEntries.KnownItems.Has(memberName)

				// Look at instance members first if requested.
				if (flags & MemberAccessFlagsSkipInstanceMembers) == 0 {
					symbol, _ := memberFields.Get(memberName)

					if symbol != nil && symbol.IsInstanceMember() && !skipTdEntry {
						hasDeclaredType := symbol.HasTypedDeclarations()
						if !declaredTypesOnly || hasDeclaredType {
							cm := &ClassMember{
								Symbol:                 symbol,
								IsInstanceMember:       true,
								IsClassMember:          symbol.IsClassMember(),
								IsSlotsMember:          symbol.IsSlotsMember(),
								IsClassVar:             IsEffectivelyClassVar(symbol, ClassTypeIsDataClass(specializedCls)),
								ClassType:              specializedCls,
								UnspecializedClassType: mroClass,
								IsReadOnly:             IsMemberReadOnly(specializedCls, memberName),
								IsTypeDeclared:         hasDeclaredType,
								SkippedUndeclaredType:  skippedUndeclaredType,
							}
							if !yield(cm) {
								return
							}
						} else {
							skippedUndeclaredType = true
						}
					}
				}

				// Next look at class members.
				if (flags & MemberAccessFlagsSkipClassMembers) == 0 {
					symbol, _ := memberFields.Get(memberName)

					if symbol != nil && symbol.IsClassMember() && !skipTdEntry {
						hasDeclaredType := symbol.HasTypedDeclarations()
						if !declaredTypesOnly || hasDeclaredType {
							isInstanceMember := symbol.IsInstanceMember()
							isClassMember := true

							// For data classes and typed dicts, variables that
							// are declared within the class are treated as
							// instance variables. This distinction is important
							// in cases where a variable is a callable type
							// because we don't want to bind it to the instance
							// like we would for a class member.
							isDataclass := ClassTypeIsDataClass(specializedCls)
							isTypedDict := ClassTypeIsTypedDictClass(specializedCls)
							if hasDeclaredType && (isDataclass || isTypedDict) {
								decls := symbol.GetDeclarations()
								if len(decls) > 0 && decls[0].DeclBase().Type == DeclarationTypeVariable {
									isInstanceMember = true
									isClassMember = isDataclass
								}
							}

							// Handle the special case of a __call__ class
							// member in a partial class.
							if memberName == "__call__" && cls.Priv.PartialCallType != nil {
								comparand := cls
								if cls.IsInstance() {
									comparand = ClassTypeCloneAsInstantiable(cls, true)
								}
								if ClassTypeIsSameGenericClass(comparand, specializedCls, 0) {
									symbol = SymbolCreateWithType(
										SymbolFlagsClassMember, cls.Priv.PartialCallType, nil)
								}
							}

							cm := &ClassMember{
								Symbol:                 symbol,
								IsInstanceMember:       isInstanceMember,
								IsClassMember:          isClassMember,
								IsSlotsMember:          symbol.IsSlotsMember(),
								IsClassVar:             IsEffectivelyClassVar(symbol, isDataclass),
								ClassType:              specializedCls,
								UnspecializedClassType: mroClass,
								IsReadOnly:             false,
								IsTypeDeclared:         hasDeclaredType,
								SkippedUndeclaredType:  skippedUndeclaredType,
							}
							if !yield(cm) {
								return
							}
						} else {
							skippedUndeclaredType = true
						}
					}
				}
			}
		} else if IsAnyOrUnknown(classType) {
			// The class derives from an unknown type, so all bets are off when
			// trying to find a member. Return an Any or Unknown symbol.
			cm := &ClassMember{
				Symbol:                 SymbolCreateWithType(SymbolFlagsNone, classType, nil),
				IsInstanceMember:       false,
				IsClassMember:          true,
				IsSlotsMember:          false,
				IsClassVar:             false,
				ClassType:              classType,
				UnspecializedClassType: classType,
				IsReadOnly:             false,
				IsTypeDeclared:         false,
				SkippedUndeclaredType:  false,
			}
			yield(cm)
		}
	}
}

// IsMemberReadOnly checks whether the member is effectively read only because
// it belongs to a frozen dataclass or a named tuple.
func IsMemberReadOnly(classType *ClassType, name string) bool {
	if ClassTypeHasNamedTupleEntry(classType, name) {
		return true
	}

	if ClassTypeIsDataClassFrozen(classType) {
		for _, entry := range classType.Shared.DataClassEntries {
			if entry.Name == name {
				return true
			}
		}
	}

	return false
}

// MroClassPair is one step of GetClassIterator. The TypeScript yields a
// two-element tuple; Go has no tuple type, so this names the halves.
type MroClassPair struct {
	MroClass            Type
	SpecializedMroClass Type
}

// GetClassIterator corresponds to getClassIterator. The TypeScript defaults
// flags to Default and skipMroClass to undefined.
func GetClassIterator(classType Type, flags ClassIteratorFlags, skipMroClass *ClassType) iter.Seq[MroClassPair] {
	return func(yield func(MroClassPair) bool) {
		cls, ok := AsClass(classType)
		if !ok {
			return
		}

		foundSkipMroClass := skipMroClass == nil

		for _, mroClass := range cls.Shared.Mro {
			// Are we still searching for the skipMroClass?
			if !foundSkipMroClass && skipMroClass != nil {
				if mroCls, ok := AsClass(mroClass); !ok {
					foundSkipMroClass = true
				} else if ClassTypeIsSameGenericClass(mroCls, skipMroClass, 0) {
					foundSkipMroClass = true
					continue
				} else {
					continue
				}
			}

			// If mroClass is an ancestor of classType, partially specialize it
			// in the context of classType.
			specializedMroClass := PartiallySpecializeType(mroClass, cls, nil, nil)

			// Should we ignore members on the 'object' base class?
			if flags&ClassIteratorFlagsSkipObjectBaseClass != 0 {
				if specializedCls, ok := AsInstantiableClass(specializedMroClass); ok {
					if ClassTypeIsBuiltInNamed(specializedCls, "object") {
						break
					}
				}
			}

			// Should we ignore members on the 'type' base class?
			if flags&ClassIteratorFlagsSkipTypeBaseClass != 0 {
				if specializedCls, ok := AsInstantiableClass(specializedMroClass); ok {
					if ClassTypeIsBuiltInNamed(specializedCls, "type") {
						break
					}
				}
			}

			if !yield(MroClassPair{MroClass: mroClass, SpecializedMroClass: specializedMroClass}) {
				return
			}

			if (flags & ClassIteratorFlagsSkipBaseClasses) != 0 {
				break
			}
		}
	}
}
