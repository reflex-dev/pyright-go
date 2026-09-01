/*
 * types_union.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 * Author: Eric Traut
 *
 * OverloadedType, NeverType, AnyType, TypeCondition and UnionType.
 *
 * Transliterated from analyzer/types.ts (pyright 1.1.412). Split out of
 * types.go only so no single Go file has to carry all 4000 lines.
 */

package analyzer

import (
	"sort"
	"strconv"

	"github.com/microsoft/pyright/go/common"
)

// ---------------------------------------------------------------------------
// OverloadedType
// ---------------------------------------------------------------------------

// OverloadedDetailsPriv corresponds to the interface of the same name. The
// original names both fields with a leading underscore to discourage direct
// access; read them through OverloadedTypeGetOverloads and
// OverloadedTypeGetImplementation.
type OverloadedDetailsPriv struct {
	Overloads      []*FunctionType
	Implementation Type
}

// OverloadedType corresponds to the interface of the same name.
type OverloadedType struct {
	TypeBase
	Priv OverloadedDetailsPriv
}

func (t *OverloadedType) cloneSelf() Type {
	clone := *t
	clone.cloneBaseInto()
	return &clone
}

func (t *OverloadedType) isUnionable() {}

// OverloadedTypeCreate corresponds to OverloadedType.create. A nil
// implementation stands in for the optional parameter.
func OverloadedTypeCreate(overloads []*FunctionType, implementation Type) *OverloadedType {
	newType := &OverloadedType{
		TypeBase: TypeBase{
			Category: TypeCategoryOverloaded,
			Flags:    TypeFlagsInstance,
		},
		Priv: OverloadedDetailsPriv{
			Overloads:      []*FunctionType{},
			Implementation: implementation,
		},
	}

	for _, overload := range overloads {
		OverloadedTypeAddOverload(newType, overload)
	}

	if implementation != nil {
		if fn, ok := AsFunction(implementation); ok {
			fn.Priv.Overloaded = newType
		}
	}

	return newType
}

// OverloadedTypeAddOverload adds a new overload or an implementation.
func OverloadedTypeAddOverload(t *OverloadedType, functionType *FunctionType) {
	functionType.Priv.Overloaded = t
	t.Priv.Overloads = append(t.Priv.Overloads, functionType)
}

func OverloadedTypeGetOverloads(t *OverloadedType) []*FunctionType {
	return t.Priv.Overloads
}

// OverloadedTypeGetImplementation returns nil where the TypeScript returns
// undefined.
func OverloadedTypeGetImplementation(t *OverloadedType) Type {
	return t.Priv.Implementation
}

// ---------------------------------------------------------------------------
// NeverType
// ---------------------------------------------------------------------------

// NeverDetailsPriv corresponds to the interface of the same name.
type NeverDetailsPriv struct {
	IsNoReturn bool
}

// NeverType corresponds to the interface of the same name. It is not a
// UnionableType.
type NeverType struct {
	TypeBase
	Priv NeverDetailsPriv
}

func (t *NeverType) cloneSelf() Type {
	clone := *t
	clone.cloneBaseInto()
	return &clone
}

var neverInstance = &NeverType{
	TypeBase: TypeBase{
		Category: TypeCategoryNever,
		Flags:    TypeFlagsInstance | TypeFlagsInstantiable,
	},
	Priv: NeverDetailsPriv{IsNoReturn: false},
}

var noReturnInstance = &NeverType{
	TypeBase: TypeBase{
		Category: TypeCategoryNever,
		Flags:    TypeFlagsInstance | TypeFlagsInstantiable,
	},
	Priv: NeverDetailsPriv{IsNoReturn: true},
}

// NeverTypeCreateNever corresponds to NeverType.createNever.
func NeverTypeCreateNever() *NeverType { return neverInstance }

// NeverTypeCreateNoReturn corresponds to NeverType.createNoReturn.
func NeverTypeCreateNoReturn() *NeverType { return noReturnInstance }

// NeverTypeConvertToInstance corresponds to NeverType.convertToInstance.
func NeverTypeConvertToInstance(t *NeverType) *NeverType {
	// Remove the specialForm or typeForm if present. Otherwise return the
	// existing type.
	if t.Props == nil || (t.Props.SpecialForm == nil && t.Props.TypeForm == nil) {
		return t
	}

	if t.Priv.IsNoReturn {
		return NeverTypeCreateNoReturn()
	}
	return NeverTypeCreateNever()
}

// ---------------------------------------------------------------------------
// AnyType
// ---------------------------------------------------------------------------

// AnyDetailsPriv corresponds to the interface of the same name.
type AnyDetailsPriv struct {
	IsEllipsis bool
}

// AnyType corresponds to the interface of the same name.
type AnyType struct {
	TypeBase
	Priv AnyDetailsPriv
}

