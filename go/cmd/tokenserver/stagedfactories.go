/*
 * stagedfactories.go
 *
 * Where the Stage D seams get filled.
 *
 * analyzer/program.go carries two of them: an evaluator factory and a checker
 * factory. With both unset the program parses, binds, resolves imports, walks
 * the import graph, detects cycles and reports parse and bind diagnostics, but
 * does not check types -- which is exactly the state Stage C left it in.
 *
 * Keeping the wiring in one function means the gate can be stood up and shown
 * to work before the evaluator exists, and lit by editing one place afterwards.
 */

package main

import "github.com/microsoft/pyright/go/analyzer"

func installStageDFactories(program *analyzer.Program) {
	// Nothing yet. The evaluator and checker land here.
	_ = program
}
