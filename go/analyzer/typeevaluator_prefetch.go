/*
 * typeevaluator_prefetch.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/typeEvaluator.ts (pyright 1.1.412):
 * initializePrefetchedTypes, the module and builtin lookups it is built from,
 * and the accessors that read it back.
 *
 * This is the evaluator's bootstrap. Nothing in the type model means anything
 * until `object`, `type`, `int`, `str`, `tuple` and the rest have been resolved
 * out of builtins.pyi and typing.pyi, and every one of those resolutions goes
 * through getEffectiveTypeOfSymbol, which is already ported. So this layer is
 * what turns the spine that lands above it into something that can produce a
 * real type rather than Unknown -- once class creation exists to answer it.
 *
 * The prefetch assignment happens before the fetches, not after, which is load
 * bearing: object is a type and type is an object in builtins.pyi, so the
 * fetches re-enter this function and must find `prefetched` already non-nil.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/common/uri"
	"github.com/microsoft/pyright/go/parser"
)

// initializePrefetchedTypes corresponds to the function of the same name.
func (e *typeEvaluator) initializePrefetchedTypes(node parser.ParseNode) {
	if e.prefetched != nil {
		return
	}

	// The original's comment: some of these types have cyclical dependencies on
	// each other, so don't re-enter this block once we start executing it.
	e.prefetched = &PrefetchedTypes{}

	fileInfo := GetFileInfo(node)
	p := e.prefetched

	p.ObjectClass = e.GetBuiltInType(node, "object")
	p.TypeClass = e.GetBuiltInType(node, "type")

	p.FunctionClass = e.getTypesType(node, "FunctionType")
	if p.FunctionClass == nil {
		p.FunctionClass = e.GetBuiltInType(node, "function")
	}

	p.MethodClass = e.getTypesType(node, "MethodType")

	p.UnionTypeClass = e.getTypesType(node, "UnionType")
	if p.UnionTypeClass != nil && IsClass(p.UnionTypeClass) {
		p.UnionTypeClass.(*ClassType).Shared.Flags |= ClassTypeFlagsSpecialFormClass
	}

	// The original's comment: initialize and cache "Collection" to break a
	// cyclical dependency that occurs when resolving tuple below.
	e.getTypingType(node, "Collection")

	p.NoneTypeClass = e.getTypeshedType(node, "NoneType")
	if p.NoneTypeClass == nil {
		p.NoneTypeClass = UnknownTypeCreate(false)
	}

	p.TupleClass = e.GetBuiltInType(node, "tuple")
	p.BoolClass = e.GetBuiltInType(node, "bool")
	p.IntClass = e.GetBuiltInType(node, "int")
	p.StrClass = e.GetBuiltInType(node, "str")
	p.DictClass = e.GetBuiltInType(node, "dict")
	p.ModuleTypeClass = e.getTypingType(node, "ModuleType")

	p.TypedDictPrivateClass = e.getTypeCheckerInternalsType(node, "TypedDictFallback")
	if p.TypedDictPrivateClass == nil {
		p.TypedDictPrivateClass = e.getTypingType(node, "_TypedDict")
	}

	p.TypedDictClass = e.getTypingType(node, "TypedDict")
	p.AwaitableClass = e.getTypingType(node, "Awaitable")
	p.MappingClass = e.getTypingType(node, "Mapping")

	// The original's comment: don't attempt to resolve the string.templatelib if
	// pyright is configured for Python 3.13 or older. Doing so will either fail
	// to resolve (if running on Python 3.13 or older) or resolve to the
	// templatelib.py source file (if running on Python 3.14).
	if fileInfo.ExecutionEnvironment.PythonVersion.IsGreaterOrEqualTo(common.PythonVersion3_14) {
		p.TemplateClass = e.getTypeOfModule(node, "Template", []string{"string", "templatelib"})
	} else {
		p.TemplateClass = UnknownTypeCreate(false)
	}

	p.SupportsKeysAndGetItemClass = e.getTypeshedType(node, "SupportsKeysAndGetItem")
	if p.SupportsKeysAndGetItemClass == nil {
		// The original's comment: fall back on 'Mapping' if
		// 'SupportsKeysAndGetItem' is not available.
		p.SupportsKeysAndGetItemClass = p.MappingClass
	}

	// The original's comment: wire up the `Any` class to the special-form
	// version of our internal AnyType.
	if p.ObjectClass != nil && IsInstantiableClass(p.ObjectClass) &&
		p.TypeClass != nil && IsInstantiableClass(p.TypeClass) {
		anyClass := ClassTypeCreateInstantiable(
			"Any",
			"typing.Any",
			"typing",
			uri.Empty(),
			ClassTypeFlagsBuiltIn|ClassTypeFlagsSpecialFormClass|ClassTypeFlagsIllegalIsinstanceClass,
			-1,
			nil,
			p.TypeClass,
			nil,
		)
		anyClass.Shared.BaseClasses = append(anyClass.Shared.BaseClasses, p.ObjectClass)
		ComputeMroLinearization(anyClass)

		anySpecialForm := AnyTypeCreateSpecialForm()
		if IsAny(anySpecialForm) {
			anySpecialForm.Base().SetSpecialForm(anyClass)
			anySpecialForm.Base().SetTypeForm(ConvertToInstance(anySpecialForm, false))
		}
	}
}

/*
 * The module lookups. Each returns nil where the original returns undefined.
 */

