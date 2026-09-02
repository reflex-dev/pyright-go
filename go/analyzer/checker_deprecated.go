/*
 * checker_deprecated.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/checker.ts (pyright 1.1.412):
 * _reportDeprecatedUseForType with its two local closures.
 *
 * A name is deprecated for one of two unrelated reasons, and the function
 * handles both in sequence.
 *
 * The first is an explicit `@deprecated` marker on whatever the name resolves
 * to. Finding it is harder than reading a flag, because for an overloaded
 * function the answer depends on *which overload the call actually selected* --
 * `@deprecated` on one overload should fire only at call sites that pick it.
 * That is why getDeprecatedMessageForOverloadedCall walks back up to the
 * enclosing call or decorator node and reads overloadsUsedForCall, rather than
 * inspecting the type in isolation.
 *
 * The name check inside that walk is what keeps a deprecated `__init__` from
 * being reported as a deprecated *function*: the node's own text is compared
 * against the overload's name, and the two constructor dunders and `__call__`
 * are recognized separately so they produce the class-shaped message instead.
 *
 * The second reason is PEP 585: `Tuple`, `List` and friends are superseded by
 * their builtin equivalents from a given Python version onward. Those carry no
 * marker in typeshed, so the deprecatedAliases table is the only record. The
 * typingImportOnly entries are the interesting ones -- `Callable` is deprecated
 * from `typing` and current from `collections.abc`, so the diagnostic depends on
 * where the name was imported from, not on what it resolves to.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/localization"
	"github.com/microsoft/pyright/go/parser"
)

// deprecationFinding accumulates the two values the original assigns to
// closed-over locals.
type deprecationFinding struct {
	ErrorMessage      string
	DeprecatedMessage string
}

// reportDeprecatedUseForType corresponds to _reportDeprecatedUseForType.
func (c *Checker) reportDeprecatedUseForType(
	node *parser.NameNode, t Type, isImportFromTyping bool,
) {
	if t == nil {
		return
	}

	found := &deprecationFinding{}

	DoForEachSubtype(t, func(subtype Type, _ int, _ []Type) {
		if IsClass(subtype) {
			cls := subtype.(*ClassType)
			if !cls.Priv.IncludeSubclasses && cls.Shared.DeprecatedMessage != nil &&
				node.D.Value == cls.Shared.Name {
				found.DeprecatedMessage = *cls.Shared.DeprecatedMessage
				found.ErrorMessage = localization.LocMessage.DeprecatedClass().Format(cls.Shared.Name)
				return
			}

			c.deprecatedMessageForOverloadedCall(node, subtype, found)
			return
		}

		if IsFunction(subtype) {
			fn := subtype.(*FunctionType)
			if fn.Shared.DeprecatedMessage == nil {
				return
			}
			if fn.Shared.Name == "" || fn.Shared.Name == "__call__" || node.D.Value == fn.Shared.Name {
				found.DeprecatedMessage = *fn.Shared.DeprecatedMessage
				found.ErrorMessage = c.deprecatedMessageForFunction(fn)
			}
			return
		}

		if IsOverloaded(subtype) {
			// The original's comment: determine if the node is part of a call
			// expression. If so, we can determine which overload(s) were used to
			// satisfy the call expression and determine whether any of them are
			// deprecated.
			c.deprecatedMessageForOverloadedCall(node, subtype, found)

			// The original's comment: if the implementation itself is deprecated,
			// assume it is deprecated even if it's outside of a call expression.
			impl := OverloadedTypeGetImplementation(subtype.(*OverloadedType))
			if impl != nil && IsFunction(impl) {
				implFn := impl.(*FunctionType)
				if implFn.Shared.DeprecatedMessage != nil &&
					(implFn.Shared.Name == "" || node.D.Value == implFn.Shared.Name) {
					found.DeprecatedMessage = *implFn.Shared.DeprecatedMessage
					found.ErrorMessage = c.deprecatedMessageForFunction(implFn)
				}
			}
		}
	})

	if found.ErrorMessage != "" {
		c.reportDeprecatedDiagnostic(node, found.ErrorMessage, found.DeprecatedMessage)
	}

	c.reportDeprecatedTypingAlias(node, t, isImportFromTyping)
}

// deprecatedMessageForFunction corresponds to the local
// getDeprecatedMessageForFunction: a deprecated method names its class, a
// deprecated free function does not.
func (c *Checker) deprecatedMessageForFunction(functionType *FunctionType) string {
	if functionType.Shared.Declaration != nil {
		if fnNode, ok := functionType.Shared.Declaration.Node.(*parser.FunctionNode); ok {
			if containingClass := GetEnclosingClass(fnNode, true); containingClass != nil {
				name := functionType.Shared.Name
				if name == "" {
					name = "<anonymous>"
				}
				return localization.LocMessage.DeprecatedMethod().
					Format(name, containingClass.D.Name.D.Value)
			}
		}
	}

	return localization.LocMessage.DeprecatedFunction().Format(functionType.Shared.Name)
}

// deprecatedMessageForOverloadedCall corresponds to the local
// getDeprecatedMessageForOverloadedCall.
func (c *Checker) deprecatedMessageForOverloadedCall(
	node *parser.NameNode, t Type, found *deprecationFinding,
) {
	// The original's comment: determine if the node is part of a call expression.
	// If so, we can determine which overload(s) were used to satisfy the call
	// expression and determine whether any of them are deprecated.
	var callTypeResult *TypeResult

	if callNode := GetCallForName(node); callNode != nil {
		callTypeResult = c.evaluator.GetTypeResult(callNode)
	} else if decoratorNode := GetDecoratorForName(node); decoratorNode != nil {
		callTypeResult = c.evaluator.GetTypeResultForDecorator(decoratorNode)
	}

	if callTypeResult == nil || len(callTypeResult.OverloadsUsedForCall) == 0 {
		return
	}

	for _, overload := range callTypeResult.OverloadsUsedForCall {
		if overload.Shared.DeprecatedMessage == nil {
			continue
		}

		switch {
		case node.D.Value == overload.Shared.Name:
			found.DeprecatedMessage = *overload.Shared.DeprecatedMessage
			found.ErrorMessage = c.deprecatedMessageForFunction(overload)

		case IsInstantiableClass(t) &&
			(overload.Shared.Name == "__init__" || overload.Shared.Name == "__new__"):
			// A deprecated constructor is reported against the class, not as a
			// deprecated method named `__init__`.
			found.DeprecatedMessage = *overload.Shared.DeprecatedMessage
			found.ErrorMessage = localization.LocMessage.DeprecatedConstructor().
				Format(t.(*ClassType).Shared.Name)

		case IsClassInstance(t) && overload.Shared.Name == "__call__":
			found.DeprecatedMessage = *overload.Shared.DeprecatedMessage
			found.ErrorMessage = localization.LocMessage.DeprecatedFunction().Format(node.D.Value)
		}
	}
}

// reportDeprecatedTypingAlias is the original's deprecateTypingAliases block.
func (c *Checker) reportDeprecatedTypingAlias(
	node *parser.NameNode, t Type, isImportFromTyping bool,
) {
	if !c.fileInfo.DiagnosticRuleSet.DeprecateTypingAliases {
		return
	}

	deprecatedForm, ok := deprecatedAliases[node.D.Value]
	if !ok {
		deprecatedForm, ok = deprecatedSpecialForms[node.D.Value]
		if !ok {
			return
		}
	}

	// The name alone is not enough -- an unrelated local called `List` must not
	// be reported -- so the resolved type has to match the table entry, either
	// directly or through a type alias.
	matches := IsInstantiableClass(t) && t.(*ClassType).Shared.FullName == deprecatedForm.FullName
	if !matches {
		if aliasInfo := propsTypeAliasInfo(t); aliasInfo != nil && aliasInfo.Shared != nil &&
			aliasInfo.Shared.FullName == deprecatedForm.FullName {
			matches = true
		}
	}
	if !matches {
		return
	}

	if !c.fileInfo.ExecutionEnvironment.PythonVersion.IsGreaterOrEqualTo(deprecatedForm.Version) {
		return
	}

	if deprecatedForm.TypingImportOnly && !isImportFromTyping {
		return
	}

	c.reportDeprecatedDiagnostic(node,
		localization.LocMessage.DeprecatedType().Format(
			deprecatedForm.Version.String(), deprecatedForm.ReplacementText),
		"")
}