func (t *AnyType) cloneSelf() Type {
	clone := *t
	clone.cloneBaseInto()
	return &clone
}

func (t *AnyType) isUnionable() {}

// anyInstanceSpecialForm is a distinct instance from anyInstance even though
// the two are structurally identical, because callers compare by reference.
var anyInstanceSpecialForm = &AnyType{
	TypeBase: TypeBase{
		Category: TypeCategoryAny,
		Flags:    TypeFlagsInstance | TypeFlagsInstantiable,
	},
	Priv: AnyDetailsPriv{IsEllipsis: false},
}

var anyInstance = &AnyType{
	TypeBase: TypeBase{
		Category: TypeCategoryAny,
		Flags:    TypeFlagsInstance | TypeFlagsInstantiable,
	},
	Priv: AnyDetailsPriv{IsEllipsis: false},
}

var ellipsisInstance = &AnyType{
	TypeBase: TypeBase{
		Category: TypeCategoryAny,
		Flags:    TypeFlagsInstance | TypeFlagsInstantiable,
	},
	Priv: AnyDetailsPriv{IsEllipsis: true},
}

// AnyTypeCreate corresponds to AnyType.create. The TypeScript defaults
// isEllipsis to false.
func AnyTypeCreate(isEllipsis bool) *AnyType {
	if isEllipsis {
		return ellipsisInstance
	}
	return anyInstance
}

// AnyTypeCreateSpecialForm corresponds to AnyType.createSpecialForm.
func AnyTypeCreateSpecialForm() *AnyType { return anyInstanceSpecialForm }

// AnyTypeConvertToInstance corresponds to AnyType.convertToInstance.
func AnyTypeConvertToInstance(t *AnyType) *AnyType {
	// Remove the "special form" if present. Otherwise return the existing type.
	if t.Props != nil && t.Props.SpecialForm != nil {
		return AnyTypeCreate(false)
	}
	return t
}

// ---------------------------------------------------------------------------
// TypeCondition
// ---------------------------------------------------------------------------

// TypeCondition references a single condition associated with a constrained
// TypeVar.
type TypeCondition struct {
	TypeVar         *TypeVarType
	ConstraintIndex int
}

// TypeConditionCombine corresponds to TypeCondition.combine.
func TypeConditionCombine(conditions1, conditions2 []TypeCondition) []TypeCondition {
	if conditions1 == nil {
		return conditions2
	}

	if conditions2 == nil {
		return conditions1
	}

	// Deduplicate the lists.
	combined := append([]TypeCondition{}, conditions1...)
	for _, c1 := range conditions2 {
		found := false
		for _, c2 := range combined {
			if typeConditionCompare(c1, c2) == 0 {
				found = true
				break
			}
		}
		if !found {
			combined = append(combined, c1)
		}
	}

	// Always keep the conditions sorted for easier comparison.
	//
	// sort.SliceStable rather than sort.Slice because Array.prototype.sort is
	// required to be stable, and an unstable sort could order equal-comparing
	// conditions differently from the original.
	sort.SliceStable(combined, func(i, j int) bool {
		return typeConditionCompare(combined[i], combined[j]) < 0
	})
	return combined
}

// typeConditionCompare corresponds to the unexported _compare.
func typeConditionCompare(c1, c2 TypeCondition) int {
	if c1.TypeVar.Shared.Name < c2.TypeVar.Shared.Name {
		return -1
	} else if c1.TypeVar.Shared.Name > c2.TypeVar.Shared.Name {
		return 1
	}
	if c1.ConstraintIndex < c2.ConstraintIndex {
		return -1
	} else if c1.ConstraintIndex > c2.ConstraintIndex {
		return 1
	}
	return 0
}

// TypeConditionIsSame corresponds to TypeCondition.isSame.
func TypeConditionIsSame(conditions1, conditions2 []TypeCondition) bool {
	if conditions1 == nil {
		return conditions2 == nil
	}

	if conditions2 == nil || len(conditions1) != len(conditions2) {
		return false
	}

	for index, c1 := range conditions1 {
		if c1.TypeVar.Priv.NameWithScope != conditions2[index].TypeVar.Priv.NameWithScope ||
			c1.ConstraintIndex != conditions2[index].ConstraintIndex {
			return false
		}
	}
	return true
}

// TypeConditionIsCompatible determines if the two conditions can be used at the
// same time. If one constraint list contains a constraint for a type variable,
// and the same constraint is not in the other constraint list, the two are
// considered incompatible.
func TypeConditionIsCompatible(conditions1, conditions2 []TypeCondition) bool {
	if conditions1 == nil || conditions2 == nil {
		return true
	}

	for _, c1 := range conditions1 {
		foundTypeVarMatch := false
		exactMatch := false
		for _, c2 := range conditions2 {
			if c1.TypeVar.Priv.NameWithScope == c2.TypeVar.Priv.NameWithScope {
				foundTypeVarMatch = true
				if c1.ConstraintIndex == c2.ConstraintIndex {
					exactMatch = true
					break
				}
			}
		}

		if foundTypeVarMatch && !exactMatch {
			return false
		}
	}

	return true
}