// getTypingType corresponds to the function of the same name.
func (e *typeEvaluator) getTypingType(node parser.ParseNode, symbolName string) Type {
	if t := e.getTypeOfModule(node, symbolName, []string{"typing"}); t != nil {
		return t
	}
	return e.getTypeOfModule(node, symbolName, []string{"typing_extensions"})
}

// getTypeCheckerInternalsType corresponds to the function of the same name.
func (e *typeEvaluator) getTypeCheckerInternalsType(node parser.ParseNode, symbolName string) Type {
	return e.getTypeOfModule(node, symbolName, []string{"_typeshed", "_type_checker_internals"})
}

// getTypesType corresponds to the function of the same name.
func (e *typeEvaluator) getTypesType(node parser.ParseNode, symbolName string) Type {
	return e.getTypeOfModule(node, symbolName, []string{"types"})
}

// getTypeshedType corresponds to the function of the same name.
func (e *typeEvaluator) getTypeshedType(node parser.ParseNode, symbolName string) Type {
	return e.getTypeOfModule(node, symbolName, []string{"_typeshed"})
}

// getTypeOfModule corresponds to the function of the same name. The original
// passes an AbsoluteModuleDescriptor to importLookup, which in this port is a
// three-parameter function whose first parameter carries the Uri arm of the
// original's `Uri | AbsoluteModuleDescriptor` union.
func (e *typeEvaluator) getTypeOfModule(node parser.ParseNode, symbolName string, nameParts []string) Type {
	fileInfo := GetFileInfo(node)
	lookupResult := e.importLookup(nil, &AbsoluteModuleDescriptor{
		NameParts:        nameParts,
		ImportingFileUri: fileInfo.FileUri,
	}, nil)

	if lookupResult == nil {
		return nil
	}

	symbol, ok := lookupResult.SymbolTable.Get(symbolName)
	if !ok || symbol == nil {
		return nil
	}

	return e.GetEffectiveTypeOfSymbol(symbol)
}

/*
 * The builtin scope lookups.
 */

// GetBuiltInType corresponds to getBuiltInType.
func (e *typeEvaluator) GetBuiltInType(node parser.ParseNode, name string) Type {
	if scope := GetScopeForNode(node); scope != nil {
		builtInScope := GetBuiltInScope(scope)
		if nameType := builtInScope.LookUpSymbol(name); nameType != nil {
			return e.GetEffectiveTypeOfSymbol(nameType)
		}
	}

	return UnknownTypeCreate(false)
}

// GetBuiltInObject corresponds to getBuiltInObject. The original's typeArgs
// parameter is optional, and a nil slice here means the same thing.
func (e *typeEvaluator) GetBuiltInObject(node parser.ParseNode, name string, typeArgs []Type) Type {
	nameType := e.GetBuiltInType(node, name)
	if IsInstantiableClass(nameType) {
		classType := nameType.(*ClassType)
		if typeArgs != nil {
			classType = ClassTypeSpecialize(classType, typeArgs, nil, false, nil, nil)
		}

		return ClassTypeCloneAsInstance(classType, false)
	}

	return nameType
}

/*
 * The accessors. Each reads the prefetched table back and answers undefined --
 * nil here -- when the fetch did not produce a class.
 */

func (e *typeEvaluator) prefetchedInstantiableClass(t Type) *ClassType {
	if e.prefetched != nil && t != nil && IsInstantiableClass(t) {
		return t.(*ClassType)
	}
	return nil
}

// GetTypedDictClassType corresponds to getTypedDictClassType. The original
// returns the *private* class, which is the one with the synthesized methods.
func (e *typeEvaluator) GetTypedDictClassType() *ClassType {
	if e.prefetched == nil {
		return nil
	}
	return e.prefetchedInstantiableClass(e.prefetched.TypedDictPrivateClass)
}

func (e *typeEvaluator) GetTupleClassType() *ClassType {
	if e.prefetched == nil {
		return nil
	}
	return e.prefetchedInstantiableClass(e.prefetched.TupleClass)
}

func (e *typeEvaluator) GetDictClassType() *ClassType {
	if e.prefetched == nil {
		return nil
	}
	return e.prefetchedInstantiableClass(e.prefetched.DictClass)
}

func (e *typeEvaluator) GetStrClassType() *ClassType {
	if e.prefetched == nil {
		return nil
	}
	return e.prefetchedInstantiableClass(e.prefetched.StrClass)
}

func (e *typeEvaluator) GetObjectType() Type {
	if e.prefetched != nil && e.prefetched.ObjectClass != nil {
		return ConvertToInstance(e.prefetched.ObjectClass, false)
	}
	return UnknownTypeCreate(false)
}

func (e *typeEvaluator) GetNoneType() Type {
	if e.prefetched != nil && e.prefetched.NoneTypeClass != nil {
		return ConvertToInstance(e.prefetched.NoneTypeClass, false)
	}
	return UnknownTypeCreate(false)
}

func (e *typeEvaluator) GetUnionClassType() Type {
	if e.prefetched != nil && e.prefetched.UnionTypeClass != nil {
		return e.prefetched.UnionTypeClass
	}
	return UnknownTypeCreate(false)
}

func (e *typeEvaluator) GetTypeClassType() *ClassType {
	if e.prefetched == nil {
		return nil
	}
	return e.prefetchedInstantiableClass(e.prefetched.TypeClass)
}
