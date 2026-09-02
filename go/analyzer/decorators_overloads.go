/*
 * decorators_overloads.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/decorators.ts (pyright 1.1.412):
 * addOverloadsToFunctionType.
 *
 * How several `def`s of the same name become one type. Each is evaluated
 * independently by getTypeOfFunction; this runs on the LAST one and reaches
 * backwards through the symbol's declaration list to collect the rest.
 *
 * The backwards reach is why the loop that "evaluates all of the previous
 * function declarations" exists and why the original's comment insists on that
 * order: evaluating declaration N-1 would otherwise recurse into N-2 and so on,
 * and a module with thousands of overloads (typeshed has them) would overflow
 * the stack. Walking forwards first fills the cache so the later reads are flat.
 *
 * Only the immediately previous declaration is inspected, because it has
 * already accumulated everything before it. Three shapes matter:
 *
 *   - previous is a plain function marked @overload -> it is overload one, and
 *     this one joins it.
 *   - previous is already an OverloadedType -> copy its overloads out. But if it
 *     already had an implementation, the chain was complete, and this `def`
 *     starts a new one that replaces it entirely.
 *   - this one is not marked @overload -> it is the implementation.
 *
 * The docstring and deprecation passes at the end run from the implementation
 * outward: PEP 702 says a deprecated implementation deprecates every overload,
 * and an overload with no docstring inherits the implementation's.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/parser"
)

// AddOverloadsToFunctionType corresponds to the function of the same name.
func AddOverloadsToFunctionType(evaluator TypeEvaluator, node *parser.FunctionNode, t Type) Type {
	var functionDecl *FunctionDeclaration
	if decl := GetDeclaration(node); decl != nil {
		// The original casts unconditionally rather than testing the tag.
		functionDecl, _ = decl.(*FunctionDeclaration)
	}

	symbolWithScope := evaluator.LookUpSymbolRecursive(node, node.D.Name.D.Value, false)
	if symbolWithScope == nil {
		return t
	}

	decls := symbolWithScope.Symbol.GetDeclarations()

	// The original's comment: find this function's declaration.
	declIndex := -1
	for i, decl := range decls {
		if functionDecl != nil && decl == Declaration(functionDecl) {
			declIndex = i
			break
		}
	}

	// A declIndex of 0 means this is the first `def`, with nothing to merge; -1
	// means it was not found at all. Both fall through unchanged.
	if declIndex <= 0 {
		return t
	}

	// The original's comment: evaluate all of the previous function declarations.
	// They will be cached. We do it in this order to avoid a stack overflow due
	// to recursion if there is a large number (1000's) of overloads.
	for i := 0; i < declIndex; i++ {
		if funcDecl, ok := decls[i].(*FunctionDeclaration); ok {
			if funcNode, ok := funcDecl.Node.(*parser.FunctionNode); ok {
				evaluator.GetTypeOfFunction(funcNode)
			}
		}
	}

	var overloadedTypes []*FunctionType
	var implementation Type

	// The original's comment: look at the previous declaration's type.
	prevDecl, prevIsFunc := decls[declIndex-1].(*FunctionDeclaration)
	if prevNode, ok := declFunctionNode(prevDecl, prevIsFunc); ok {
		if prevTypeInfo := evaluator.GetTypeOfFunction(prevNode); prevTypeInfo != nil {
			switch prevType := prevTypeInfo.DecoratedType.(type) {
			case *FunctionType:
				if FunctionTypeIsOverloaded(prevType) {
					overloadedTypes = append(overloadedTypes, prevType)
				}

			case *OverloadedType:
				implementation = OverloadedTypeGetImplementation(prevType)

				// The original's comment: if the previous overloaded function
				// already had an implementation, this new function completely
				// replaces the previous one.
				if implementation != nil {
					return t
				}

				// The original's comment: if the previous declaration was itself
				// an overloaded function, copy the entries from it.
				overloadedTypes = append(overloadedTypes, OverloadedTypeGetOverloads(prevType)...)
			}
		}
	}

	if fn, ok := t.(*FunctionType); ok && FunctionTypeIsOverloaded(fn) {
		overloadedTypes = append(overloadedTypes, fn)
	} else {
		implementation = t
	}

	if len(overloadedTypes) == 1 && implementation == nil {
		return overloadedTypes[0]
	}

	if len(overloadedTypes) == 0 && implementation != nil {
		return implementation
	}

	if implFn, ok := implementation.(*FunctionType); ok {
		// The original's comment: apply the implementation's docstring to any
		// overloads that don't have their own docstrings.
		if implFn.Shared.DocString != nil {
			docString := implFn.Shared.DocString
			overloadedTypes = mapOverloads(overloadedTypes, func(overload *FunctionType) *FunctionType {
				if FunctionTypeIsOverloaded(overload) && overload.Shared.DocString == nil {
					return FunctionTypeCloneWithDocString(overload, docString)
				}
				return overload
			})
		}

		// The original's comment: PEP 702 indicates that if the implementation of
		// an overloaded function is marked deprecated, all of the overloads
		// should be treated as deprecated as well.
		if implFn.Shared.DeprecatedMessage != nil {
			message := implFn.Shared.DeprecatedMessage
			overloadedTypes = mapOverloads(overloadedTypes, func(overload *FunctionType) *FunctionType {
				if FunctionTypeIsOverloaded(overload) && overload.Shared.DeprecatedMessage == nil {
					return FunctionTypeCloneWithDeprecatedMessage(overload, message)
				}
				return overload
			})
		}
	}

	return OverloadedTypeCreate(overloadedTypes, implementation)
}

// mapOverloads stands in for the original's Array.map, which builds a new array
// rather than mutating in place. The distinction matters: these FunctionTypes
// are the cached results of getTypeOfFunction.
func mapOverloads(overloads []*FunctionType, fn func(*FunctionType) *FunctionType) []*FunctionType {
	mapped := make([]*FunctionType, len(overloads))
	for i, overload := range overloads {
		mapped[i] = fn(overload)
	}
	return mapped
}

// declFunctionNode narrows a FunctionDeclaration to its FunctionNode. The
// original reaches decl.node directly because the declaration's tag already
// guarantees the node type; Go's DeclarationBase holds a ParseNode.
func declFunctionNode(decl *FunctionDeclaration, isFunc bool) (*parser.FunctionNode, bool) {
	if !isFunc || decl == nil {
		return nil, false
	}
	node, ok := decl.Node.(*parser.FunctionNode)
	return node, ok
}
