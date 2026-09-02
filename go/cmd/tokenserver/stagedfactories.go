/*
 * stagedfactories.go
 *
 * Where the Stage D seams get filled.
 *
 * analyzer/program.go carries two of them: an evaluator factory and a checker
 * factory. Both are now installed, which closes the gate's supply chain --
 * sourcefile.go runs the checker and drains the diagnostic sink inside a single
 * `if s.checkerFactory != nil` block, so until the checker existed nothing
 * walked a file to drive the evaluator and nothing collected what the evaluator
 * wrote.
 *
 * The parts that do not exist yet count themselves; see
 * analyzer/typeevaluator_unported.go and the checker's own stubs.
 */

package main

import (
	"github.com/microsoft/pyright/go/analyzer"
	"github.com/microsoft/pyright/go/parser"
)

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

	program.SetCheckerFactory(func(
		importResolver *analyzer.ImportResolver,
		evaluator analyzer.TypeEvaluator,
		parserOutput *parser.ParserOutput,
		dependentFiles []*parser.ParserOutput,
	) *analyzer.Checker {
		return analyzer.NewChecker(importResolver, evaluator, parserOutput, dependentFiles)
	})
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
