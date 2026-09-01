/*
 * typeutils_anywalk.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 * Author: Eric Traut
 *
 * The member-collection and Any/Unknown detection helpers from
 * analyzer/typeUtils.ts (pyright 1.1.412), lines 2511-2650. See the header of
 * typeutils.go for the file split.
 */

package analyzer

// GetMembersForClass corresponds to getMembersForClass. It populates
// symbolTable in place, as the original does.
func GetMembersForClass(classType *ClassType, symbolTable SymbolTable, includeInstanceVars bool) {
	for _, mroClass := range classType.Shared.Mro {
		if cls, ok := AsInstantiableClass(mroClass); ok {
			// Add any new member variables from this class.
			isClassTypedDict := ClassTypeIsTypedDictClass(cls)
			ClassTypeGetSymbolTable(cls).ForEach(func(symbol *Symbol, name string) {
				if symbol.IsClassMember() || (includeInstanceVars && symbol.IsInstanceMember()) {
					if !isClassTypedDict || !IsTypedDictMemberAccessedThroughIndex(symbol) {
						if !symbol.IsInitVar() {
							existingSymbol, _ := symbolTable.Get(name)

							if existingSymbol == nil {
								symbolTable.Set(name, symbol)
							} else if !existingSymbol.HasTypedDeclarations() && symbol.HasTypedDeclarations() {
								// If the existing symbol is unannotated but a
								// parent class has an annotation for the
								// symbol, use the parent type instead.
								symbolTable.Set(name, symbol)
							}
						}
					}
				}
			})
		}
	}

	// Add members of the metaclass as well.
	if !includeInstanceVars {
		metaclass := classType.Shared.EffectiveMetaclass
		if metaclass != nil {
			if metaCls, ok := AsInstantiableClass(metaclass); ok {
				for _, mroClass := range metaCls.Shared.Mro {
					cls, ok := AsInstantiableClass(mroClass)
					if !ok {
						break
					}

					ClassTypeGetSymbolTable(cls).ForEach(func(symbol *Symbol, name string) {
						existingSymbol, _ := symbolTable.Get(name)

						if existingSymbol == nil {
							symbolTable.Set(name, symbol)
						} else if !existingSymbol.HasTypedDeclarations() && symbol.HasTypedDeclarations() {
							// If the existing symbol is unannotated but a
							// parent class has an annotation for the symbol,
							// use the parent type instead.
							symbolTable.Set(name, symbol)
						}
					})
				}
			}
		}
	}
}

// GetMembersForModule corresponds to getMembersForModule. It populates
// symbolTable in place.
func GetMembersForModule(moduleType *ModuleType, symbolTable SymbolTable) {
	// Start with the loader fields. If there are any symbols of the same name
	// defined within the module, they will overwrite the loader fields.
	if moduleType.Priv.LoaderFields != nil {
		moduleType.Priv.LoaderFields.ForEach(func(symbol *Symbol, name string) {
			symbolTable.Set(name, symbol)
		})
	}

	moduleType.Priv.Fields.ForEach(func(symbol *Symbol, name string) {
		symbolTable.Set(name, symbol)
	})
}

// anyWalker corresponds to the class of the same name declared inside
// containsAnyRecursive.
type anyWalker struct {
	*TypeWalker
	*TypeWalkerDefaults

	foundAny       bool
	includeUnknown bool
}

func newAnyWalker(includeUnknown bool) *anyWalker {
	w := &anyWalker{includeUnknown: includeUnknown}
	w.TypeWalker = NewTypeWalker(w)
	w.TypeWalkerDefaults = NewTypeWalkerDefaults(w.TypeWalker)
	return w
}

func (w *anyWalker) VisitAny(t *AnyType) {
	w.foundAny = true
	w.CancelWalk()
}

func (w *anyWalker) VisitUnknown(t *UnknownType) {
	if w.includeUnknown {
		w.foundAny = true
		w.CancelWalk()
	}
}

// ContainsAnyRecursive determines if the type contains an Any recursively. The
// TypeScript defaults includeUnknown to true.
func ContainsAnyRecursive(t Type, includeUnknown bool) bool {
	walker := newAnyWalker(includeUnknown)
	walker.Walk(t)
	return walker.foundAny
}

