/*
 * checker_comparison.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/checker.ts (pyright 1.1.412):
 * visitBinaryOperation's comparison half, _validateContainmentTypes and
 * _validateComparisonTypes.
 *
 * These report comparisons whose outcome is already known -- `x == y` where the
 * two types can never be equal, `a in b` where `a` can never be an element of
 * `b`. Both are skipped inside an assert, where a redundant comparison is the
 * whole point of the statement.
 *
 * _validateComparisonTypes splits on whether both sides are literals. Two
 * literal unions can be decided exactly, by asking whether any member of one is
 * assignable to the other; anything else falls back to isTypeComparable, which
 * asks the weaker question of whether the types could *possibly* overlap.
 *
 * The enum substitution before that split is the subtle part. An IntEnum member
 * compares equal to a plain int at runtime, and a StrEnum member to a str, so
 * comparing the enum types directly would call a legitimate comparison
 * impossible. replaceEnumTypeWithLiteralValue rewrites such a member to the
 * value it carries -- and rewrites a whole enum class to the union of its
 * members' values -- but only when the enum derives from int, str or bytes,
 * because only those have the runtime equality that makes the substitution
 * sound.
 *
 * The chained-comparison adjustment at the top handles `a < b < c`, which parses
 * as `a < (b < c)`: the right operand for comparison purposes is `b`, not the
 * inner comparison.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/localization"
	"github.com/microsoft/pyright/go/parser"
)

// checkBinaryOperation corresponds to the comparison half of
// visitBinaryOperation.
func (c *Checker) checkBinaryOperation(node *parser.BinaryOperationNode) {
	switch node.D.Operator {
	case parser.OperatorTypeEquals, parser.OperatorTypeNotEquals,
		parser.OperatorTypeIs, parser.OperatorTypeIsNot:
		// The original's comment: don't apply this rule if it's within an assert.
		if !IsWithinAssertExpression(node) {
			c.validateComparisonTypes(node)
		}

	case parser.OperatorTypeIn, parser.OperatorTypeNotIn:
		// The original's comment: don't apply this rule if it's within an assert.
		if !IsWithinAssertExpression(node) {
			c.validateContainmentTypes(node)
		}
	}

	typeResult := c.evaluator.GetTypeResult(node)
	c.reportDeprecatedUseForOperation(node.D.LeftExpr, typeResult)
}

// validateContainmentTypes corresponds to _validateContainmentTypes.
func (c *Checker) validateContainmentTypes(node *parser.BinaryOperationNode) {
	leftType := c.evaluator.GetType(node.D.LeftExpr)
	containerType := c.evaluator.GetType(node.D.RightExpr)

	if leftType == nil || containerType == nil {
		return
	}

	if IsNever(leftType) || IsNever(containerType) {
		return
	}

	// The original's comment: use the common narrowing logic for containment.
	elementType := GetElementTypeForContainerNarrowing(containerType)
	if elementType == nil {
		return
	}

	narrowedType := NarrowTypeForContainerElementType(c.evaluator, leftType,
		c.evaluator.MakeTopLevelTypeVarsConcrete(elementType, false))

	if !IsNever(narrowedType) {
		return
	}

	expandOptions := &PrintTypeOptions{ExpandTypeAlias: true}
	message := localization.LocMessage.ContainmentAlwaysTrue().Format(
		c.evaluator.PrintType(leftType, expandOptions),
		c.evaluator.PrintType(elementType, expandOptions))
	if node.D.Operator == parser.OperatorTypeIn {
		message = localization.LocMessage.ContainmentAlwaysFalse().Format(
			c.evaluator.PrintType(leftType, expandOptions),
			c.evaluator.PrintType(elementType, expandOptions))
	}

	c.evaluator.AddDiagnostic(DiagnosticRuleReportUnnecessaryContains, message, node, nil)
}

// validateComparisonTypes corresponds to _validateComparisonTypes. The
// original's comment: determines whether the types of the two operands for an ==
// or != operation have overlapping types.
func (c *Checker) validateComparisonTypes(node *parser.BinaryOperationNode) {
	rightExpression := node.D.RightExpr
	assumeIsOperator := node.D.Operator == parser.OperatorTypeIs ||
		node.D.Operator == parser.OperatorTypeIsNot

	// The original's comment: check for chained comparisons. `a < b < c` parses
	// as `a < (b < c)`, so the operand to compare against is the inner left.
	if inner, ok := rightExpression.(*parser.BinaryOperationNode); ok &&
		!inner.D.HasParens && OperatorSupportsChaining(inner.D.Operator) {
		rightExpression = inner.D.LeftExpr
	}

	leftType := c.evaluator.GetType(node.D.LeftExpr)
	rightType := c.evaluator.GetType(rightExpression)

	if leftType == nil || rightType == nil {
		return
	}

	if IsNever(leftType) || IsNever(rightType) {
		return
	}

	if IsModule(leftType) || IsModule(rightType) {
		return
	}

	// The original's comment: handle enum literals that are assignable to another
	// (non-Enum) literal. This can happen for IntEnum and StrEnum members.
	leftType = c.replaceEnumTypeWithLiteralValue(leftType)
	rightType = c.replaceEnumTypeWithLiteralValue(rightType)

	expandOptions := &PrintTypeOptions{ExpandTypeAlias: true}
	report := func() {
		message := localization.LocMessage.ComparisonAlwaysTrue().Format(
			c.evaluator.PrintType(leftType, expandOptions),
			c.evaluator.PrintType(rightType, expandOptions))
		if node.D.Operator == parser.OperatorTypeEquals || node.D.Operator == parser.OperatorTypeIs {
			message = localization.LocMessage.ComparisonAlwaysFalse().Format(
				c.evaluator.PrintType(leftType, expandOptions),
				c.evaluator.PrintType(rightType, expandOptions))
		}
		c.evaluator.AddDiagnostic(DiagnosticRuleReportUnnecessaryComparison, message, node, nil)
	}

	// The original's comment: check for the special case where the LHS and RHS
	// are both literals.
	if IsLiteralTypeOrUnion(rightType, false) && IsLiteralTypeOrUnion(leftType, false) {
		// The original passes only three arguments; the two alias lists are
		// optional there and absent here. `known` being true is the original's
		// `!== undefined`, meaning the comparison is statically decidable and
		// therefore already reported elsewhere.
		_, known := EvaluateStaticBoolExpression(node,
			c.fileInfo.ExecutionEnvironment, c.fileInfo.DefinedConstants, nil, nil)
		if known {
			return
		}

		isPossiblyTrue := false

		DoForEachSubtype(leftType, func(leftSubtype Type, _ int, _ []Type) {
			if c.evaluator.AssignType(rightType, leftSubtype, nil, nil, AssignTypeFlagsDefault, 0) {
				isPossiblyTrue = true
			}
		})

		DoForEachSubtype(rightType, func(rightSubtype Type, _ int, _ []Type) {
			if c.evaluator.AssignType(leftType, rightSubtype, nil, nil, AssignTypeFlagsDefault, 0) {
				isPossiblyTrue = true
			}
		})

		if !isPossiblyTrue {
			report()
		}
		return
	}

	isComparable := false

	c.evaluator.MapSubtypesExpandTypeVars(leftType, nil,
		func(leftSubtype Type, _ Type) Type {
			if isComparable {
				return nil
			}

			c.evaluator.MapSubtypesExpandTypeVars(rightType, nil,
				func(rightSubtype Type, _ Type) Type {
					if isComparable {
						return nil
					}

					if c.evaluator.IsTypeComparable(leftSubtype, rightSubtype, assumeIsOperator) {
						isComparable = true
					}

					return rightSubtype
				})

			return leftSubtype
		})

	if !isComparable {
		report()
	}
}

// replaceEnumTypeWithLiteralValue corresponds to the local closure of the same
// name: rewrite an int/str/bytes-derived enum to the values its members carry,
// because those compare equal to plain ints and strings at runtime.
func (c *Checker) replaceEnumTypeWithLiteralValue(t Type) Type {
	return MapSubtypes(t, func(subtype Type) Type {
		if !IsClassInstance(subtype) || !ClassTypeIsEnumClass(subtype.(*ClassType)) {
			return subtype
		}

		cls := subtype.(*ClassType)

		derivesFromValueType := false
		for _, base := range cls.Shared.Mro {
			if IsClass(base) && ClassTypeIsBuiltInNamed(base.(*ClassType), "int", "str", "bytes") {
				derivesFromValueType = true
				break
			}
		}
		if !derivesFromValueType {
			return subtype
		}

		// The original's comment: if this is an enum literal, replace it with its
		// literal value.
		if enumLiteral, ok := cls.Priv.LiteralValue.(*EnumLiteral); ok {
			return enumLiteral.ItemType
		}

		// The original's comment: if this is an enum class, replace it with the
		// type of its members.
		literalValues := EnumerateLiteralsForType(c.evaluator, cls)
		if len(literalValues) == 0 {
			return subtype
		}

		itemTypes := make([]Type, 0, len(literalValues))
		for _, literalClass := range literalValues {
			enumLiteral, ok := literalClass.Priv.LiteralValue.(*EnumLiteral)
			if !ok {
				// The original asserts here. Every member enumerated for an enum
				// class carries an EnumLiteral.
				return subtype
			}
			itemTypes = append(itemTypes, enumLiteral.ItemType)
		}

		return CombineTypes(itemTypes, nil)
	}, nil)
}
