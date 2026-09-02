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

func (e *typeEvaluator) CreateSubclass(errorNode parser.ExpressionNode, type1 *ClassType, type2 *ClassType) *ClassType {
	e.unported("CreateSubclass")
	return nil
}

func (e *typeEvaluator) EvaluateTypesForMatchStatement(node *parser.MatchNode) {
	e.unported("EvaluateTypesForMatchStatement")
}

func (e *typeEvaluator) EvaluateTypesForCaseStatement(node *parser.CaseNode) {
	e.unported("EvaluateTypesForCaseStatement")
}

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

func (e *typeEvaluator) ValidateOverloadedArgTypes(errorNode parser.ExpressionNode, argList []*Arg, typeResult *TypeResult, constraints *ConstraintTracker, skipUnknownArgCheck bool, inferenceContext *InferenceContext) *CallResult {
	e.unported("ValidateOverloadedArgTypes")
	return nil
}

func (e *typeEvaluator) ValidateInitSubclassArgs(node *parser.ClassNode, classType *ClassType) {
	e.unported("ValidateInitSubclassArgs")
}

func (e *typeEvaluator) GetDeclInfoForStringNode(node *parser.StringNode) *SymbolDeclInfo {
	e.unported("GetDeclInfoForStringNode")
	return nil
}

func (e *typeEvaluator) GetDeclInfoForNameNode(node *parser.NameNode, skipUnreachableCode *bool) *SymbolDeclInfo {
	e.unported("GetDeclInfoForNameNode")
	return nil
}

func (e *typeEvaluator) GetTypeForDeclaration(declaration Declaration) *DeclaredSymbolTypeInfo {
	e.unported("GetTypeForDeclaration")
	return nil
}

func (e *typeEvaluator) GetTypeOfIterable(typeResult *TypeResult, isAsync bool, errorNode parser.ExpressionNode, emitNotIterableError *bool) *TypeResult {
	e.unported("GetTypeOfIterable")
	return nil
}

func (e *typeEvaluator) GetTypeOfIterator(typeResult *TypeResult, isAsync bool, errorNode parser.ExpressionNode, emitNotIterableError *bool) *TypeResult {
	e.unported("GetTypeOfIterator")
	return nil
}

func (e *typeEvaluator) BuildTupleTypesList(entryTypeResults []*TypeResult, stripLiterals bool, convertModules bool) []*TupleTypeArg {
	e.unported("BuildTupleTypesList")
	return nil
}

func (e *typeEvaluator) ExpandPromotionTypes(node parser.ParseNode, t Type) Type {
	e.unported("ExpandPromotionTypes")
	return UnknownTypeCreate(false)
}

func (e *typeEvaluator) IsTypeSubsumedByOtherType(t Type, otherType Type, allowAnyToSubsume bool) bool {
	e.unported("IsTypeSubsumedByOtherType")
	return false
}

func (e *typeEvaluator) GetInferredTypeOfDeclaration(symbol *Symbol, decl Declaration) Type {
	e.unported("GetInferredTypeOfDeclaration")
	return UnknownTypeCreate(false)
}

func (e *typeEvaluator) GetInferredReturnType(t *FunctionType, callSiteInfo *CallSiteEvaluationInfo) Type {
	e.unported("GetInferredReturnType")
	return UnknownTypeCreate(false)
}

func (e *typeEvaluator) GetBestOverloadForArgs(errorNode parser.ExpressionNode, typeResult *TypeResult, argList []*Arg) *FunctionType {
	e.unported("GetBestOverloadForArgs")
	return nil
}

func (e *typeEvaluator) GetCallbackProtocolType(objType *ClassType, recursionCount int) Type {
	e.unported("GetCallbackProtocolType")
	return UnknownTypeCreate(false)
}

func (e *typeEvaluator) GetCallSignatureInfo(node *parser.CallNode, activeIndex int, activeOrFake bool) *CallSignatureInfo {
	e.unported("GetCallSignatureInfo")
	return nil
}

func (e *typeEvaluator) NarrowConstrainedTypeVar(node parser.ParseNode, typeVar *TypeVarType) Type {
	e.unported("NarrowConstrainedTypeVar")
	return UnknownTypeCreate(false)
}

func (e *typeEvaluator) IsTypeComparable(leftType Type, rightType Type, assumeIsOperator bool) bool {
	e.unported("IsTypeComparable")
	return false
}

func (e *typeEvaluator) ValidateOverrideMethod(baseMethod Type, overrideMethod Type, baseClass *ClassType, diag *common.DiagnosticAddendum, enforceParamNames *bool) bool {
	e.unported("ValidateOverrideMethod")
	return false
}

func (e *typeEvaluator) AssignClassToSelf(destType *ClassType, srcType *ClassType, assumedVariance Variance) bool {
	e.unported("AssignClassToSelf")
	return false
}

func (e *typeEvaluator) AssignTypeArgs(destType *ClassType, srcType *ClassType, diag *common.DiagnosticAddendum, constraints *ConstraintTracker, flags AssignTypeFlags, recursionCount int) bool {
	e.unported("AssignTypeArgs")
	return false
}

func (e *typeEvaluator) PrintControlFlowGraph(flowNode FlowNode, reference CodeFlowReferenceExpressionNode, callName string, logger common.ConsoleInterface) {
	e.unported("PrintControlFlowGraph")
}
