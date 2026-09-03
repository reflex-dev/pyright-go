/*
 * typeevaluator_typingstub.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/typeEvaluator.ts (pyright 1.1.412):
 * handleTypingStubTypeAnnotation and createSpecialBuiltInClass.
 *
 * typing.pyi declares about thirty of its members as bare annotations --
 * `Callable: _SpecialForm`, `Optional: _SpecialForm` -- because there is no
 * Python syntax that expresses what they actually mean. Pyright therefore does
 * not read those declarations; it recognizes the names and synthesizes a class
 * for each, with the flags, base class and (for a few) the single variance-typed
 * type parameter that the checker needs.
 *
 * The table is the specification. It is transliterated entry for entry, in the
 * original's order, because the difference between an entry with
 * `isSpecialForm` and one without is the difference between `Optional` being
 * usable in a type expression and not.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/parser"
)

// aliasMapEntry corresponds to the interface of the same name. The module field
// is the original's `'builtins' | 'collections' | 'internals'` union.
type aliasMapEntry struct {
	Alias             string
	Module            string
	ImplicitBaseClass string
	IsSpecialForm     bool

	// IsIllegalInIsinstance and TypeParamVariance are optional in the original.
	// TypeParamVariance is a pointer because Variance's zero value is a real
	// variance and the original distinguishes "absent" from any of them.
	IsIllegalInIsinstance bool
	TypeParamVariance     *Variance
}

func variancePtr(v Variance) *Variance { return &v }

// typingStubAnnotationTypes is the table handleTypingStubTypeAnnotation
// consults, in the original's order.
var typingStubAnnotationTypes = map[string]aliasMapEntry{
	"Tuple":       {Alias: "tuple", Module: "builtins"},
	"Generic":     {Module: "builtins", IsSpecialForm: true},
	"Protocol":    {Module: "builtins", IsSpecialForm: true},
	"Callable":    {Module: "builtins", IsSpecialForm: true},
	"Type":        {Alias: "type", Module: "builtins"},
	"ClassVar":    {Module: "builtins", IsSpecialForm: true},
	"Final":       {Module: "builtins", IsSpecialForm: true},
	"Literal":     {Module: "builtins", IsSpecialForm: true},
	"TypedDict":   {Alias: "TypedDictFallback", Module: "internals"},
	"Union":       {Module: "builtins", IsSpecialForm: true},
	"Optional":    {Module: "builtins", IsSpecialForm: true},
	"Annotated":   {Module: "builtins", IsSpecialForm: true, IsIllegalInIsinstance: true},
	"TypeAlias":   {Module: "builtins", IsSpecialForm: true},
	"Concatenate": {Module: "builtins", IsSpecialForm: true},
	"TypeGuard": {
		Module:            "builtins",
		ImplicitBaseClass: "bool",
		IsSpecialForm:     true,
		TypeParamVariance: variancePtr(VarianceCovariant),
	},
	"Unpack":        {Module: "builtins", IsSpecialForm: true},
	"Required":      {Module: "builtins", IsSpecialForm: true},
	"NotRequired":   {Module: "builtins", IsSpecialForm: true},
	"Self":          {Module: "builtins", IsSpecialForm: true},
	"NoReturn":      {Module: "builtins", IsSpecialForm: true},
	"Never":         {Module: "builtins", IsSpecialForm: true},
	"LiteralString": {Module: "builtins", IsSpecialForm: true},
	"ReadOnly":      {Module: "builtins", IsSpecialForm: true},
	"TypeIs": {
		Module:            "builtins",
		ImplicitBaseClass: "bool",
		IsSpecialForm:     true,
		TypeParamVariance: variancePtr(VarianceInvariant),
	},
	"TypeForm": {
		Module:                "builtins",
		IsSpecialForm:         true,
		TypeParamVariance:     variancePtr(VarianceCovariant),
		IsIllegalInIsinstance: true,
	},
}

// handleTypingStubTypeAnnotation corresponds to the function of the same name.
// The original's comment: handles some special-case type annotations that are
// found within the typings.pyi file.
func (e *typeEvaluator) handleTypingStubTypeAnnotation(node parser.ExpressionNode) Type {
	annotation, ok := node.NodeBase().Parent.(*parser.TypeAnnotationNode)
	if !ok {
		return nil
	}

	nameNode, ok := annotation.D.ValueExpr.(*parser.NameNode)
	if !ok {
		return nil
	}

	assignedName := nameNode.D.Value

	aliasMapEntry, ok := typingStubAnnotationTypes[assignedName]
	if !ok {
		return nil
	}

	if cachedType := e.readTypeCache(node, evalFlagsNonePtr()); cachedType != nil {
		return cachedType
	}

	var specialType Type = e.createSpecialBuiltInClass(node, assignedName, aliasMapEntry)

	// The original's comment: handle 'LiteralString' specially because we want
	// it to act as though it derives from 'str'.
	if assignedName == "LiteralString" {
		asClass := specialType.(*ClassType)
		var strBase Type = AnyTypeCreate(false)
		if e.prefetched != nil && e.prefetched.StrClass != nil {
			strBase = e.prefetched.StrClass
		}
		asClass.Shared.BaseClasses = append(asClass.Shared.BaseClasses, strBase)
		ComputeMroLinearization(asClass)

		specialType = CloneWithTypeForm(specialType, ConvertToInstance(specialType, true))
	}

	// The original's comment: handle 'Never' and 'NoReturn' specially.
	if assignedName == "Never" || assignedName == "NoReturn" {
		var neverType Type = NeverTypeCreateNever()
		if assignedName == "NoReturn" {
			neverType = NeverTypeCreateNoReturn()
		}

		specialType = CloneAsSpecialForm(neverType, specialType.(*ClassType))
		specialType = CloneWithTypeForm(specialType, ConvertToInstance(specialType, true))
	}

	e.writeTypeCache(node, &TypeResult{Type: specialType}, evalFlagsNonePtr(), nil, false)
	return specialType
}

// createSpecialBuiltInClass corresponds to the function of the same name.
func (e *typeEvaluator) createSpecialBuiltInClass(
	node parser.ParseNode,
	assignedName string,
	entry aliasMapEntry,
) *ClassType {
	fileInfo := GetFileInfo(node)
	specialClassType := ClassTypeCreateInstantiable(
		assignedName,
		GetClassFullName(node, fileInfo.ModuleName, assignedName),
		fileInfo.ModuleName,
		fileInfo.FileUri,
		ClassTypeFlagsBuiltIn|ClassTypeFlagsSpecialBuiltIn,
		0,
		nil,
		nil,
		nil,
	)

	if entry.IsSpecialForm {
		specialClassType.Shared.Flags |= ClassTypeFlagsSpecialFormClass
	}

	if entry.IsIllegalInIsinstance {
		specialClassType.Shared.Flags |= ClassTypeFlagsIllegalIsinstanceClass
	}

	// The original's comment: synthesize a single type parameter with the
	// specified variance if specified in the alias map entry.
	if entry.TypeParamVariance != nil {
		typeParam := TypeVarTypeCreateInstance("T", TypeVarKindTypeVar)
		scopeName := assignedName
		scopeType := TypeVarScopeTypeClass
		typeParam = TypeVarTypeCloneForScopeID(typeParam, GetScopeIdForNode(node), &scopeName, &scopeType)
		typeParam.Shared.DeclaredVariance = *entry.TypeParamVariance
		specialClassType.Shared.TypeParams = append(specialClassType.Shared.TypeParams, typeParam)
	}

	// `getDeclaration(node) ?? (node.parent ? getDeclaration(node.parent) : undefined)`
	decl := GetDeclaration(node)
	if decl == nil && node.NodeBase().Parent != nil {
		decl = GetDeclaration(node.NodeBase().Parent)
	}
	specialClassType.Shared.Declaration = decl

	if fileInfo.IsTypingExtensionsStubFile {
		specialClassType.Shared.Flags |= ClassTypeFlagsTypingExtensionClass
	}

	// `implicitBaseClass || alias || 'object'` -- a truthiness chain, so an
	// empty alias falls through to "object".
	baseClassName := "object"
	switch {
	case entry.ImplicitBaseClass != "":
		baseClassName = entry.ImplicitBaseClass
	case entry.Alias != "":
		baseClassName = entry.Alias
	}

	var baseClass Type
	switch entry.Module {
	case "builtins":
		baseClass = e.GetBuiltInType(node, baseClassName)

	case "collections":
		// The original's comment: the typing.pyi file imports collections.
		baseClass = e.getTypeOfModule(node, baseClassName, []string{"collections"})

	case "internals":
		// The original's comment: handle TypedDict specially. It asserts the
		// base class name here.
		if e.prefetched != nil {
			baseClass = e.prefetched.TypedDictPrivateClass
		}
		if baseClass != nil && IsInstantiableClass(baseClass) &&
			ClassTypeIsBuiltInNamed(baseClass.(*ClassType), "_TypedDict", "TypedDictFallback") {
			// The original's comment: the TypedDictFallback class is marked as
			// abstract, but the methods that are abstract are overridden and
			// shouldn't cause the TypedDict to be marked as abstract.
			asClass := baseClass.(*ClassType)
			baseClass = ClassTypeCloneWithNewFlags(
				asClass,
				asClass.Shared.Flags&^(ClassTypeFlagsSupportsAbstractMethods|ClassTypeFlagsTypeCheckOnly),
			)
		}
	}

	if baseClass != nil && IsInstantiableClass(baseClass) {
		if entry.Alias != "" {
			return ClassTypeCloneForTypingAlias(baseClass.(*ClassType), assignedName)
		}

		specialClassType.Shared.BaseClasses = append(specialClassType.Shared.BaseClasses, baseClass)
		specialClassType.Shared.EffectiveMetaclass = baseClass.(*ClassType).Shared.EffectiveMetaclass
		ComputeMroLinearization(specialClassType)
		return specialClassType
	}

	specialClassType.Shared.BaseClasses = append(specialClassType.Shared.BaseClasses, UnknownTypeCreate(false))
	specialClassType.Shared.EffectiveMetaclass = UnknownTypeCreate(false)
	ComputeMroLinearization(specialClassType)

	return specialClassType
}
