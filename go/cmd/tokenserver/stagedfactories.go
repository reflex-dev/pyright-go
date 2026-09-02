/*
 * stagedfactories.go
 *
 * Where the Stage D seams get filled.
 *
 * analyzer/program.go carries two of them: an evaluator factory and a checker
 * factory. The evaluator factory is now installed; the checker's is not, so the
 * program parses, binds, resolves imports, walks the import graph, evaluates
 * what the evaluator can evaluate, and reports parse and bind diagnostics --
 * but does not check types.
 *
 * Installing the evaluator is what makes it reachable from the gate and the
 * per-node differential, so from here on every one of those runs exercises it.
 * The parts that do not exist yet count themselves; see
 * analyzer/typeevaluator_unported.go.
 */

package main

import "github.com/microsoft/pyright/go/analyzer"

func installStageDFactories(program *analyzer.Program) {
	program.SetEvaluatorFactory(func(p *analyzer.Program) analyzer.TypeEvaluator {
		configOptions := p.ConfigOptions()

		// The original's _createNewEvaluator, minus the trace printer and the
		// log-tracker wrapper, which are reporting facilities.
		return analyzer.NewTypeEvaluator(p.LookUpImport(), analyzer.EvaluatorOptions{
			PrintTypeFlags:          analyzer.GetPrintTypeFlags(configOptions),
			LogCalls:                configOptions.LogTypeEvaluationTime,
			MinimumLoggingThreshold: configOptions.TypeEvaluationTimeThreshold,
			// `!!this._configOptions.evaluateUnknownImportsAsAny` -- the field
			// does not exist on ConfigOptions in 1.1.412, so the double-negation
			// of undefined is false.
			EvaluateUnknownImportsAsAny: false,
			VerifyTypeCacheEvaluatorFlags: configOptions.InternalTestMode != nil &&
				*configOptions.InternalTestMode,
		})
	})

	// The checker is still Stage D's remaining half.
}

// evaluatorUnportedCounts reports what the program's evaluator could not do, so
// the bridge can surface a work-remaining map measured over the corpus rather
// than guessed at from reading typeEvaluator.ts. Nil once nothing is unported.
func evaluatorUnportedCounts(program *analyzer.Program) map[string]int {
	reporter, ok := program.Evaluator().(analyzer.UnportedReporter)
	if !ok || reporter.UnportedTotal() == 0 {
		return nil
	}

	counts := map[string]int{}
	reporter.UnportedCounts().ForEach(func(count int, what string) {
		counts[what] = count
	})
	return counts
}
