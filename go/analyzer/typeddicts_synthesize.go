/*
 * typeddicts_synthesize.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/typedDicts.ts (pyright 1.1.412):
 * synthesizeTypedDictClassMethods with its six local factories, plus
 * getTypedDictMappingEquivalent and getTypedDictDictEquivalent.
 *
 * A TypedDict has no runtime class of its own -- at runtime it is a plain dict.
 * Everything that makes `d["x"]` and `d.get("x")` typecheck is synthesized here,
 * as a set of overloads keyed on *string literal types*. `get("x")` and
 * `get("y")` resolve to different overloads because the literal `"x"` matches
 * only the overload whose key parameter is `Literal["x"]`. That is why every
 * accessor below is an OverloadedType with one entry per known item rather than
 * a single signature.
 *
 * The two `__init__` overloads are not alternatives in the usual sense. The
 * first takes a single positional mapping and gives *every* field a default, so
 * `T(other_td)` checks; the second takes keyword arguments and gives defaults
 * only to non-required fields, so `T(x=1)` enforces required keys. Order
 * matters: the first is tried first, and only a real mapping argument matches it.
 *
 * Read-only and required-ness drive which accessors exist at all, not just their
 * signatures. A read-only entry gets no `setdefault` overload and no `update`
 * keyword; a required entry gets no `pop`, because popping it would leave the
 * dict invalid. When every entry is read-only, `__delitem__` is omitted outright
 * and `update`'s mapping parameter becomes Never.
 *
 * The closed-TypedDict handling is the subtle part. An ordinary TypedDict may
 * carry unknown extra keys, so the catch-all `get(str)` overload must return
 * Any and `pop(str)` must return object. A closed one cannot, so those overloads
 * can be given the real extra-items type -- and if there are no extra items at
 * all, even the key type of `items`/`keys`/`values` narrows to the union of the
 * known literal keys.
 *
 * One ordering detail is called out by the original and is easy to lose: the
 * three `update` overloads are returned as [method2, method1, method3], not in
 * declaration order, so that method1's signature is the one quoted in the error
 * message when nothing matches.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/parser"
)

// SynthesizeTypedDictClassMethods corresponds to synthesizeTypedDictClassMethods.
func SynthesizeTypedDictClassMethods(
	evaluator TypeEvaluator, node parser.ParseNode, classType *ClassType,
) {
	classScopeID := GetTypeVarScopeID(classType)
	clsName, selfName, mapName, kName, defaultName, kwargsName := "cls", "self", "__map", "k", "default", "kwargs"

	// The original's comment: synthesize a __new__ method.
	newType := FunctionTypeCreateSynthesizedInstance("__new__", FunctionTypeFlagsConstructorMethod)
	FunctionTypeAddParam(newType, FunctionParamCreate(
		parser.ParamCategorySimple, classType, FunctionParamFlagsTypeDeclared, &clsName, nil, nil))
	FunctionTypeAddDefaultParams(newType, false)
	newType.Shared.DeclaredReturnType = ClassTypeCloneAsInstance(classType, false)
	newType.Priv.ConstructorTypeVarScopeID = classScopeID

	// The original's comment: synthesize an __init__ method with two overrides.
	initOverride1 := FunctionTypeCreateSynthesizedInstance("__init__", FunctionTypeFlagsOverloaded)
	FunctionTypeAddParam(initOverride1, FunctionParamCreate(
		parser.ParamCategorySimple, ClassTypeCloneAsInstance(classType, false),
		FunctionParamFlagsTypeDeclared, &selfName, nil, nil))
	initOverride1.Shared.DeclaredReturnType = evaluator.GetNoneType()
	initOverride1.Priv.ConstructorTypeVarScopeID = classScopeID

	// The original's comment: the first parameter must be positional-only.
	FunctionTypeAddParam(initOverride1, FunctionParamCreate(
		parser.ParamCategorySimple, ClassTypeCloneAsInstance(classType, false),
		FunctionParamFlagsTypeDeclared, &mapName, nil, nil))

	entries := GetTypedDictMembersForClass(evaluator, classType, false)
	extraEntriesInfo := entries.ExtraItems
	if extraEntriesInfo == nil {
		extraEntriesInfo = GetEffectiveExtraItemsEntryType(evaluator, classType)
	}
	allEntriesAreReadOnly := entries.KnownItems.Size() > 0

	if entries.KnownItems.Size() > 0 {
		FunctionTypeAddPositionOnlyParamSeparator(initOverride1)

		// The original's comment: all subsequent parameters must be named, so
		// insert an empty "*".
		FunctionTypeAddKeywordOnlyParamSeparator(initOverride1)
	}

	initOverride2 := FunctionTypeCreateSynthesizedInstance("__init__", FunctionTypeFlagsOverloaded)
	FunctionTypeAddParam(initOverride2, FunctionParamCreate(
		parser.ParamCategorySimple, ClassTypeCloneAsInstance(classType, false),
		FunctionParamFlagsTypeDeclared, &selfName, nil, nil))
	initOverride2.Shared.DeclaredReturnType = evaluator.GetNoneType()
	initOverride2.Priv.ConstructorTypeVarScopeID = classScopeID

	if entries.KnownItems.Size() > 0 {
		// The original's comment: all parameters must be named, so insert an
		// empty "*".
		FunctionTypeAddKeywordOnlyParamSeparator(initOverride2)
	}

	entries.KnownItems.ForEach(func(entry *TypedDictEntry, name string) {
		entryName := name

		// Overload 1 defaults every field, so a lone mapping argument suffices.
		FunctionTypeAddParam(initOverride1, FunctionParamCreate(
			parser.ParamCategorySimple, entry.ValueType, FunctionParamFlagsTypeDeclared,
			&entryName, entry.ValueType, nil))

		// Overload 2 defaults only the non-required fields, so required keys must
		// be passed.
		var defaultType Type
		if !entry.IsRequired {
			defaultType = entry.ValueType
		}
		FunctionTypeAddParam(initOverride2, FunctionParamCreate(
			parser.ParamCategorySimple, entry.ValueType, FunctionParamFlagsTypeDeclared,
			&entryName, defaultType, nil))

		if !entry.IsReadOnly {
			allEntriesAreReadOnly = false
		}
	})

	if entries.ExtraItems != nil && !IsNever(entries.ExtraItems.ValueType) {
		FunctionTypeAddParam(initOverride1, FunctionParamCreate(
			parser.ParamCategoryKwargsDict, entries.ExtraItems.ValueType,
			FunctionParamFlagsTypeDeclared, &kwargsName, nil, nil))
		FunctionTypeAddParam(initOverride2, FunctionParamCreate(
			parser.ParamCategoryKwargsDict, entries.ExtraItems.ValueType,
			FunctionParamFlagsTypeDeclared, &kwargsName, nil, nil))
	}

	symbolTable := ClassTypeGetSymbolTable(classType)
	initType := OverloadedTypeCreate([]*FunctionType{initOverride1, initOverride2}, nil)
	symbolTable.Set("__init__", SymbolCreateWithType(SymbolFlagsClassMember, initType, nil))
	symbolTable.Set("__new__", SymbolCreateWithType(SymbolFlagsClassMember, newType, nil))

	strClass := evaluator.GetBuiltInType(node, "str")

	// The original's comment: synthesize a "get", pop, and setdefault method for
	// each named entry.
	if !IsInstantiableClass(strClass) {
		return
	}
	strClassType := strClass.(*ClassType)

	s := &typedDictSynthesizer{
		evaluator:             evaluator,
		node:                  node,
		classType:             classType,
		entries:               entries,
		extraEntriesInfo:      extraEntriesInfo,
		allEntriesAreReadOnly: allEntriesAreReadOnly,
		strClass:              strClassType,
		selfParam: FunctionParamCreate(
			parser.ParamCategorySimple, ClassTypeCloneAsInstance(classType, false),
			FunctionParamFlagsTypeDeclared, &selfName, nil, nil),
		kName:       kName,
		defaultName: defaultName,
	}

	getOverloads := []*FunctionType{}
	popOverloads := []*FunctionType{}
	setDefaultOverloads := []*FunctionType{}

	entries.KnownItems.ForEach(func(entry *TypedDictEntry, name string) {
		nameLiteralType := ClassTypeCloneAsInstance(
			ClassTypeCloneWithLiteral(strClassType, LiteralString(name)), false)

		getOverloads = append(getOverloads,
			s.createGetMethod(nameLiteralType, entry.ValueType, false, entry.IsRequired, false))

		getOverloads = append(getOverloads,
			s.createGetMethod(nameLiteralType, entry.ValueType, true, entry.IsRequired, entry.IsRequired))

		// The original's comment: add a pop method if the entry is not required.
		if !entry.IsRequired && !entry.IsReadOnly {
			popOverloads = append(popOverloads,
				s.createPopMethods(nameLiteralType, entry.ValueType, entry.IsRequired)...)
		}

		if !entry.IsReadOnly {
			setDefaultOverloads = append(setDefaultOverloads,
				s.createSetDefaultMethod(nameLiteralType, entry.ValueType))
		}
	})

	strType := ClassTypeCloneAsInstance(strClassType, false)

	// The original's comment: if the class is closed, we can assume that any
	// other keys that are present will return the default parameter value or the
	// extra entries value type.
	if ClassTypeIsTypedDictEffectivelyClosed(classType) {
		getOverloads = append(getOverloads, s.createGetMethod(
			strType,
			CombineTypes([]Type{extraEntriesInfo.ValueType, evaluator.GetNoneType()}, nil),
			false, true, false))
		getOverloads = append(getOverloads,
			s.createGetMethod(strType, extraEntriesInfo.ValueType, true, false, false))
	} else {
		// The original's comment: provide a final `get` overload that handles the
		// general case where the key is a str but the literal value isn't known.
		getOverloads = append(getOverloads,
			s.createGetMethod(strType, AnyTypeCreate(false), false, false, false))
		getOverloads = append(getOverloads,
			s.createGetMethod(strType, AnyTypeCreate(false), true, false, false))
	}

	// The original's comment: add a catch-all pop method.
	if ClassTypeIsTypedDictEffectivelyClosed(classType) {
		if !IsNever(extraEntriesInfo.ValueType) {
			popOverloads = append(popOverloads,
				s.createPopMethods(strType, extraEntriesInfo.ValueType, false)...)
		}
	} else {
		popOverloads = append(popOverloads,
			s.createPopMethods(strType, evaluator.GetObjectType(), false)...)
	}

	symbolTable.Set("get", SymbolCreateWithType(SymbolFlagsClassMember,
		OverloadedTypeCreate(getOverloads, nil), nil))

	if len(popOverloads) > 0 {
		symbolTable.Set("pop", SymbolCreateWithType(SymbolFlagsClassMember,
			OverloadedTypeCreate(popOverloads, nil), nil))
	}

	if len(setDefaultOverloads) > 0 {
		symbolTable.Set("setdefault", SymbolCreateWithType(SymbolFlagsClassMember,
			OverloadedTypeCreate(setDefaultOverloads, nil), nil))
	}

	if !allEntriesAreReadOnly {
		symbolTable.Set("__delitem__", SymbolCreateWithType(SymbolFlagsClassMember,
			s.createDelItemMethod(strType), nil))
	}

	s.allEntriesAreReadOnly = allEntriesAreReadOnly
	symbolTable.Set("update", SymbolCreateWithType(SymbolFlagsClassMember,
		s.createUpdateMethod(strType), nil))

	// The original's comment: if the TypedDict is closed and all of its entries
	// are NotRequired and not ReadOnly, add a "clear" and "popitem" method.
	dictValueType := GetTypedDictDictEquivalent(evaluator, classType, 0)

	if dictValueType != nil {
		clearMethod := FunctionTypeCreateSynthesizedInstance("clear", FunctionTypeFlagsNone)
		FunctionTypeAddParam(clearMethod, s.selfParam)
		clearMethod.Shared.DeclaredReturnType = evaluator.GetNoneType()
		symbolTable.Set("clear", SymbolCreateWithType(SymbolFlagsClassMember, clearMethod, nil))

		popItemMethod := FunctionTypeCreateSynthesizedInstance("popitem", FunctionTypeFlagsNone)
		FunctionTypeAddParam(popItemMethod, s.selfParam)

		var tupleType Type = UnknownTypeCreate(false)
		if tc := evaluator.GetTupleClassType(); tc != nil && IsInstantiableClass(tc) {
			tupleType = SpecializeTupleClass(ClassTypeCloneAsInstance(tc, false), []*TupleTypeArg{
				{Type: strType, IsUnbounded: false},
				{Type: dictValueType, IsUnbounded: false},
			}, true, false)
		}

		popItemMethod.Shared.DeclaredReturnType = tupleType
		symbolTable.Set("popitem", SymbolCreateWithType(SymbolFlagsClassMember, popItemMethod, nil))
	}

	// The original's comment: if the TypedDict is closed, we can provide a more
	// accurate value type for the "items", "keys" and "values" methods.
	mappingValueType := GetTypedDictMappingEquivalent(evaluator, classType)

	if mappingValueType == nil {
		return
	}

	var keyValueType Type = strType

	// The original's comment: if we know that there can be no more items, we can
	// provide a more accurate key type consisting of all known keys.
	if entries.ExtraItems != nil && IsNever(entries.ExtraItems.ValueType) {
		keyTypes := []Type{}
		for _, key := range entries.KnownItems.Keys() {
			keyTypes = append(keyTypes, ClassTypeCloneWithLiteral(strType, LiteralString(key)))
		}
		keyValueType = CombineTypes(keyTypes, nil)
	}

	for _, methodName := range []string{"items", "keys", "values"} {
		method := FunctionTypeCreateSynthesizedInstance(methodName, FunctionTypeFlagsNone)
		FunctionTypeAddParam(method, s.selfParam)

		returnTypeClass := evaluator.GetTypingType(node, "dict_"+methodName)
		if returnTypeClass != nil && IsInstantiableClass(returnTypeClass) &&
			len(returnTypeClass.(*ClassType).Shared.TypeParams) == 2 {
			method.Shared.DeclaredReturnType = ClassTypeSpecialize(
				ClassTypeCloneAsInstance(returnTypeClass.(*ClassType), false),
				[]Type{keyValueType, mappingValueType}, nil, false, nil, nil)

			symbolTable.Set(methodName, SymbolCreateWithType(SymbolFlagsClassMember, method, nil))
		}
	}
}

// typedDictSynthesizer carries what the original's nested functions close over.
type typedDictSynthesizer struct {
	evaluator             TypeEvaluator
	node                  parser.ParseNode
	classType             *ClassType
	entries               *TypedDictEntries
	extraEntriesInfo      *TypedDictEntry
	allEntriesAreReadOnly bool
	strClass              *ClassType
	selfParam             FunctionParam
	kName                 string
	defaultName           string
}

// createDefaultTypeVar corresponds to the local function of the same name. The
// TypeVar is scoped to the individual overload, not the class, so
// `d.get("x", 0)` and `d.get("y", "")` solve independently.
func (s *typedDictSynthesizer) createDefaultTypeVar(fn *FunctionType) *TypeVarType {
	defaultTypeVar := TypeVarTypeCreateInstance("__TDefault", TypeVarKindTypeVar)
	scopeType := TypeVarScopeTypeFunction
	return TypeVarTypeCloneForScopeID(
		defaultTypeVar, string(fn.Shared.TypeVarScopeID), &s.classType.Shared.Name, &scopeType)
}

// createGetMethod corresponds to the local function of the same name.
func (s *typedDictSynthesizer) createGetMethod(
	keyType Type, valueType Type, includeDefault bool, isEntryRequired bool, defaultTypeMatchesField bool,
) *FunctionType {
	getOverload := FunctionTypeCreateSynthesizedInstance("get", FunctionTypeFlagsOverloaded)
	FunctionTypeAddParam(getOverload, s.selfParam)
	getOverload.Shared.TypeVarScopeID = TypeVarScopeId(GetScopeIdForNode(s.node))
	FunctionTypeAddParam(getOverload, FunctionParamCreate(
		parser.ParamCategorySimple, keyType, FunctionParamFlagsTypeDeclared, &s.kName, nil, nil))

	if !includeDefault {
		if isEntryRequired {
			getOverload.Shared.DeclaredReturnType = valueType
		} else {
			getOverload.Shared.DeclaredReturnType = CombineTypes(
				[]Type{valueType, s.evaluator.GetNoneType()}, nil)
		}
		return getOverload
	}

	defaultTypeVar := s.createDefaultTypeVar(getOverload)
	var defaultParamType Type
	var returnType Type

	if isEntryRequired {
		// The original's comment: if the entry is required, the type of the
		// default param doesn't matter because the type will always come from the
		// value.
		defaultParamType = AnyTypeCreate(false)
		returnType = valueType
	} else {
		if defaultTypeMatchesField {
			defaultParamType = valueType
		} else {
			defaultParamType = CombineTypes([]Type{valueType, defaultTypeVar}, nil)
		}

		returnType = defaultParamType
	}

	FunctionTypeAddParam(getOverload, FunctionParamCreate(
		parser.ParamCategorySimple, defaultParamType, FunctionParamFlagsTypeDeclared,
		&s.defaultName, nil, nil))
	getOverload.Shared.DeclaredReturnType = returnType

	return getOverload
}

// createPopMethods corresponds to the local function of the same name.
func (s *typedDictSynthesizer) createPopMethods(
	keyType Type, valueType Type, isEntryRequired bool,
) []*FunctionType {
	keyParam := FunctionParamCreate(
		parser.ParamCategorySimple, keyType, FunctionParamFlagsTypeDeclared, &s.kName, nil, nil)

	popOverload1 := FunctionTypeCreateSynthesizedInstance("pop", FunctionTypeFlagsOverloaded)
	FunctionTypeAddParam(popOverload1, s.selfParam)
	FunctionTypeAddParam(popOverload1, keyParam)
	popOverload1.Shared.DeclaredReturnType = valueType

	popOverload2 := FunctionTypeCreateSynthesizedInstance("pop", FunctionTypeFlagsOverloaded)
	FunctionTypeAddParam(popOverload2, s.selfParam)
	FunctionTypeAddParam(popOverload2, keyParam)
	popOverload2.Shared.TypeVarScopeID = TypeVarScopeId(GetScopeIdForNode(s.node))
	defaultTypeVar := s.createDefaultTypeVar(popOverload2)

	var defaultParamType Type
	var returnType Type

	if isEntryRequired {
		// The original's comment: if the entry is required, the type of the
		// default param doesn't matter because the type will always come from the
		// value.
		defaultParamType = AnyTypeCreate(false)
		returnType = valueType
	} else {
		defaultParamType = CombineTypes([]Type{valueType, defaultTypeVar}, nil)
		returnType = defaultParamType
	}

	FunctionTypeAddParam(popOverload2, FunctionParamCreate(
		parser.ParamCategorySimple, defaultParamType, FunctionParamFlagsTypeDeclared,
		&s.defaultName, defaultParamType, nil))
	popOverload2.Shared.DeclaredReturnType = returnType

	return []*FunctionType{popOverload1, popOverload2}
}

// createSetDefaultMethod corresponds to the local function of the same name.
func (s *typedDictSynthesizer) createSetDefaultMethod(keyType Type, valueType Type) *FunctionType {
	setDefaultOverload := FunctionTypeCreateSynthesizedInstance("setdefault", FunctionTypeFlagsOverloaded)
	FunctionTypeAddParam(setDefaultOverload, s.selfParam)
	FunctionTypeAddParam(setDefaultOverload, FunctionParamCreate(
		parser.ParamCategorySimple, keyType, FunctionParamFlagsTypeDeclared, &s.kName, nil, nil))
	FunctionTypeAddParam(setDefaultOverload, FunctionParamCreate(
		parser.ParamCategorySimple, valueType, FunctionParamFlagsTypeDeclared, &s.defaultName, nil, nil))
	setDefaultOverload.Shared.DeclaredReturnType = valueType
	return setDefaultOverload
}

// createDelItemMethod corresponds to the local function of the same name. The
// original names the synthesized function "delitem" while registering it under
// "__delitem__"; that asymmetry is reproduced rather than corrected, since the
// name appears in printed types.
func (s *typedDictSynthesizer) createDelItemMethod(keyType Type) *FunctionType {
	delItemOverload := FunctionTypeCreateSynthesizedInstance("delitem", FunctionTypeFlagsOverloaded)
	FunctionTypeAddParam(delItemOverload, s.selfParam)
	FunctionTypeAddParam(delItemOverload, FunctionParamCreate(
		parser.ParamCategorySimple, keyType, FunctionParamFlagsTypeDeclared, &s.kName, nil, nil))
	delItemOverload.Shared.DeclaredReturnType = s.evaluator.GetNoneType()
	return delItemOverload
}

// createUpdateMethod corresponds to the local function of the same name.
func (s *typedDictSynthesizer) createUpdateMethod(strType *ClassType) Type {
	mapName := "__m"

	// The original's comment: overload 1: update(__m: Partial[<writable fields>], /)
	updateMethod1 := FunctionTypeCreateSynthesizedInstance("update", FunctionTypeFlagsOverloaded)
	FunctionTypeAddParam(updateMethod1, s.selfParam)

	// The original's comment: overload 2: update(__m: Iterable[tuple[<name>, <type>]], /)
	updateMethod2 := FunctionTypeCreateSynthesizedInstance("update", FunctionTypeFlagsOverloaded)
	FunctionTypeAddParam(updateMethod2, s.selfParam)

	// The original's comment: overload 3: update(*, <name>: <type>, ...)
	updateMethod3 := FunctionTypeCreateSynthesizedInstance("update", FunctionTypeFlagsOverloaded)
	FunctionTypeAddParam(updateMethod3, s.selfParam)

	// The original's comment: if all entries are read-only, don't allow updates.
	var mapParamType Type
	if s.allEntriesAreReadOnly {
		mapParamType = NeverTypeCreateNever()
	} else {
		mapParamType = ClassTypeCloneAsInstance(
			ClassTypeCloneForPartialTypedDict(s.classType), false)
	}
	FunctionTypeAddParam(updateMethod1, FunctionParamCreate(
		parser.ParamCategorySimple, mapParamType, FunctionParamFlagsTypeDeclared, &mapName, nil, nil))

	if s.entries.KnownItems.Size() > 0 {
		FunctionTypeAddPositionOnlyParamSeparator(updateMethod1)
		FunctionTypeAddKeywordOnlyParamSeparator(updateMethod3)
	}

	updateMethod1.Shared.DeclaredReturnType = s.evaluator.GetNoneType()
	updateMethod2.Shared.DeclaredReturnType = s.evaluator.GetNoneType()
	updateMethod3.Shared.DeclaredReturnType = s.evaluator.GetNoneType()

	tuplesToCombine := []Type{}
	tupleClass := s.evaluator.GetBuiltInType(s.node, "tuple")

	s.entries.KnownItems.ForEach(func(entry *TypedDictEntry, name string) {
		if entry.IsReadOnly {
			return
		}
		entryName := name

		// The original's comment: for writable entries, add a tuple entry.
		if tupleClass != nil && IsInstantiableClass(tupleClass) {
			tupleType := SpecializeTupleClass(
				ClassTypeCloneAsInstance(tupleClass.(*ClassType), false),
				[]*TupleTypeArg{
					{Type: ClassTypeCloneWithLiteral(strType, LiteralString(entryName)), IsUnbounded: false},
					{Type: entry.ValueType, IsUnbounded: false},
				}, false, false)

			tuplesToCombine = append(tuplesToCombine, tupleType)
		}

		// The original's comment: for writable entries, add a keyword argument.
		FunctionTypeAddParam(updateMethod3, FunctionParamCreate(
			parser.ParamCategorySimple, entry.ValueType, FunctionParamFlagsTypeDeclared,
			&entryName, AnyTypeCreate(true), nil))
	})

	iterableClass := s.evaluator.GetTypingType(s.node, "Iterable")
	if iterableClass != nil && IsInstantiableClass(iterableClass) {
		iterableType := ClassTypeCloneAsInstance(iterableClass.(*ClassType), false)

		FunctionTypeAddParam(updateMethod2, FunctionParamCreate(
			parser.ParamCategorySimple,
			ClassTypeSpecialize(iterableType, []Type{CombineTypes(tuplesToCombine, nil)},
				nil, false, nil, nil),
			FunctionParamFlagsTypeDeclared, &mapName, nil, nil))
	}

	if s.entries.KnownItems.Size() > 0 {
		FunctionTypeAddPositionOnlyParamSeparator(updateMethod2)
	}

	// The original's comment: note that the order of method1 and method2 is
	// swapped. This is done so the method1 signature is used in the error message
	// when neither method2 or method1 match.
	return OverloadedTypeCreate(
		[]*FunctionType{updateMethod2, updateMethod1, updateMethod3}, nil)
}

// GetTypedDictMappingEquivalent corresponds to getTypedDictMappingEquivalent. It
// returns the value type this TypedDict is equivalent to as a Mapping[str, T],
// or nil when the answer is just the uninformative Mapping[str, object].
func GetTypedDictMappingEquivalent(evaluator TypeEvaluator, classType *ClassType) Type {
	// The original's comment: if the TypedDict class isn't closed, it's just a
	// normal Mapping[str, object].
	if !ClassTypeIsTypedDictEffectivelyClosed(classType) {
		return nil
	}

	entries := GetTypedDictMembersForClass(evaluator, classType, false)
	typesToCombine := []Type{}

	entries.KnownItems.ForEach(func(entry *TypedDictEntry, _ string) {
		typesToCombine = append(typesToCombine, entry.ValueType)
	})

	if entries.ExtraItems != nil {
		typesToCombine = append(typesToCombine, entries.ExtraItems.ValueType)
	}

	// The original's comment: is the final value type 'object'?
	valueType := CombineTypes(typesToCombine, nil)
	if IsClassInstance(valueType) && ClassTypeIsBuiltInNamed(valueType.(*ClassType), "object") {
		return nil
	}

	return valueType
}

// GetTypedDictDictEquivalent corresponds to getTypedDictDictEquivalent. The
// original's comment: if the TypedDict class is consistent with dict[str, T], it
// returns T. Otherwise it returns undefined.
func GetTypedDictDictEquivalent(
	evaluator TypeEvaluator, classType *ClassType, recursionCount int,
) Type {
	// The original's comment: if the TypedDict class isn't closed, it's not
	// equivalent to a dict.
	if !ClassTypeIsTypedDictEffectivelyClosed(classType) {
		return nil
	}

	entries := GetTypedDictMembersForClass(evaluator, classType, false)

	// The original's comment: if there is no "extraItems" defined or it is
	// read-only, it's not equivalent to a dict.
	if entries.ExtraItems == nil || entries.ExtraItems.IsReadOnly {
		return nil
	}

	dictValueType := entries.ExtraItems.ValueType

	isEquivalentToDict := true
	entries.KnownItems.ForEach(func(entry *TypedDictEntry, _ string) {
		if entry.IsReadOnly || entry.IsRequired {
			isEquivalentToDict = false
		}

		dictValueType = CombineTypes([]Type{dictValueType, entry.ValueType}, nil)

		// The invariance check is what rules out a dict whose value type merely
		// *contains* the entry type: dict[str, T] is invariant in T, so a widened
		// union is not an equivalent.
		if !evaluator.AssignType(dictValueType, entry.ValueType, nil, nil,
			AssignTypeFlagsInvariant, recursionCount+1) {
			isEquivalentToDict = false
		}
	})

	if !isEquivalentToDict {
		return nil
	}

	return dictValueType
}
