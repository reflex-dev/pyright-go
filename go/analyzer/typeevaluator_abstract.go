/*
 * typeevaluator_abstract.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/typeEvaluator.ts (pyright 1.1.412):
 * getAbstractSymbolInfo, getAbstractSymbols.
 *
 * Which methods of a class are abstract and unimplemented -- the check behind
 * "cannot instantiate abstract class".
 *
 * In an ABC the rule is simple: a method is abstract if it carries
 * @abstractmethod. In a PROTOCOL it is not, because a protocol's members are
 * unimplemented by nature. There the question becomes whether the member has a
 * body at all:
 *
 *   - A variable declared in a protocol with no assignment anywhere is
 *     unimplemented.
 *   - A method whose body is empty, or which does nothing but raise
 *     NotImplementedError, is unimplemented.
 *   - An overloaded method in a STUB file has no implementation to look at, so
 *     the only way to mark it abstract is the decorator on the first overload;
 *     absent that, it is assumed implemented.
 *
 * getAbstractSymbols walks the MRO in REVERSE so that a derived class overrides
 * what its bases said. That direction is what lets a concrete override delete an
 * inherited abstract entry rather than merely shadowing it -- the map is keyed by
 * name, and a non-abstract redefinition removes the key.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/parser"
)

// getAbstractSymbolInfo corresponds to the function of the same name. It returns
// nil where the original returns undefined, meaning "not abstract".
//
// Its comment: determines whether a symbol is abstract. In an ABC class, this
// means a function is specifically decorated with @abstractmethod. In a protocol
// class, the rules are more complicated and depend on whether the method is
// defined in a stub file.
func (e *typeEvaluator) getAbstractSymbolInfo(classType *ClassType, symbolName string) *AbstractSymbol {
	isProtocolClass := ClassTypeIsProtocolClass(classType)

	symbol, found := ClassTypeGetSymbolTable(classType).Get(symbolName)
	if !found {
		return nil
	}

	// The original's comment: ignore instance variables. Also, ignore named tuple
	// members, which are modeled in pyright as instance variables, but their
	// runtime implementation uses a descriptor object.
	if !symbol.IsClassMember() && !symbol.IsNamedTupleMemberMember() {
		return nil
	}

	lastDecl := GetLastTypedDeclarationForSymbol(symbol)
	if lastDecl == nil {
		return nil
	}

	// The original's comment: handle protocol variables specially.
	if isProtocolClass {
		if _, isVar := lastDecl.(*VariableDeclaration); isVar {
			// The original's comment: if none of the declarations involve
			// assignments, assume it's not implemented in the protocol.
			hasAssignment := false
			for _, decl := range symbol.GetDeclarations() {
				if varDecl, ok := decl.(*VariableDeclaration); ok && varDecl.InferredTypeSource != nil {
					hasAssignment = true
					break
				}
			}
			if !hasAssignment {
				return &AbstractSymbol{
					Symbol:            symbol,
					SymbolName:        symbolName,
					ClassType:         classType,
					HasImplementation: false,
				}
			}
		}
	}

	lastFunctionDecl, isFunctionDecl := lastDecl.(*FunctionDeclaration)
	if !isFunctionDecl {
		return nil
	}

	lastFunctionNode, ok := lastFunctionDecl.Node.(*parser.FunctionNode)
	if !ok {
		return nil
	}

	isAbstract := false
	lastFunctionInfo := GetFunctionInfoFromDecorators(e, lastFunctionNode, true)
	if (lastFunctionInfo.Flags & FunctionTypeFlagsAbstractMethod) != 0 {
		isAbstract = true
	}

	isStubFile := GetFileInfo(lastFunctionNode).IsStubFile

	// The original's comment: in an overloaded method, the first overload can also
	// be marked abstract. In stub files, there is no implementation, so this is the
	// only way to mark an overloaded method as abstract.
	decls := symbol.GetDeclarations()
	if len(decls) > 0 {
		firstDecl := decls[0]

		if firstDecl != Declaration(lastFunctionDecl) {
			if firstFunctionDecl, ok := firstDecl.(*FunctionDeclaration); ok {
				firstFunctionNode, isFn := firstFunctionDecl.Node.(*parser.FunctionNode)
				if !isFn {
					return nil
				}
				firstFunctionInfo := GetFunctionInfoFromDecorators(e, firstFunctionNode, true)
				if (firstFunctionInfo.Flags & FunctionTypeFlagsAbstractMethod) != 0 {
					isAbstract = true
				}

				// The original's comment: if there's no implementation, assume it's
				// unimplemented.
				if isProtocolClass && (lastFunctionInfo.Flags&FunctionTypeFlagsOverloaded) != 0 {
					// The original's comment: if this is a protocol class method defined
					// in a stub file and it's not marked abstract, assume it's not
					// abstract and implemented.
					//
					// The outer `isProtocolClass` is already known here; the original
					// tests it again.
					if isProtocolClass && !isAbstract && isStubFile {
						return nil
					}

					return &AbstractSymbol{
						Symbol:            symbol,
						SymbolName:        symbolName,
						ClassType:         classType,
						HasImplementation: false,
					}
				}
			}
		}
	}

	// The original's comment: in a non-protocol class, if the method isn't
	// explicitly marked abstract, then it's not abstract.
	if !isProtocolClass && !isAbstract {
		return nil
	}

	hasImplementation := !IsSuiteEmpty(lastFunctionNode.D.Suite) &&
		!e.methodAlwaysRaisesNotImplemented(lastFunctionDecl)

	return &AbstractSymbol{
		Symbol:            symbol,
		SymbolName:        symbolName,
		ClassType:         classType,
		HasImplementation: hasImplementation,
	}
}

// GetAbstractSymbols corresponds to getAbstractSymbols.
//
// The MRO is walked in REVERSE so a derived class overrides what its bases said.
// That direction is what lets a concrete override DELETE an inherited abstract
// entry rather than merely shadow it.
func (e *typeEvaluator) GetAbstractSymbols(classType *ClassType) []*AbstractSymbol {
	symbolTable := common.NewOrderedMap[string, *AbstractSymbol]()

	for _, mroClass := range ClassTypeGetReverseMro(classType) {
		mroClassType, ok := mroClass.(*ClassType)
		if !ok || !IsInstantiableClass(mroClass) {
			continue
		}

		// The original's comment: see if this class is introducing a new abstract
		// symbol that has not been introduced previously or if it is overriding an
		// abstract symbol with a non-abstract one.
		ClassTypeGetSymbolTable(mroClassType).ForEach(func(_ *Symbol, symbolName string) {
			abstractSymbolInfo := e.getAbstractSymbolInfo(mroClassType, symbolName)

			if abstractSymbolInfo != nil {
				symbolTable.Set(symbolName, abstractSymbolInfo)
			} else {
				symbolTable.Delete(symbolName)
			}
		})
	}

	// The original's comment: create a final list of symbols that are abstract.
	return symbolTable.Values()
}