// anyOrUnknownWalker corresponds to the class of the same name declared inside
// containsAnyOrUnknown.
type anyOrUnknownWalker struct {
	*TypeWalker
	*TypeWalkerDefaults

	// anyOrUnknownType holds an AnyType or UnknownType, or nil.
	anyOrUnknownType Type
	recurse          bool
}

func newAnyOrUnknownWalker(recurse bool) *anyOrUnknownWalker {
	w := &anyOrUnknownWalker{recurse: recurse}
	w.TypeWalker = NewTypeWalker(w)
	w.TypeWalkerDefaults = NewTypeWalkerDefaults(w.TypeWalker)
	return w
}

// VisitTypeAlias deliberately does nothing: the original overrides it to avoid
// exploring type aliases.
func (w *anyOrUnknownWalker) VisitTypeAlias(t Type) {}

func (w *anyOrUnknownWalker) VisitUnknown(t *UnknownType) {
	if w.anyOrUnknownType != nil {
		w.anyOrUnknownType = PreserveUnknown(w.anyOrUnknownType, t)
	} else {
		w.anyOrUnknownType = t
	}
}

func (w *anyOrUnknownWalker) VisitAny(t *AnyType) {
	if w.anyOrUnknownType != nil {
		w.anyOrUnknownType = PreserveUnknown(w.anyOrUnknownType, t)
	} else {
		w.anyOrUnknownType = t
	}
}

func (w *anyOrUnknownWalker) VisitClass(t *ClassType) {
	if w.recurse {
		w.TypeWalkerDefaults.VisitClass(t)
	}
}

func (w *anyOrUnknownWalker) VisitFunction(t *FunctionType) {
	if w.recurse {
		// A function with a "..." type is effectively an "Any".
		if FunctionTypeIsGradualCallableForm(t) {
			if w.anyOrUnknownType != nil {
				w.anyOrUnknownType = PreserveUnknown(w.anyOrUnknownType, AnyTypeCreate(false))
			} else {
				w.anyOrUnknownType = AnyTypeCreate(false)
			}
		}

		w.TypeWalkerDefaults.VisitFunction(t)
	}
}

// ContainsAnyOrUnknown determines if the type contains an Any or Unknown type.
// If so, it returns the Any or Unknown type; Unknowns are preferred over Any if
// both are present. If recurse is true, it recurses through type arguments and
// parameters. It returns nil where the TypeScript returns undefined.
func ContainsAnyOrUnknown(t Type, recurse bool) Type {
	walker := newAnyOrUnknownWalker(recurse)
	walker.Walk(t)
	return walker.anyOrUnknownType
}

// IsPartlyUnknown determines if any part of the type contains "Unknown",
// including any type arguments. The TypeScript defaults recursionCount to 0.
//
// The original notes: this function does not use the TypeWalker because it is
// called very frequently, and allocating a memory walker object for every call
// significantly increases peak memory usage. That duplication is preserved here
// rather than folded into a walker.
func IsPartlyUnknown(t Type, recursionCount int) bool {
	if recursionCount > MaxTypeRecursionCount {
		return false
	}
	recursionCount++

	if IsUnknown(t) {
		return true
	}

	// If this is a generic type alias, see if any of its type arguments are
	// either unspecified or are partially known.
	if t.Base().Props != nil && t.Base().Props.TypeAliasInfo != nil {
		for _, typeArg := range t.Base().Props.TypeAliasInfo.TypeArgs {
			if IsPartlyUnknown(typeArg, recursionCount) {
				return true
			}
		}
	}

	// See if a union contains an unknown type.
	if IsUnion(t) {
		return FindSubtype(t, func(subtype Type) bool {
			return IsPartlyUnknown(subtype, recursionCount)
		}) != nil
	}

	// See if an object or class has an unknown type argument.
	if cls, ok := AsClass(t); ok {
		// If this is a reference to the class itself, as opposed to a reference
		// to a type that represents the class and its subclasses, don't flag
		// the type as partially unknown.
		if !cls.Priv.IncludeSubclasses {
			return false
		}

		if !ClassTypeIsPseudoGenericClass(cls) {
			var typeArgs []Type
			if cls.Priv.TupleTypeArgs != nil {
				typeArgs = make([]Type, 0, len(cls.Priv.TupleTypeArgs))
				for _, e := range cls.Priv.TupleTypeArgs {
					typeArgs = append(typeArgs, e.Type)
				}
			} else {
				typeArgs = cls.Priv.TypeArgs
			}
			for _, argType := range typeArgs {
				if IsPartlyUnknown(argType, recursionCount) {
					return true
				}
			}
		}

		return false
	}

	// See if a function has an unknown type.
	if overloaded, ok := AsOverloaded(t); ok {
		for _, overload := range OverloadedTypeGetOverloads(overloaded) {
			if IsPartlyUnknown(overload, recursionCount) {
				return true
			}
		}
		return false
	}

	if fn, ok := AsFunction(t); ok {
		for i := range fn.Shared.Parameters {
			// Ignore parameters such as "*" that have no name.
			if fn.Shared.Parameters[i].Name != nil && *fn.Shared.Parameters[i].Name != "" {
				paramType := FunctionTypeGetParamType(fn, i)
				if IsPartlyUnknown(paramType, recursionCount) {
					return true
				}
			}
		}

		if fn.Shared.DeclaredReturnType != nil &&
			!FunctionTypeIsParamSpecValue(fn) &&
			IsPartlyUnknown(fn.Shared.DeclaredReturnType, recursionCount) {
			return true
		}

		return false
	}

	return false
}

