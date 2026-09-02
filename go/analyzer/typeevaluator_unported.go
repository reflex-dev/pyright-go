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

func (e *typeEvaluator) GetType(node parser.ExpressionNode) Type {
	e.unported("GetType")
	return UnknownTypeCreate(false)
}

func (e *typeEvaluator) GetTypeResult(node parser.ExpressionNode) *TypeResult {
	e.unported("GetTypeResult")
	return nil
}

func (e *typeEvaluator) GetTypeResultForDecorator(node *parser.DecoratorNode) *TypeResult {
	e.unported("GetTypeResultForDecorator")
	return nil
}

func (e *typeEvaluator) GetCachedType(node parser.ExpressionNode) Type {
	e.unported("GetCachedType")
	return UnknownTypeCreate(false)
}

func (e *typeEvaluator) GetTypeOfExpression(node parser.ExpressionNode, flags EvalFlags, context *InferenceContext) *TypeResult {
	e.unported("GetTypeOfExpression")
	return nil
}

func (e *typeEvaluator) GetTypeOfAnnotation(node parser.ExpressionNode, options *ExpectedTypeOptions) Type {
	e.unported("GetTypeOfAnnotation")
	return UnknownTypeCreate(false)
}

func (e *typeEvaluator) GetTypeOfClass(node *parser.ClassNode) *ClassTypeResult {
	e.unported("GetTypeOfClass")
	return nil
}

func (e *typeEvaluator) CreateSubclass(errorNode parser.ExpressionNode, type1 *ClassType, type2 *ClassType) *ClassType {
	e.unported("CreateSubclass")
	return nil
}

func (e *typeEvaluator) GetTypeOfFunction(node *parser.FunctionNode) *FunctionTypeResult {
	e.unported("GetTypeOfFunction")
	return nil
}

func (e *typeEvaluator) GetTypeOfExpressionExpectingType(node parser.ExpressionNode, options *ExpectedTypeOptions) *TypeResult {
	e.unported("GetTypeOfExpressionExpectingType")
	return nil
}

func (e *typeEvaluator) EvaluateTypeForSubnode(subnode parser.ParseNode, callback func()) *TypeResult {
	e.unported("EvaluateTypeForSubnode")
	return nil
}

func (e *typeEvaluator) EvaluateTypesForStatement(node parser.ParseNode) {
	e.unported("EvaluateTypesForStatement")
}

func (e *typeEvaluator) EvaluateTypesForMatchStatement(node *parser.MatchNode) {
	e.unported("EvaluateTypesForMatchStatement")
}

func (e *typeEvaluator) EvaluateTypesForCaseStatement(node *parser.CaseNode) {
	e.unported("EvaluateTypesForCaseStatement")
}

func (e *typeEvaluator) EvaluateTypeOfParam(node *parser.ParameterNode) {
	e.unported("EvaluateTypeOfParam")
}

func (e *typeEvaluator) CanBeTruthy(t Type) bool {
	e.unported("CanBeTruthy")
	return false
}

func (e *typeEvaluator) CanBeFalsy(t Type) bool {
	e.unported("CanBeFalsy")
	return false
}