// ---------------------------------------------------------------------------
// UnionType
// ---------------------------------------------------------------------------

// LiteralTypes corresponds to the interface of the same name.
//
// LiteralIntMap is keyed by a string rather than by the literal value itself.
// The original uses `Map<bigint | number, UnionableType>`, where JavaScript
// compares bigint and number keys by value -- and treats 1n and 1 as *different*
// keys. A Go map keyed by LiteralValue would compare LiteralInt by its *big.Int
// pointer instead, so two literals with the same value would not collide.
// literalNumberKey reproduces both halves of the JavaScript behavior.
type LiteralTypes struct {
	LiteralStrMap  *common.OrderedMap[string, UnionableType]
	LiteralIntMap  *common.OrderedMap[string, UnionableType]
	LiteralEnumMap *common.OrderedMap[string, UnionableType]
}

// literalNumberKey produces the map key for a numeric literal, encoding which
// arm of `bigint | number` it came from so that 1n and 1 stay distinct keys as
// they are in JavaScript.
func literalNumberKey(value LiteralValue) string {
	switch v := value.(type) {
	case LiteralInt:
		if v.Value == nil {
			return "n:<nil>"
		}
		return "n:" + v.Value.String()
	case LiteralFloat:
		return "f:" + formatFloatKey(float64(v))
	default:
		return ""
	}
}

// UnionDetailsPriv corresponds to the interface of the same name.
type UnionDetailsPriv struct {
	Subtypes         []UnionableType
	LiteralInstances LiteralTypes
	LiteralClasses   LiteralTypes
	TypeAliasSources *common.OrderedSet[*UnionType]

	IncludesRecursiveTypeAlias bool

	// IncludesEnumLiteral is cached, and relies on all union construction
	// adding subtypes through UnionTypeAddType.
	IncludesEnumLiteral bool
}

// UnionType corresponds to the interface of the same name. It is not a
// UnionableType.
type UnionType struct {
	TypeBase
	Priv UnionDetailsPriv
}

func (t *UnionType) cloneSelf() Type {
	clone := *t
	clone.cloneBaseInto()
	return &clone
}

// UnionTypeCreate corresponds to UnionType.create.
func UnionTypeCreate() *UnionType {
	return &UnionType{
		TypeBase: TypeBase{
			Category: TypeCategoryUnion,
			Flags:    TypeFlagsInstance | TypeFlagsInstantiable,
		},
		Priv: UnionDetailsPriv{
			Subtypes: []UnionableType{},
		},
	}
}

// UnionTypeAddType corresponds to UnionType.addType.
func UnionTypeAddType(unionType *UnionType, newType UnionableType) {
	if cls, ok := AsClass(newType); ok && ClassTypeIsEnumClass(cls) {
		if _, isEnumLiteral := cls.Priv.LiteralValue.(*EnumLiteral); isEnumLiteral {
			unionType.Priv.IncludesEnumLiteral = true
		}
	}

	// If we're adding a string, integer or enum literal, add it to the
	// corresponding literal map to speed up some operations. It's not uncommon
	// for unions to contain hundreds of literals.
	if cls, ok := AsClass(newType); ok && cls.Priv.LiteralValue != nil &&
		(cls.Props == nil || cls.Props.Condition == nil) {
		var literalMaps *LiteralTypes
		if IsClassInstance(newType) {
			literalMaps = &unionType.Priv.LiteralInstances
		} else {
			literalMaps = &unionType.Priv.LiteralClasses
		}

		if ClassTypeIsBuiltInNamed(cls, "str") {
			if literalMaps.LiteralStrMap == nil {
				literalMaps.LiteralStrMap = common.NewOrderedMap[string, UnionableType]()
			}
			literalMaps.LiteralStrMap.Set(string(cls.Priv.LiteralValue.(LiteralString)), newType)
		} else if ClassTypeIsBuiltInNamed(cls, "int") {
			if literalMaps.LiteralIntMap == nil {
				literalMaps.LiteralIntMap = common.NewOrderedMap[string, UnionableType]()
			}
			literalMaps.LiteralIntMap.Set(literalNumberKey(cls.Priv.LiteralValue), newType)
		} else if ClassTypeIsEnumClass(cls) {
			if literalMaps.LiteralEnumMap == nil {
				literalMaps.LiteralEnumMap = common.NewOrderedMap[string, UnionableType]()
			}
			enumLiteral := cls.Priv.LiteralValue.(*EnumLiteral)
			literalMaps.LiteralEnumMap.Set(enumLiteral.GetName(), newType)
		}
	}

	unionType.Flags &= newType.Base().Flags
	unionType.Priv.Subtypes = append(unionType.Priv.Subtypes, newType)

	if tv, ok := AsTypeVar(newType); ok && tv.Shared.RecursiveAlias != nil && tv.Shared.RecursiveAlias.Name != "" {
		// Note that at least one recursive type alias was included in this
		// union. We'll need to expand it before the union is used.
		unionType.Priv.IncludesRecursiveTypeAlias = true
	}
}