// ExplodeGenericClass "explodes" a generic class with a single union type
// argument into a union of classes with each element of the union -- e.g.
// Foo[A | B] becomes Foo[A] | Foo[B].
func ExplodeGenericClass(classType *ClassType) Type {
	if len(classType.Priv.TypeArgs) != 1 {
		return classType
	}
	union, ok := AsUnion(classType.Priv.TypeArgs[0])
	if !ok {
		return classType
	}

	specialized := make([]Type, 0, len(union.Priv.Subtypes))
	for _, subtype := range union.Priv.Subtypes {
		specialized = append(specialized, ClassTypeSpecialize(classType, []Type{subtype}, nil, false, nil, nil))
	}

	return CombineTypes(specialized, nil)
}

// CombineSameSizedTuples combines a union of same-sized tuples into a single
// tuple with that size. Otherwise it returns the type unchanged.
//
// Note the doc comment on the original says it returns undefined when the union
// is not of same-sized tuples, but every such path actually returns `type`.
func CombineSameSizedTuples(t Type, tupleType Type) Type {
	if tupleType == nil {
		return t
	}
	tupleCls, ok := AsInstantiableClass(tupleType)
	if !ok || IsUnboundedTupleClass(tupleCls) {
		return t
	}

	var tupleEntries [][]Type
	isValid := true

	DoForEachSubtype(t, func(subtype Type, index int, allSubtypes []Type) {
		if subtypeCls, ok := AsClassInstance(subtype); ok {
			var tupleClass *ClassType
			if IsTupleClass(subtypeCls) && !IsUnboundedTupleClass(subtypeCls) {
				tupleClass = subtypeCls
			}

			if tupleClass == nil {
				// Look in the mro list to see if this subtype derives from a
				// tuple with a known size. This includes named tuples.
				for _, mroClass := range subtypeCls.Shared.Mro {
					if mroCls, ok := AsClass(mroClass); ok && IsTupleClass(mroCls) && !IsUnboundedTupleClass(mroCls) {
						tupleClass = mroCls
						break
					}
				}
			}

			if tupleClass != nil && tupleClass.Priv.TupleTypeArgs != nil {
				if tupleEntries != nil {
					if len(tupleEntries) == len(tupleClass.Priv.TupleTypeArgs) {
						for i, entry := range tupleClass.Priv.TupleTypeArgs {
							tupleEntries[i] = append(tupleEntries[i], entry.Type)
						}
					} else {
						isValid = false
					}
				} else {
					tupleEntries = make([][]Type, 0, len(tupleClass.Priv.TupleTypeArgs))
					for _, entry := range tupleClass.Priv.TupleTypeArgs {
						tupleEntries = append(tupleEntries, []Type{entry.Type})
					}
				}
			} else {
				isValid = false
			}
		} else {
			isValid = false
		}
	})

	if !isValid || tupleEntries == nil {
		return t
	}

	args := make([]*TupleTypeArg, 0, len(tupleEntries))
	for _, entry := range tupleEntries {
		args = append(args, &TupleTypeArg{Type: CombineTypes(entry, nil), IsUnbounded: false})
	}

	return ConvertToInstance(
		SpecializeTupleClass(tupleCls, args, true, false),
		true,
	)
}