func (e *typeEvaluator) StripLiteralValue(t Type) Type {
	e.unported("StripLiteralValue")
	return UnknownTypeCreate(false)
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

func (e *typeEvaluator) IsNodeReachable(node parser.ParseNode, sourceNode parser.ParseNode) bool {
	e.unported("IsNodeReachable")
	return false
}

func (e *typeEvaluator) IsAfterNodeReachable(node parser.ParseNode) bool {
	e.unported("IsAfterNodeReachable")
	return false
}

func (e *typeEvaluator) GetNodeReachability(node parser.ParseNode, sourceNode parser.ParseNode) Reachability {
	e.unported("GetNodeReachability")
	return ReachabilityReachable
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

func (e *typeEvaluator) ResolveAliasDeclaration(declaration Declaration, resolveLocalNames bool, options *EvaluatorResolveAliasOptions) Declaration {
	e.unported("ResolveAliasDeclaration")
	return nil
}

func (e *typeEvaluator) ResolveAliasDeclarationWithInfo(declaration Declaration, resolveLocalNames bool, options *EvaluatorResolveAliasOptions) *ResolvedAliasInfo {
	e.unported("ResolveAliasDeclarationWithInfo")
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

func (e *typeEvaluator) GetTypeOfArg(arg *Arg, inferenceContext *InferenceContext) *TypeResult {
	e.unported("GetTypeOfArg")
	return nil
}

func (e *typeEvaluator) ConvertNodeToArg(node *parser.ArgumentNode) *Arg {
	e.unported("ConvertNodeToArg")
	return nil
}

func (e *typeEvaluator) BuildTupleTypesList(entryTypeResults []*TypeResult, stripLiterals bool, convertModules bool) []*TupleTypeArg {
	e.unported("BuildTupleTypesList")
	return nil
}

func (e *typeEvaluator) MarkNamesAccessed(node parser.ParseNode, names []string) {
	e.unported("MarkNamesAccessed")
}

func (e *typeEvaluator) ExpandPromotionTypes(node parser.ParseNode, t Type) Type {
	e.unported("ExpandPromotionTypes")
	return UnknownTypeCreate(false)
}

func (e *typeEvaluator) MakeTopLevelTypeVarsConcrete(t Type, makeParamSpecsConcrete bool) Type {
	e.unported("MakeTopLevelTypeVarsConcrete")
	return UnknownTypeCreate(false)
}

func (e *typeEvaluator) MapSubtypesExpandTypeVars(t Type, options *EvaluatorMapSubtypesOptions, callback func(expandedSubtype Type, unexpandedSubtype Type) Type) Type {
	e.unported("MapSubtypesExpandTypeVars")
	return UnknownTypeCreate(false)
}

func (e *typeEvaluator) IsTypeSubsumedByOtherType(t Type, otherType Type, allowAnyToSubsume bool) bool {
	e.unported("IsTypeSubsumedByOtherType")
	return false
}

func (e *typeEvaluator) LookUpSymbolRecursive(node parser.ParseNode, name string, honorCodeFlow bool) *SymbolWithScope {
	e.unported("LookUpSymbolRecursive")
	return nil
}

func (e *typeEvaluator) GetDeclaredTypeOfSymbol(symbol *Symbol) *DeclaredSymbolTypeInfo {
	e.unported("GetDeclaredTypeOfSymbol")
	return nil
}

func (e *typeEvaluator) GetEffectiveTypeOfSymbol(symbol *Symbol) Type {
	e.unported("GetEffectiveTypeOfSymbol")
	return UnknownTypeCreate(false)
}

func (e *typeEvaluator) GetEffectiveTypeOfSymbolForUsage(symbol *Symbol, usageNode *parser.NameNode, useLastDecl bool) *EffectiveTypeResult {
	e.unported("GetEffectiveTypeOfSymbolForUsage")
	return nil
}

func (e *typeEvaluator) GetInferredTypeOfDeclaration(symbol *Symbol, decl Declaration) Type {
	e.unported("GetInferredTypeOfDeclaration")
	return UnknownTypeCreate(false)
}

func (e *typeEvaluator) GetDeclaredTypeForExpression(expression parser.ExpressionNode, usage *EvaluatorUsage) Type {
	e.unported("GetDeclaredTypeForExpression")
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

func (e *typeEvaluator) GetBuiltInType(node parser.ParseNode, name string) Type {
	e.unported("GetBuiltInType")
	return UnknownTypeCreate(false)
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

func (e *typeEvaluator) AssignType(destType Type, srcType Type, diag *common.DiagnosticAddendum, constraints *ConstraintTracker, flags AssignTypeFlags, recursionCount int) bool {
	e.unported("AssignType")
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

func (e *typeEvaluator) ValidateTypeArg(argResult *TypeResultWithNode, options *ValidateTypeArgsOptions) bool {
	e.unported("ValidateTypeArg")
	return false
}

func (e *typeEvaluator) AssignTypeToExpression(target parser.ExpressionNode, typeResult *TypeResult, srcExpr parser.ExpressionNode) {
	e.unported("AssignTypeToExpression")
}

func (e *typeEvaluator) AssignClassToSelf(destType *ClassType, srcType *ClassType, assumedVariance Variance) bool {
	e.unported("AssignClassToSelf")
	return false
}

func (e *typeEvaluator) GetBuiltInObject(node parser.ParseNode, name string, typeArgs []Type) Type {
	e.unported("GetBuiltInObject")
	return UnknownTypeCreate(false)
}

func (e *typeEvaluator) GetTypedDictClassType() *ClassType {
	e.unported("GetTypedDictClassType")
	return nil
}

func (e *typeEvaluator) GetTupleClassType() *ClassType {
	e.unported("GetTupleClassType")
	return nil
}

func (e *typeEvaluator) GetDictClassType() *ClassType {
	e.unported("GetDictClassType")
	return nil
}

func (e *typeEvaluator) GetStrClassType() *ClassType {
	e.unported("GetStrClassType")
	return nil
}

func (e *typeEvaluator) GetObjectType() Type {
	e.unported("GetObjectType")
	return UnknownTypeCreate(false)
}

func (e *typeEvaluator) GetNoneType() Type {
	e.unported("GetNoneType")
	return UnknownTypeCreate(false)
}

func (e *typeEvaluator) GetUnionClassType() Type {
	e.unported("GetUnionClassType")
	return UnknownTypeCreate(false)
}

func (e *typeEvaluator) GetTypeClassType() *ClassType {
	e.unported("GetTypeClassType")
	return nil
}

func (e *typeEvaluator) GetTypingType(node parser.ParseNode, symbolName string) Type {
	e.unported("GetTypingType")
	return UnknownTypeCreate(false)
}

func (e *typeEvaluator) GetTypeCheckerInternalsType(node parser.ParseNode, symbolName string) Type {
	e.unported("GetTypeCheckerInternalsType")
	return UnknownTypeCreate(false)
}

func (e *typeEvaluator) InferReturnTypeIfNecessary(t Type) {
	e.unported("InferReturnTypeIfNecessary")
}

func (e *typeEvaluator) InferVarianceForClass(t *ClassType) {
	e.unported("InferVarianceForClass")
}

func (e *typeEvaluator) AssignTypeArgs(destType *ClassType, srcType *ClassType, diag *common.DiagnosticAddendum, constraints *ConstraintTracker, flags AssignTypeFlags, recursionCount int) bool {
	e.unported("AssignTypeArgs")
	return false
}

func (e *typeEvaluator) ReportMissingTypeArgs(node parser.ExpressionNode, t Type, flags EvalFlags) Type {
	e.unported("ReportMissingTypeArgs")
	return UnknownTypeCreate(false)
}

func (e *typeEvaluator) IsFinalVariable(symbol *Symbol) bool {
	e.unported("IsFinalVariable")
	return false
}

func (e *typeEvaluator) IsFinalVariableDeclaration(decl Declaration) bool {
	e.unported("IsFinalVariableDeclaration")
	return false
}

func (e *typeEvaluator) IsExplicitTypeAliasDeclaration(decl Declaration) bool {
	e.unported("IsExplicitTypeAliasDeclaration")
	return false
}

func (e *typeEvaluator) AddInformation(message string, node parser.ParseNode, textRange *common.TextRange) *common.Diagnostic {
	e.unported("AddInformation")
	return nil
}

func (e *typeEvaluator) AddUnreachableCode(node parser.ParseNode, reachability Reachability, textRange common.TextRange) {
	e.unported("AddUnreachableCode")
}

func (e *typeEvaluator) AddDeprecated(message string, node parser.ParseNode) {
	e.unported("AddDeprecated")
}

func (e *typeEvaluator) AddDiagnostic(rule DiagnosticRule, message string, node parser.ParseNode, textRange *common.TextRange) *common.Diagnostic {
	e.unported("AddDiagnostic")
	return nil
}

func (e *typeEvaluator) AddDiagnosticForTextRange(fileInfo *AnalyzerFileInfo, rule DiagnosticRule, message string, textRange common.TextRange) *common.Diagnostic {
	e.unported("AddDiagnosticForTextRange")
	return nil
}

func (e *typeEvaluator) PrintType(t Type, options *PrintTypeOptions) string {
	e.unported("PrintType")
	return ""
}

func (e *typeEvaluator) PrintSrcDestTypes(srcType Type, destType Type) SrcDestTypes {
	e.unported("PrintSrcDestTypes")
	return SrcDestTypes{}
}

func (e *typeEvaluator) PrintFunctionParts(t *FunctionType, extraFlags PrintTypeFlags) ([]string, string) {
	e.unported("PrintFunctionParts")
	return nil, ""
}

func (e *typeEvaluator) UseSpeculativeMode(speculativeNode parser.ParseNode, callback func(), options *SpeculativeModeOptions) {
	e.unported("UseSpeculativeMode")
}

func (e *typeEvaluator) PrintControlFlowGraph(flowNode FlowNode, reference CodeFlowReferenceExpressionNode, callName string, logger common.ConsoleInterface) {
	e.unported("PrintControlFlowGraph")
}
