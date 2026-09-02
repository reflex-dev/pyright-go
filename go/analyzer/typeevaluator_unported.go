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

func (e *typeEvaluator) GetCachedType(node parser.ExpressionNode) Type {
	e.unported("GetCachedType")
	return UnknownTypeCreate(false)
}

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

func (e *typeEvaluator) CanBeTruthy(t Type) bool {
	e.unported("CanBeTruthy")
	return false
}

func (e *typeEvaluator) CanBeFalsy(t Type) bool {
	e.unported("CanBeFalsy")
	return false
}

func (e *typeEvaluator) RemoveTruthinessFromType(t Type) Type {
	e.unported("RemoveTruthinessFromType")
	return UnknownTypeCreate(false)
}

func (e *typeEvaluator) RemoveFalsinessFromType(t Type) Type {
	e.unported("RemoveFalsinessFromType")
	return UnknownTypeCreate(false)
}

func (e *typeEvaluator) StripTypeGuard(t Type) Type {
	e.unported("StripTypeGuard")
	return UnknownTypeCreate(false)
}

func (e *typeEvaluator) SolveAndApplyConstraints(t Type, constraints *ConstraintTracker, applyOptions *ApplyTypeVarOptions, solveOptions *SolveConstraintsOptions) Type {
	e.unported("SolveAndApplyConstraints")
	return UnknownTypeCreate(false)
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

func (e *typeEvaluator) IsAfterNodeReachable(node parser.ParseNode) bool {
	e.unported("IsAfterNodeReachable")
	return false
}

func (e *typeEvaluator) GetAfterNodeReachability(node parser.ParseNode) Reachability {
	e.unported("GetAfterNodeReachability")
	return ReachabilityReachable
}

func (e *typeEvaluator) SuppressDiagnostics(node parser.ParseNode, callback func()) {
	e.unported("SuppressDiagnostics")
}

func (e *typeEvaluator) IsSpecialFormClass(classType *ClassType, flags AssignTypeFlags) bool {
	e.unported("IsSpecialFormClass")
	return false
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

func (e *typeEvaluator) GetGetterTypeFromProperty(propertyClass *ClassType) Type {
	e.unported("GetGetterTypeFromProperty")
	return UnknownTypeCreate(false)
}

func (e *typeEvaluator) ConvertNodeToArg(node *parser.ArgumentNode) *Arg {
	e.unported("ConvertNodeToArg")
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

func (e *typeEvaluator) GetDeclaredReturnType(node *parser.FunctionNode) Type {
	e.unported("GetDeclaredReturnType")
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

func (e *typeEvaluator) GetTypeOfMember(member *ClassMember) Type {
	e.unported("GetTypeOfMember")
	return UnknownTypeCreate(false)
}

func (e *typeEvaluator) GetTypeOfBoundMember(errorNode parser.ExpressionNode, objectType *ClassType, memberName string, usage *EvaluatorUsage, diag *common.DiagnosticAddendum, flags MemberAccessFlags, selfType Type) *TypeResult {
	e.unported("GetTypeOfBoundMember")
	return nil
}

func (e *typeEvaluator) GetBoundMagicMethod(classType *ClassType, memberName string, selfType Type, errorNode parser.ExpressionNode, diag *common.DiagnosticAddendum, recursionCount int) Type {
	e.unported("GetBoundMagicMethod")
	return UnknownTypeCreate(false)
}

func (e *typeEvaluator) GetTypeOfMagicMethodCall(objType Type, methodName string, argList []*TypeResult, errorNode parser.ExpressionNode, inferenceContext *InferenceContext) *TypeResult {
	e.unported("GetTypeOfMagicMethodCall")
	return nil
}

func (e *typeEvaluator) BindFunctionToClassOrObject(baseType *ClassType, memberType Type, memberClass *ClassType, treatConstructorAsClassMethod bool, selfType Type, diag *common.DiagnosticAddendum, recursionCount int) Type {
	e.unported("BindFunctionToClassOrObject")
	return UnknownTypeCreate(false)
}

func (e *typeEvaluator) GetCallbackProtocolType(objType *ClassType, recursionCount int) Type {
	e.unported("GetCallbackProtocolType")
	return UnknownTypeCreate(false)
}

func (e *typeEvaluator) GetCallSignatureInfo(node *parser.CallNode, activeIndex int, activeOrFake bool) *CallSignatureInfo {
	e.unported("GetCallSignatureInfo")
	return nil
}

func (e *typeEvaluator) GetAbstractSymbols(classType *ClassType) []*AbstractSymbol {
	e.unported("GetAbstractSymbols")
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

func (e *typeEvaluator) ValidateCallArgs(errorNode parser.ExpressionNode, argList []*Arg, callTypeResult *TypeResult, constraints *ConstraintTracker, skipUnknownArgCheck bool, inferenceContext *InferenceContext) *CallResult {
	e.unported("ValidateCallArgs")
	return nil
}

func (e *typeEvaluator) AssignTypeToExpression(target parser.ExpressionNode, typeResult *TypeResult, srcExpr parser.ExpressionNode) {
	e.unported("AssignTypeToExpression")
}

func (e *typeEvaluator) AssignClassToSelf(destType *ClassType, srcType *ClassType, assumedVariance Variance) bool {
	e.unported("AssignClassToSelf")
	return false
}

func (e *typeEvaluator) GetTypingType(node parser.ParseNode, symbolName string) Type {
	e.unported("GetTypingType")
	return UnknownTypeCreate(false)
}

func (e *typeEvaluator) GetTypeCheckerInternalsType(node parser.ParseNode, symbolName string) Type {
	e.unported("GetTypeCheckerInternalsType")
	return UnknownTypeCreate(false)
}

func (e *typeEvaluator) AssignTypeArgs(destType *ClassType, srcType *ClassType, diag *common.DiagnosticAddendum, constraints *ConstraintTracker, flags AssignTypeFlags, recursionCount int) bool {
	e.unported("AssignTypeArgs")
	return false
}

func (e *typeEvaluator) IsFinalVariable(symbol *Symbol) bool {
	e.unported("IsFinalVariable")
	return false
}

func (e *typeEvaluator) UseSpeculativeMode(speculativeNode parser.ParseNode, callback func(), options *SpeculativeModeOptions) {
	e.unported("UseSpeculativeMode")
}

func (e *typeEvaluator) PrintControlFlowGraph(flowNode FlowNode, reference CodeFlowReferenceExpressionNode, callName string, logger common.ConsoleInterface) {
	e.unported("PrintControlFlowGraph")
}
