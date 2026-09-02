/*
 * typeevaluator_unported.go
 *
 * Every TypeEvaluator method that is not ported yet.
 *
 * The evaluator is installed and reachable from this commit on, so the gate and
 * the per-node differential exercise it for real. That is only defensible if the
 * parts that do not exist say so rather than quietly answering Unknown: each
 * stub records itself, and the counts come back through the bridge.
 *
 * The result is a work-remaining map derived from the corpus rather than from
 * reading the source -- which of the 109 interface methods the sample files
 * actually reach, and how often. Reading typeEvaluator.ts tells you what exists;
 * this tells you what matters.
 *
 * Each stub answers the way an evaluator that knows nothing would: Unknown for a
 * type, nil for a result, false for a predicate. Those are not neutral answers --
 * an implementation that returns Unknown everywhere passes every test that
 * asserts no diagnostics -- which is why both scoreboards count Unknown answers
 * apart from real ones.
 *
 * This file shrinks as the port grows. When it is empty, Stage D is done.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/parser"
)

func (e *typeEvaluator) GetExpectedType(node parser.ExpressionNode) *ExpectedTypeResult {
	e.unported("GetExpectedType")
	return nil
}

func (e *typeEvaluator) VerifyRaiseExceptionType(node parser.ExpressionNode, allowNone bool) {
	e.unported("VerifyRaiseExceptionType")
}

func (e *typeEvaluator) VerifyDeleteExpression(node parser.ExpressionNode) {
	e.unported("VerifyDeleteExpression")
}

// ValidateOverloadedArgTypes is the interface method; the original exposes
// validateOverloadedArgTypes under the same name.
func (e *typeEvaluator) ValidateOverloadedArgTypes(errorNode parser.ExpressionNode, argList []*Arg, typeResult *TypeResult, constraints *ConstraintTracker, skipUnknownArgCheck bool, inferenceContext *InferenceContext) *CallResult {
	return e.validateOverloadedArgTypes(errorNode, argList, typeResult, constraints, skipUnknownArgCheck, inferenceContext)
}

func (e *typeEvaluator) ValidateInitSubclassArgs(node *parser.ClassNode, classType *ClassType) {
	e.validateInitSubclassArgs(node, classType)
}

func (e *typeEvaluator) GetDeclInfoForStringNode(node *parser.StringNode) *SymbolDeclInfo {
	e.unported("GetDeclInfoForStringNode")
	return nil
}

func (e *typeEvaluator) GetDeclInfoForNameNode(node *parser.NameNode, skipUnreachableCode *bool) *SymbolDeclInfo {
	e.unported("GetDeclInfoForNameNode")
	return nil
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

func (e *typeEvaluator) GetCallSignatureInfo(node *parser.CallNode, activeIndex int, activeOrFake bool) *CallSignatureInfo {
	e.unported("GetCallSignatureInfo")
	return nil
}

func (e *typeEvaluator) NarrowConstrainedTypeVar(node parser.ParseNode, typeVar *TypeVarType) Type {
	e.unported("NarrowConstrainedTypeVar")
	return UnknownTypeCreate(false)
}

// AssignClassToSelf is the interface method for assignClassToSelf. The original
// defaults ignoreBaseClassVariance to true and recursionCount to 0.
func (e *typeEvaluator) AssignClassToSelf(destType *ClassType, srcType *ClassType, assumedVariance Variance) bool {
	return e.assignClassToSelf(destType, srcType, assumedVariance, true, 0)
}

func (e *typeEvaluator) PrintControlFlowGraph(flowNode FlowNode, reference CodeFlowReferenceExpressionNode, callName string, logger common.ConsoleInterface) {
	e.unported("PrintControlFlowGraph")
}