// UnionTypeContainsType determines whether the union contains a specified
// subtype. If exclusionSet is passed, the method skips any subtype indexes that
// are in the set and adds a found index to the exclusion set. This speeds up
// union type comparisons.
func UnionTypeContainsType(
	unionType *UnionType,
	subtype Type,
	options TypeSameOptions,
	exclusionSet *common.OrderedSet[int],
	recursionCount int,
) bool {
	// Handle string literals as a special case because unions can sometimes
	// contain hundreds of string literal types.
	if cls, ok := AsClass(subtype); ok &&
		(cls.Props == nil || cls.Props.Condition == nil) &&
		cls.Priv.LiteralValue != nil {
		var literalMaps *LiteralTypes
		if IsClassInstance(subtype) {
			literalMaps = &unionType.Priv.LiteralInstances
		} else {
			literalMaps = &unionType.Priv.LiteralClasses
		}

		if ClassTypeIsBuiltInNamed(cls, "str") && literalMaps.LiteralStrMap != nil {
			return literalMaps.LiteralStrMap.Has(string(cls.Priv.LiteralValue.(LiteralString)))
		} else if ClassTypeIsBuiltInNamed(cls, "int") && literalMaps.LiteralIntMap != nil {
			return literalMaps.LiteralIntMap.Has(literalNumberKey(cls.Priv.LiteralValue))
		} else if ClassTypeIsEnumClass(cls) && literalMaps.LiteralEnumMap != nil {
			enumLiteral := cls.Priv.LiteralValue.(*EnumLiteral)
			return literalMaps.LiteralEnumMap.Has(enumLiteral.GetName())
		}
	}

	foundIndex := -1
	for i, t := range unionType.Priv.Subtypes {
		if exclusionSet != nil && exclusionSet.Has(i) {
			continue
		}

		if IsTypeSame(t, subtype, options, recursionCount) {
			foundIndex = i
			break
		}
	}

	if foundIndex < 0 {
		return false
	}

	if exclusionSet != nil {
		exclusionSet.Add(foundIndex)
	}
	return true
}

// UnionTypeAddTypeAliasSource corresponds to UnionType.addTypeAliasSource.
func UnionTypeAddTypeAliasSource(unionType *UnionType, typeAliasSource Type) {
	if typeAliasSource.Base().Category != TypeCategoryUnion {
		return
	}

	source := typeAliasSource.(*UnionType)

	var sourcesToAdd []*UnionType
	if source.Props != nil && source.Props.TypeAliasInfo != nil {
		sourcesToAdd = []*UnionType{source}
	} else if source.Priv.TypeAliasSources != nil {
		sourcesToAdd = source.Priv.TypeAliasSources.Values()
	}

	if sourcesToAdd != nil {
		if unionType.Priv.TypeAliasSources == nil {
			unionType.Priv.TypeAliasSources = common.NewOrderedSet[*UnionType]()
		}

		for _, s := range sourcesToAdd {
			unionType.Priv.TypeAliasSources.Add(s)
		}
	}
}

// ---------------------------------------------------------------------------
// Variance, RecursiveAliasInfo, TypeVarKind
// ---------------------------------------------------------------------------

// Variance corresponds to the const enum of the same name.
type Variance int

const (
	VarianceAuto Variance = iota
	VarianceUnknown
	VarianceInvariant
	VarianceCovariant
	VarianceContravariant
)

// RecursiveAliasInfo corresponds to the interface of the same name.
type RecursiveAliasInfo struct {
	// Name, ScopeID and IsPep695Syntax are used for recursive type aliases.
	Name           string
	ScopeID        TypeVarScopeId
	IsPep695Syntax bool

	// TypeParams holds the type parameters for a recursive type alias.
	TypeParams []*TypeVarType
}

// TypeVarKind corresponds to the enum of the same name.
type TypeVarKind int

const (
	TypeVarKindTypeVar TypeVarKind = iota
	TypeVarKindTypeVarTuple
	TypeVarKindParamSpec
)

// formatFloatKey renders a `number` literal as a map key. JavaScript Map keys
// use SameValueZero, under which -0 and 0 are the same key and NaN equals
// itself, so the -0 case is normalized here.
func formatFloatKey(f float64) string {
	if f == 0 {
		return "0"
	}
	return strconv.FormatFloat(f, 'g', -1, 64)
}
