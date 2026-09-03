/*
 * typeevaluator_narrowassign.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/typeEvaluator.ts (pyright 1.1.412):
 * narrowTypeBasedOnAssignment, reportPossibleUnknownAssignment,
 * stripLiteralValue, isFinalVariableDeclaration.
 *
 * What `x: Sequence[int] = [1, 2]` leaves behind. The declared type says what
 * the name accepts; narrowTypeBasedOnAssignment decides what it currently holds,
 * which is neither the declared type nor the assigned type in general but a
 * subtype-by-subtype negotiation between them.
 *
 * The original opens with a TODO saying the rules here are undefined in the
 * typing spec, not internally consistent, and probably unsound, and should be
 * reworked after a public discussion. That is transliterated verbatim rather
 * than tidied, because the inconsistency is observable: it is the reason a few
 * of these cases exist at all.
 *
 * The negotiation, per assigned subtype:
 *
 *   - A literal that already appears in a declared union is kept as-is. This is
 *     purely to avoid the n^2 walk over unions of thousands of literals.
 *   - Otherwise every declared subtype it can be assigned to is considered. If
 *     the two are bidirectionally assignable they are equivalent or one is
 *     gradual, and the DECLARED one wins -- except for a TypedDict with narrowed
 *     entries and for a callback protocol receiving a function, where the
 *     assigned one carries more information.
 *   - An Unknown is kept, so code flow analysis converges and strict mode still
 *     reports it.
 *   - If nothing in the declared type accepts it, the result is Never, and the
 *     unnarrowed assigned subtype is returned instead.
 *
 * The Unknown handling at the end distinguishes incomplete from complete: an
 * incomplete Unknown propagates its incompleteness, while a complete one is
 * unioned with the declared type so completions still work in strict mode.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/localization"
	"github.com/microsoft/pyright/go/parser"
)

// narrowTypeBasedOnAssignment corresponds to the function of the same name.
func (e *typeEvaluator) narrowTypeBasedOnAssignment(
	declaredType Type, assignedTypeResult *TypeResult,
) *TypeResult {
	narrowedType := MapSubtypes(assignedTypeResult.Type, func(assignedSubtype Type) Type {
		// The original's comment: handle the special case where the assigned type
		// is a literal type. Some types include very large unions of literal
		// types, and we don't want to use an n^2 loop to compare them.
		if IsClass(assignedSubtype) && IsLiteralType(assignedSubtype.(*ClassType)) {
			if IsUnion(declaredType) &&
				UnionTypeContainsType(declaredType.(*UnionType), assignedSubtype, TypeSameOptions{}, nil, 0) {
				return assignedSubtype
			}
		}

		narrowedSubtype := MapSubtypes(declaredType, func(declaredSubtype Type) Type {
			return e.narrowDeclaredSubtype(declaredSubtype, assignedSubtype)
		}, nil)

		// The original's comment: if we couldn't assign the assigned subtype any
		// of the declared subtypes, the types are incompatible. Return the
		// unnarrowed form.
		if IsNever(narrowedSubtype) {
			return assignedSubtype
		}

		return narrowedSubtype
	}, nil)

	if IsIncompleteUnknown(narrowedType) {
		return &TypeResult{Type: narrowedType, IsIncomplete: assignedTypeResult.IsIncomplete}
	}

	if IsUnknown(narrowedType) {
		return &TypeResult{
			Type:         CombineTypes([]Type{narrowedType, declaredType}, nil),
			IsIncomplete: assignedTypeResult.IsIncomplete,
		}
	}

	return &TypeResult{Type: narrowedType, IsIncomplete: assignedTypeResult.IsIncomplete}
}

// narrowDeclaredSubtype is the inner mapSubtypes callback: given one declared
// subtype and one assigned subtype, which of the two describes the name now.
// Returning nil is the original's `undefined`, which drops the subtype.
func (e *typeEvaluator) narrowDeclaredSubtype(declaredSubtype, assignedSubtype Type) Type {
	if !e.AssignType(declaredSubtype, assignedSubtype, nil, nil, AssignTypeFlagsDefault, 0) {
		return nil
	}

	// The original's comment: retain unknowns for code flow analysis convergence
	// and for unknown type reporting in strict mode.
	if IsUnknown(assignedSubtype) {
		return assignedSubtype
	}

	// The original's comment: preserve assignment narrowing when an enum literal
	// is assigned to its non-literal enum class. A single-member enum is
	// equivalent to its only literal, but the assigned value is still more
	// precise.
	if IsClassInstance(assignedSubtype) && IsClassInstance(declaredSubtype) {
		assignedClass := assignedSubtype.(*ClassType)
		declaredClass := declaredSubtype.(*ClassType)
		if _, isEnum := assignedClass.Priv.LiteralValue.(*EnumLiteral); isEnum &&
			declaredClass.Priv.LiteralValue == nil &&
			ClassTypeIsSameGenericClass(assignedClass, declaredClass, 0) {
			return assignedSubtype
		}
	}

	// The original's comment: if the two types are bidirectionally assignable,
	// they are either equivalent (in which case it doesn't matter which one we
	// choose) or one or both include gradual types (Any, etc.), in which case
	// we'll want to stick with the declared subtype.
	if !e.AssignType(assignedSubtype, declaredSubtype, nil, nil, AssignTypeFlagsDefault, 0) {
		return assignedSubtype
	}

	// The original's comment: we need to be careful with TypedDict types that
	// have narrowed fields. In this case, we want to return the assigned type.
	if IsClass(assignedSubtype) {
		assignedClass := assignedSubtype.(*ClassType)
		if assignedClass.Priv.TypedDictNarrowedEntries() != nil &&
			IsTypeSame(assignedSubtype, declaredSubtype,
				TypeSameOptions{IgnoreTypedDictNarrowEntries: true}, 0) {
			return assignedSubtype
		}
	}

	// The original's comment: we also need to be careful with callback protocols.
	if IsClassInstance(declaredSubtype) && ClassTypeIsProtocolClass(declaredSubtype.(*ClassType)) {
		if IsFunctionOrOverloaded(assignedSubtype) {
			return assignedSubtype
		}
	}

	return declaredSubtype
}

// reportPossibleUnknownAssignment corresponds to the function of the same name:
// the reportUnknownVariableType diagnostic, which fires when a name's inferred
// type is Unknown or contains one.
func (e *typeEvaluator) reportPossibleUnknownAssignment(
	diagLevel DiagnosticLevel,
	rule DiagnosticRule,
	target *parser.NameNode,
	t Type,
	errorNode parser.ExpressionNode,
	ignoreEmptyContainers bool,
) {
	// The original's comment: don't bother if the feature is disabled.
	if diagLevel == DiagnosticLevelNone {
		return
	}

	nameValue := target.D.Value

	// The original's comment: sometimes variables contain an "unbound" type if
	// they're assigned only within conditional statements. Remove this to avoid
	// confusion.
	simplifiedType := RemoveUnbound(t)

	if IsUnknown(simplifiedType) {
		e.AddDiagnostic(rule, localization.LocMessage.TypeUnknown().Format(nameValue), errorNode, nil)
		return
	}

	if !IsPartlyUnknown(simplifiedType, 0) {
		return
	}

	// The original's comment: if ignoreEmptyContainers is true, don't report the
	// problem for empty containers (lists or dictionaries). We'll report the
	// problem only if the assigned value is used later.
	if ignoreEmptyContainers && IsClassInstance(t) && t.(*ClassType).Priv.IsEmptyContainer {
		return
	}

	diagAddendum := common.NewDiagnosticAddendum()
	diagAddendum.AddMessage(localization.LocAddendum.TypeOfSymbol().Format(
		nameValue,
		e.PrintType(simplifiedType, &PrintTypeOptions{ExpandTypeAlias: true}),
	))

	e.AddDiagnostic(
		rule,
		localization.LocMessage.TypePartiallyUnknown().Format(nameValue)+diagAddendum.GetString(),
		errorNode,
		nil,
	)
}

// StripLiteralValue corresponds to the evaluator's stripLiteralValue.
func (e *typeEvaluator) StripLiteralValue(t Type) Type {
	// The original's comment: handle the not-uncommon case where the type is a
	// union that consists only of literal values.
	//
	// A union of N literals all of one kind has that kind's map sized N, so any
	// subtype will strip to the same thing and the first one stands for all.
	if union, ok := t.(*UnionType); ok && len(union.Priv.Subtypes) > 0 {
		count := len(union.Priv.Subtypes)
		literals := &union.Priv.LiteralInstances
		if (literals.LiteralStrMap != nil && literals.LiteralStrMap.Size() == count) ||
			(literals.LiteralIntMap != nil && literals.LiteralIntMap.Size() == count) ||
			(literals.LiteralEnumMap != nil && literals.LiteralEnumMap.Size() == count) {
			return e.StripLiteralValue(union.Priv.Subtypes[0])
		}
	}

	return MapSubtypes(t, func(subtype Type) Type {
		cls, ok := subtype.(*ClassType)
		if !ok {
			return subtype
		}

		if cls.Priv.LiteralValue != nil {
			cls = ClassTypeCloneWithLiteral(cls, nil)
		}

		// The original's comment: handle "LiteralString" specially.
		if ClassTypeIsBuiltInNamed(cls, "LiteralString") {
			if strClass := e.GetStrClassType(); strClass != nil {
				strInstance := ClassTypeCloneAsInstance(strClass, true)
				return CloneForCondition(strInstance, GetTypeCondition(cls))
			}
		}

		return cls
	}, nil)
}

// IsFinalVariableDeclaration corresponds to the function of the same name.
func (e *typeEvaluator) IsFinalVariableDeclaration(decl Declaration) bool {
	variableDecl, ok := decl.(*VariableDeclaration)
	return ok && variableDecl.IsFinal
}
