/*
 * typeevaluator_interface.go
 *
 * TypeEvaluator interface methods whose exported form differs from the internal
 * one only in argument defaults or in unwrapping a result.
 *
 * This file began as typeevaluator_unported.go: every interface method that had
 * no implementation yet, each recording itself so the gate reported a
 * work-remaining map derived from the corpus rather than from reading the
 * source. Its header said "when it is empty, Stage D is done."
 *
 * It is empty of stubs now. What remains are genuine adapters -- the original's
 * default arguments made explicit, and one method whose interface signature
 * carries a parameter the implementation drops. They are kept together because
 * each exists for the same reason: the interface and the implementation disagree
 * about a signature, and the original resolves that with a default rather than a
 * wrapper.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/parser"
)

// ValidateOverloadedArgTypes is the interface method; the original exposes
// validateOverloadedArgTypes under the same name.
func (e *typeEvaluator) ValidateOverloadedArgTypes(errorNode parser.ExpressionNode, argList []*Arg, typeResult *TypeResult, constraints *ConstraintTracker, skipUnknownArgCheck bool, inferenceContext *InferenceContext) *CallResult {
	return e.validateOverloadedArgTypes(errorNode, argList, typeResult, constraints, skipUnknownArgCheck, inferenceContext)
}

func (e *typeEvaluator) ValidateInitSubclassArgs(node *parser.ClassNode, classType *ClassType) {
	e.validateInitSubclassArgs(node, classType)
}

// GetTypeForDeclaration is the interface method for getTypeForDeclaration.
func (e *typeEvaluator) GetTypeForDeclaration(declaration Declaration) *DeclaredSymbolTypeInfo {
	return e.getTypeForDeclaration(declaration)
}

// GetInferredTypeOfDeclaration is the interface method for
// getInferredTypeOfDeclaration.
func (e *typeEvaluator) GetInferredTypeOfDeclaration(symbol *Symbol, decl Declaration) Type {
	return e.getInferredTypeOfDeclaration(symbol, decl)
}

// GetInferredReturnType corresponds to getInferredReturnType. The interface
// declares a callSiteInfo parameter, but the original's implementation takes
// only the function type and drops it, so this does the same.
func (e *typeEvaluator) GetInferredReturnType(t *FunctionType, _ *CallSiteEvaluationInfo) Type {
	return e.getInferredReturnTypeResult(t, nil).Type
}

// GetCallbackProtocolType is the interface method for getCallbackProtocolType.
func (e *typeEvaluator) GetCallbackProtocolType(objType *ClassType, recursionCount int) Type {
	return e.getCallbackProtocolType(objType, recursionCount)
}

// AssignClassToSelf is the interface method for assignClassToSelf. The original
// defaults ignoreBaseClassVariance to true and recursionCount to 0.
func (e *typeEvaluator) AssignClassToSelf(destType *ClassType, srcType *ClassType, assumedVariance Variance) bool {
	return e.assignClassToSelf(destType, srcType, assumedVariance, true, 0)
}
