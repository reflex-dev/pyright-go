/*
 * protocols_overlap.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/protocols.ts (pyright 1.1.412):
 * isMethodOnlyProtocol and isProtocolUnsafeOverlap.
 *
 * Both exist to serve isinstance/issubclass checks, and both are about a gap
 * between what the type system knows and what happens at runtime.
 *
 * A runtime `isinstance(x, SomeProtocol)` check compares *names only* -- it asks
 * whether the object has attributes with the right names, never whether they
 * have the right types. isProtocolUnsafeOverlap identifies the case where that
 * difference bites: a class that the type system says does not satisfy the
 * protocol, but that has every name the protocol requires, so the runtime check
 * passes and narrowing to the protocol is unsound. Note the direction of the
 * loop -- a single missing name is enough to make the overlap safe, so the flag
 * starts true and any absence clears it.
 *
 * isMethodOnlyProtocol backs PEP 544's rule that issubclass cannot be used with
 * a protocol that declares data members, because a class object has no instance
 * attributes to check.
 */

package analyzer

// IsMethodOnlyProtocol corresponds to isMethodOnlyProtocol: a protocol all of
// whose members are functions, in this class and in every protocol base class.
func IsMethodOnlyProtocol(classType *ClassType) bool {
	if !ClassTypeIsProtocolClass(classType) {
		return false
	}

	// The original's comment: first check for data members in any protocol base
	// classes.
	for _, baseClass := range classType.Shared.BaseClasses {
		if IsClass(baseClass) && ClassTypeIsProtocolClass(baseClass.(*ClassType)) &&
			!IsMethodOnlyProtocol(baseClass.(*ClassType)) {
			return false
		}
	}

	symbolTable := ClassTypeGetSymbolTable(classType)
	for _, name := range symbolTable.Keys() {
		symbol, _ := symbolTable.Get(name)
		if symbol.IsIgnoredForProtocolMatch() {
			continue
		}

		for _, decl := range symbol.GetDeclarations() {
			if _, isFunc := decl.(*FunctionDeclaration); !isFunc {
				return false
			}
		}
	}

	return true
}

// IsProtocolUnsafeOverlap corresponds to isProtocolUnsafeOverlap.
func IsProtocolUnsafeOverlap(evaluator TypeEvaluator, protocol *ClassType, classType *ClassType) bool {
	// The original's comment: if the classType is compatible with the protocol,
	// then it doesn't overlap unsafely.
	if evaluator.AssignType(protocol, classType, nil, nil, AssignTypeFlagsDefault, 0) {
		return false
	}

	isUnsafeOverlap := true

	for _, mroClass := range protocol.Shared.Mro {
		if !isUnsafeOverlap || !IsInstantiableClass(mroClass) ||
			!ClassTypeIsProtocolClass(mroClass.(*ClassType)) {
			continue
		}

		symbolTable := ClassTypeGetSymbolTable(mroClass.(*ClassType))
		for _, name := range symbolTable.Keys() {
			destSymbol, _ := symbolTable.Get(name)
			if !isUnsafeOverlap || !destSymbol.IsClassMember() ||
				destSymbol.IsIgnoredForProtocolMatch() {
				continue
			}

			// The original's comment: does the classType have a member with the
			// same name?
			if LookUpClassMember(classType, name, MemberAccessFlagsDefault, nil) == nil {
				isUnsafeOverlap = false
			}
		}
	}

	return isUnsafeOverlap
}
