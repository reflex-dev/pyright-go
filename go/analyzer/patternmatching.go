/*
 * patternmatching.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/patternMatching.ts (pyright 1.1.412):
 * the dispatch entry points, the as-pattern and literal-pattern narrowers, the
 * shared info structs, and the unused-pattern reporting.
 *
 * Everything in this module is built around one operation: given a subject type
 * and a pattern, produce the type the subject has *if the pattern matches*
 * (positive) or *if it does not* (negative). Both directions are needed because
 * a `match` statement narrows cumulatively -- each case sees a subject already
 * narrowed by the negative result of every preceding guard-free case, which is
 * what makes exhaustiveness checking work.
 *
 * The negative direction is not simply the complement of the positive one, and
 * most of the subtlety in this file is in the cases where it cannot be computed
 * at all. A pattern that merely *may* match leaves the subject unchanged in the
 * negative case; only a pattern that definitely matches can eliminate a subtype.
 * That distinction is why SequencePatternInfo carries both isDefiniteNoMatch and
 * isPotentialNoMatch rather than a single boolean.
 *
 * As-patterns (`case A() | B() as x`) evaluate their alternatives left to right
 * and narrow the running type as they go, so a later alternative sees only what
 * the earlier ones did not capture. That ordering is observable and is preserved.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/localization"
	"github.com/microsoft/pyright/go/parser"
)

// classPatternSpecialCases corresponds to the module-level constant of the same
// name. The original's comment: PEP 634 indicates that several built-in classes
// are handled differently when used with class pattern matching.
var classPatternSpecialCases = []string{
	"builtins.bool",
	"builtins.bytearray",
	"builtins.bytes",
	"builtins.dict",
	"builtins.float",
	"builtins.frozenset",
	"builtins.int",
	"builtins.list",
	"builtins.set",
	"builtins.str",
	"builtins.tuple",
}

// maxSequencePatternTupleExpansionSubtypes corresponds to the constant of the
// same name. The original's comment: there are cases where sequence pattern
// matching of tuples with large unions can blow up and cause hangs. This constant
// limits the total number of subtypes that can be generated during type narrowing
// for sequence patterns before the narrowed type is converted to Any. This is
// tuned empirically to provide a reasonable performance cutoff.
const maxSequencePatternTupleExpansionSubtypes = 128

// SequencePatternInfo corresponds to the interface of the same name.
type SequencePatternInfo struct {
	Subtype               Type
	IsDefiniteNoMatch     bool
	IsPotentialNoMatch    bool
	EntryTypes            []Type
	IsIndeterminateLength bool
	IsTuple               bool
	IsUnboundedTuple      bool
}

// MappingPatternInfo corresponds to the interface of the same name.
type MappingPatternInfo struct {
	Subtype                Type
	IsDefinitelyMapping    bool
	IsDefinitelyNotMapping bool
	TypedDict              *ClassType
	DictTypeArgs           *mappingDictTypeArgs
}

// mappingDictTypeArgs is the original's inline `{ key, value }` object.
type mappingDictTypeArgs struct {
	Key   Type
	Value Type
}

// PatternSubtypeNarrowingCallback corresponds to the type alias of the same
// name. It returns nil where the original returns undefined.
type PatternSubtypeNarrowingCallback func(t Type) *TypeResult

// NarrowTypeBasedOnPattern corresponds to narrowTypeBasedOnPattern.
func NarrowTypeBasedOnPattern(
	evaluator TypeEvaluator, t Type, pattern parser.ParseNode, isPositiveTest bool,
) Type {
	switch pattern.GetNodeType() {
	case parser.ParseNodeTypePatternSequence:
		return narrowTypeBasedOnSequencePattern(
			evaluator, t, pattern.(*parser.PatternSequenceNode), isPositiveTest)

	case parser.ParseNodeTypePatternLiteral:
		return narrowTypeBasedOnLiteralPattern(
			evaluator, t, pattern.(*parser.PatternLiteralNode), isPositiveTest)

	case parser.ParseNodeTypePatternClass:
		return narrowTypeBasedOnClassPattern(
			evaluator, t, pattern.(*parser.PatternClassNode), isPositiveTest)

	case parser.ParseNodeTypePatternAs:
		return narrowTypeBasedOnAsPattern(
			evaluator, t, pattern.(*parser.PatternAsNode), isPositiveTest)

	case parser.ParseNodeTypePatternMapping:
		return narrowTypeBasedOnMappingPattern(
			evaluator, t, pattern.(*parser.PatternMappingNode), isPositiveTest)

	case parser.ParseNodeTypePatternValue:
		return narrowTypeBasedOnValuePattern(
			evaluator, t, pattern.(*parser.PatternValueNode), isPositiveTest)

	case parser.ParseNodeTypePatternCapture:
		// The original's comment: a capture captures everything, so nothing
		// remains in the negative case.
		if isPositiveTest {
			return t
		}
		return NeverTypeCreateNever()

	case parser.ParseNodeTypeError:
		return t
	}

	// The original's switch is exhaustive over PatternAtomNode and has no
	// default; an unreachable node type falls out with undefined there. Returning
	// the type unchanged is the closest Go equivalent that cannot fault.
	return t
}

// CheckForUnusedPattern corresponds to checkForUnusedPattern. The original's
// comment: determines whether this pattern (or part of the pattern) in this case
// statement will never be matched.
func CheckForUnusedPattern(evaluator TypeEvaluator, pattern parser.ParseNode, subjectType Type) {
	if IsNever(subjectType) {
		reportUnnecessaryPattern(evaluator, pattern, subjectType)
		return
	}

	if asPattern, ok := pattern.(*parser.PatternAsNode); ok && len(asPattern.D.OrPatterns) > 1 {
		// The original's comment: check each of the or patterns separately.
		for _, orPattern := range asPattern.D.OrPatterns {
			subjectTypeMatch := NarrowTypeBasedOnPattern(evaluator, subjectType, orPattern, true)

			if IsNever(subjectTypeMatch) {
				reportUnnecessaryPattern(evaluator, orPattern, subjectType)
			}

			subjectType = NarrowTypeBasedOnPattern(evaluator, subjectType, orPattern, false)
		}
		return
	}

	subjectTypeMatch := NarrowTypeBasedOnPattern(evaluator, subjectType, pattern, true)

	if IsNever(subjectTypeMatch) {
		reportUnnecessaryPattern(evaluator, pattern, subjectType)
	}
}

// reportUnnecessaryPattern corresponds to the function of the same name.
func reportUnnecessaryPattern(evaluator TypeEvaluator, pattern parser.ParseNode, subjectType Type) {
	// The original's comment: if this is a simple wildcard pattern, exempt it from
	// this diagnostic.
	if asPattern, ok := pattern.(*parser.PatternAsNode); ok && len(asPattern.D.OrPatterns) == 1 {
		if capture, ok := asPattern.D.OrPatterns[0].(*parser.PatternCaptureNode); ok && capture.D.IsWildcard {
			return
		}
	}

	evaluator.AddDiagnostic(
		DiagnosticRuleReportUnnecessaryComparison,
		localization.LocMessage.PatternNeverMatches().Format(evaluator.PrintType(subjectType, nil)),
		pattern,
		nil,
	)
}

// narrowTypeBasedOnAsPattern corresponds to the function of the same name.
func narrowTypeBasedOnAsPattern(
	evaluator TypeEvaluator, t Type, pattern *parser.PatternAsNode, isPositiveTest bool,
) Type {
	remainingType := t

	if !isPositiveTest {
		for _, subpattern := range pattern.D.OrPatterns {
			remainingType = NarrowTypeBasedOnPattern(evaluator, remainingType, subpattern, false)
		}
		return remainingType
	}

	// Alternatives are evaluated left to right and the running type is narrowed
	// between them, so a later alternative sees only what the earlier ones left.
	narrowedTypes := make([]Type, 0, len(pattern.D.OrPatterns))
	for _, subpattern := range pattern.D.OrPatterns {
		narrowedSubtype := NarrowTypeBasedOnPattern(evaluator, remainingType, subpattern, true)
		remainingType = NarrowTypeBasedOnPattern(evaluator, remainingType, subpattern, false)
		narrowedTypes = append(narrowedTypes, narrowedSubtype)
	}
	return CombineTypes(narrowedTypes, nil)
}

// narrowTypeBasedOnLiteralPattern corresponds to the function of the same name.
func narrowTypeBasedOnLiteralPattern(
	evaluator TypeEvaluator, t Type, pattern *parser.PatternLiteralNode, isPositiveTest bool,
) Type {
	literalType := evaluator.GetTypeOfExpression(pattern.D.Expr, EvalFlagsNone, nil).Type

	if !isPositiveTest {
		return evaluator.MapSubtypesExpandTypeVars(t, nil,
			func(expandedSubtype Type, _ Type) Type {
				if IsClassInstance(literalType) && IsLiteralType(literalType.(*ClassType)) &&
					IsClassInstance(expandedSubtype) && IsLiteralType(expandedSubtype.(*ClassType)) &&
					(evaluator.AssignType(literalType, expandedSubtype, nil, nil, AssignTypeFlagsDefault, 0) ||
						isIntLiteralPatternEqualToBool(literalType.(*ClassType), expandedSubtype.(*ClassType))) {
					return nil
				}

				if IsNoneInstance(expandedSubtype) && IsNoneInstance(literalType) {
					return nil
				}

				// The original's comment: narrow a non-literal bool based on a
				// literal bool pattern. `x: bool` with `case True:` leaves
				// `Literal[False]` in the negative branch.
				if IsClassInstance(expandedSubtype) &&
					ClassTypeIsBuiltInNamed(expandedSubtype.(*ClassType), "bool") &&
					expandedSubtype.(*ClassType).Priv.LiteralValue == nil &&
					IsClassInstance(literalType) &&
					ClassTypeIsBuiltInNamed(literalType.(*ClassType), "bool") &&
					literalType.(*ClassType).Priv.LiteralValue != nil {
					if b, ok := literalType.(*ClassType).Priv.LiteralValue.(LiteralBool); ok {
						return ClassTypeCloneWithLiteral(literalType.(*ClassType), LiteralBool(!bool(b)))
					}
				}

				return expandedSubtype
			})
	}

	return evaluator.MapSubtypesExpandTypeVars(t, nil,
		func(expandedSubtype Type, unexpandedSubtype Type) Type {
			if IsClassInstance(literalType) && IsLiteralType(literalType.(*ClassType)) &&
				IsClassInstance(expandedSubtype) && IsLiteralType(expandedSubtype.(*ClassType)) &&
				isIntLiteralPatternEqualToBool(literalType.(*ClassType), expandedSubtype.(*ClassType)) {
				return expandedSubtype
			}

			if evaluator.AssignType(expandedSubtype, literalType, nil, nil, AssignTypeFlagsDefault, 0) {
				// The original's comment: we have to be careful here because the
				// runtime uses an equality check, but the expandedSubtype could be
				// a superclass that is not the literal type. For example, the
				// expanded subtype might be float and the literal type is
				// Literal[3]. A value of 3.0 will match this pattern, but we cannot
				// narrow it to Literal[3] in this case.
				if !IsClassInstance(literalType) || !IsLiteralType(literalType.(*ClassType)) ||
					IsTypeSame(evaluator.StripLiteralValue(expandedSubtype),
						evaluator.StripLiteralValue(literalType), TypeSameOptions{}, 0) {
					return literalType
				}

				return expandedSubtype
			}

			// The original's comment: see if the subtype is a subclass of the
			// literal's class. For example, if it's a literal str, see if the
			// subtype is subclass of str.
			if IsClassInstance(literalType) && IsClassInstance(expandedSubtype) {
				if IsLiteralType(literalType.(*ClassType)) && !IsLiteralType(expandedSubtype.(*ClassType)) {
					if evaluator.AssignType(
						ClassTypeCloneWithLiteral(literalType.(*ClassType), nil),
						expandedSubtype, nil, nil, AssignTypeFlagsDefault, 0) {
						return expandedSubtype
					}
				} else if evaluator.AssignType(literalType, expandedSubtype, nil, nil,
					AssignTypeFlagsDefault, 0) {
					return expandedSubtype
				}
			}
			return nil
		})
}

// isIntLiteralPatternEqualToBool corresponds to the function of the same name.
// The original's comment: numeric literal patterns use equality, so 0 and 1 also
// match False and True. The inverse isn't true because singleton bool patterns
// use identity.
func isIntLiteralPatternEqualToBool(patternType *ClassType, subjectType *ClassType) bool {
	if !ClassTypeIsBuiltInNamed(patternType, "int") || !ClassTypeIsBuiltInNamed(subjectType, "bool") {
		return false
	}

	subjectValue, ok := subjectType.Priv.LiteralValue.(LiteralBool)
	if !ok {
		return false
	}

	boolAsNumber := int64(0)
	if bool(subjectValue) {
		boolAsNumber = 1
	}

	// The original accepts both `number` and `bigint` for the pattern value; the
	// Go model carries integers only as LiteralInt, and a float literal is a
	// different arm, matching the original's typeof check rejecting it.
	switch patternValue := patternType.Priv.LiteralValue.(type) {
	case LiteralInt:
		return patternValue.Value != nil && patternValue.Value.IsInt64() &&
			patternValue.Value.Int64() == boolAsNumber
	case LiteralFloat:
		return float64(patternValue) == float64(boolAsNumber)
	}

	return false
}
