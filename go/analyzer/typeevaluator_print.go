/*
 * typeevaluator_print.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/typeEvaluator.ts (pyright 1.1.412):
 * printType, printObjectTypeForClass, printFunctionParts, printSrcDestTypes,
 * and the effective-return-type accessor they all pass as a callback.
 *
 * These are thin wrappers over typePrinter.ts, which was ported long before
 * the evaluator. They were nonetheless stubs, and that mattered more than their size
 * suggests: the per-node differential compares printed types, so with printType
 * returning the empty string no computed type could ever match the oracle. The
 * 74 agreements it was reporting were all `<unreachable>`, which is decided
 * before printType is reached. Porting this is what lets class creation be
 * measured rather than merely compiled.
 *
 * getEffectiveReturnTypeResult is here because it is the callback the printer
 * needs. Its declared-return-type path is ported; its inference path is a stub,
 * so a function with no return annotation still prints its return type as
 * Unknown, and says so on the frontier.
 */

package analyzer

// PrintType corresponds to printType.
func (e *typeEvaluator) PrintType(t Type, options *PrintTypeOptions) string {
	flags := e.evaluatorOptions.PrintTypeFlags

	if options != nil {
		if options.ExpandTypeAlias {
			flags |= PrintTypeFlagsExpandTypeAlias
		}
		if options.EnforcePythonSyntax {
			flags |= PrintTypeFlagsPythonSyntax
		}
		if options.UseTypingUnpack {
			flags |= PrintTypeFlagsUseTypingUnpack
		}
		if options.PrintUnknownWithAny {
			flags |= PrintTypeFlagsPrintUnknownWithAny
		}
		if options.PrintTypeVarVariance {
			flags |= PrintTypeFlagsPrintTypeVarVariance
		}
		if options.OmitTypeArgsIfUnknown {
			flags |= PrintTypeFlagsOmitTypeArgsIfUnknown
		}
		if options.UseFullyQualifiedNames {
			flags |= PrintTypeFlagsUseFullyQualifiedNames
		}
		if options.DisablePep604 {
			flags &^= PrintTypeFlagsPEP604
		}
	}

	return PrintType(t, flags, e.getEffectiveReturnType)
}

// printObjectTypeForClass corresponds to the evaluator-local wrapper of the
// same name, which shadows the typePrinter function it delegates to.
func (e *typeEvaluator) printObjectTypeForClass(t *ClassType) string {
	return PrintObjectTypeForClass(t, e.evaluatorOptions.PrintTypeFlags, e.getEffectiveReturnType)
}

// PrintFunctionParts corresponds to printFunctionParts. The original's
// extraFlags parameter is optional; zero here means the same thing, since the
// only use of the flag is a bitwise or.
func (e *typeEvaluator) PrintFunctionParts(t *FunctionType, extraFlags PrintTypeFlags) ([]string, string) {
	return PrintFunctionParts(t, e.evaluatorOptions.PrintTypeFlags|extraFlags, e.getEffectiveReturnType)
}

// PrintSrcDestTypes corresponds to printSrcDestTypes. The original's comment:
// prints two types and determines whether they need to be output in
// fully-qualified form for disambiguation.
//
// The interface exposes no options parameter, so this is the original called
// with options undefined.
func (e *typeEvaluator) PrintSrcDestTypes(srcType Type, destType Type) SrcDestTypes {
	return e.printSrcDestTypes(srcType, destType, nil)
}

// printSrcDestTypes is the original with its options parameter, which
// getTypeOfAssertType uses to request expanded type aliases. The
// fully-qualified retry keeps whatever options were passed in, as the original
// does by spreading them.
func (e *typeEvaluator) printSrcDestTypes(
	srcType Type, destType Type, options *PrintTypeOptions,
) SrcDestTypes {
	simpleSrcType := e.PrintType(srcType, options)
	simpleDestType := e.PrintType(destType, options)

	if simpleSrcType != simpleDestType {
		return SrcDestTypes{SourceType: simpleSrcType, DestType: simpleDestType}
	}

	fullOptions := &PrintTypeOptions{UseFullyQualifiedNames: true}
	if options != nil {
		copied := *options
		copied.UseFullyQualifiedNames = true
		fullOptions = &copied
	}
	fullSrcType := e.PrintType(srcType, fullOptions)
	fullDestType := e.PrintType(destType, fullOptions)

	if fullSrcType != fullDestType {
		return SrcDestTypes{SourceType: fullSrcType, DestType: fullDestType}
	}

	return SrcDestTypes{SourceType: simpleSrcType, DestType: simpleDestType}
}

/*
 * The effective return type, which is what the printer calls back into.
 */

// getEffectiveReturnType corresponds to the function of the same name.
func (e *typeEvaluator) getEffectiveReturnType(t *FunctionType) Type {
	returnType, _ := e.getEffectiveReturnTypeInfo(t, nil)
	return returnType
}

// GetEffectiveReturnType is the interface form.
func (e *typeEvaluator) GetEffectiveReturnType(t *FunctionType) Type {
	return e.getEffectiveReturnType(t)
}

// EffectiveReturnTypeOptions corresponds to the interface of the same name.
type EffectiveReturnTypeOptions struct {
	CallSiteInfo *CallSiteEvaluationInfo
}

// getEffectiveReturnTypeResult corresponds to the function of the same name.
// The original's comment: returns the return type of the function. If the type
// is explicitly provided in a type annotation, that type is returned. If not, an
// attempt is made to infer the return type.
func (e *typeEvaluator) getEffectiveReturnTypeResult(
	t *FunctionType,
	options *EffectiveReturnTypeOptions,
) *TypeResult {
	specializedReturnType := FunctionTypeGetEffectiveReturnType(t, false)
	if specializedReturnType != nil && !IsUnknown(specializedReturnType) {
		return &TypeResult{Type: specializedReturnType}
	}

	var callSiteInfo *CallSiteEvaluationInfo
	if options != nil {
		callSiteInfo = options.CallSiteInfo
	}
	return e.getInferredReturnTypeResult(t, callSiteInfo)
}

// getEffectiveReturnTypeInfo is getEffectiveReturnTypeResult for callers that
// only need the type and the incompleteness bit; the declared-return common
// case then allocates no result object.
func (e *typeEvaluator) getEffectiveReturnTypeInfo(
	t *FunctionType,
	callSiteInfo *CallSiteEvaluationInfo,
) (Type, bool) {
	specializedReturnType := FunctionTypeGetEffectiveReturnType(t, false)
	if specializedReturnType != nil && !IsUnknown(specializedReturnType) {
		return specializedReturnType, false
	}

	result := e.getInferredReturnTypeResult(t, callSiteInfo)
	return result.Type, result.IsIncomplete
}

// inferReturnTypeIfNecessary corresponds to the function of the same name.
func (e *typeEvaluator) InferReturnTypeIfNecessary(t Type) {
	if IsFunction(t) {
		e.getEffectiveReturnType(t.(*FunctionType))
		return
	}

	if IsOverloaded(t) {
		overloaded := t.(*OverloadedType)
		for _, overload := range OverloadedTypeGetOverloads(overloaded) {
			e.getEffectiveReturnType(overload)
		}

		if impl := OverloadedTypeGetImplementation(overloaded); impl != nil && IsFunction(impl) {
			e.getEffectiveReturnType(impl.(*FunctionType))
		}
	}
}
