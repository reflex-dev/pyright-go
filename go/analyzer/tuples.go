/*
 * tuples.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/tuples.ts (pyright 1.1.412).
 *
 * Only makeTupleObject so far; the rest of the module arrives with the tuple
 * expression and index handling.
 */

package analyzer

// MakeTupleObject corresponds to makeTupleObject. The original's isUnpacked
// parameter defaults to false.
func MakeTupleObject(evaluator TypeEvaluator, typeArgs []*TupleTypeArg, isUnpacked bool) Type {
	tupleClass := evaluator.GetTupleClassType()
	if tupleClass != nil && IsInstantiableClass(tupleClass) {
		return ConvertToInstance(SpecializeTupleClass(tupleClass, typeArgs, true, isUnpacked), true)
	}

	return UnknownTypeCreate(false)
}
