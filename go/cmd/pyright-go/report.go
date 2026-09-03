/*
 * report.go
 *
 * The two reporters, transliterated from packages/pyright/src/pyright.ts
 * (pyright 1.1.412): reportDiagnosticsAsJson, reportDiagnosticsAsText and the
 * helpers they share.
 *
 * The JSON schema carries the original's own warning -- "The schema for this
 * object is publicly documented. Do not change it" -- so the field names, the
 * omission rules and the ordering all follow it exactly. That is also what makes
 * the two implementations diffable, which is how this port is verified.
 */

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/microsoft/pyright/go/common"
)

type jsonPosition struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

type jsonRange struct {
	Start jsonPosition `json:"start"`
	End   jsonPosition `json:"end"`
}

// jsonDiagnostic corresponds to PyrightJsonDiagnostic. Range and Rule are
// pointers so they are omitted rather than emitted as null, which is what
// `undefined` does through JSON.stringify.
type jsonDiagnostic struct {
	File     string        `json:"file"`
	Severity SeverityLevel `json:"severity"`
	Message  string        `json:"message"`
	Range    *jsonRange    `json:"range,omitempty"`
	Rule     *string       `json:"rule,omitempty"`
}

type jsonSummary struct {
	FilesAnalyzed    int     `json:"filesAnalyzed"`
	ErrorCount       int     `json:"errorCount"`
	WarningCount     int     `json:"warningCount"`
	InformationCount int     `json:"informationCount"`
	TimeInSec        float64 `json:"timeInSec"`
}

// jsonResults corresponds to PyrightJsonResults.
type jsonResults struct {
	Version            string           `json:"version"`
	Time               string           `json:"time"`
	GeneralDiagnostics []jsonDiagnostic `json:"generalDiagnostics"`
	Summary            jsonSummary      `json:"summary"`
}

type diagnosticResult struct {
	errorCount       int
	warningCount     int
	informationCount int
}

// convertDiagnosticCategoryToSeverity corresponds to the function of the same
// name. The three categories that have no severity -- unused, unreachable and
// deprecated -- are the tagged hints, and both reporters drop them before they
// get here.
func convertDiagnosticCategoryToSeverity(category common.DiagnosticCategory) (SeverityLevel, bool) {
	switch category {
	case common.DiagnosticCategoryError:
		return SeverityError, true
	case common.DiagnosticCategoryWarning:
		return SeverityWarning, true
	case common.DiagnosticCategoryInformation:
		return SeverityInformation, true
	}
	return "", false
}

// isEmptyRange corresponds to the function of the same name: a range whose start
// and end are the same position carries no location.
func isEmptyRange(r common.Range) bool {
	return r.Start.Line == r.End.Line && r.Start.Character == r.End.Character
}

// convertDiagnosticToJson corresponds to the function of the same name.
func convertDiagnosticToJSON(filePath string, diag *common.Diagnostic) jsonDiagnostic {
	severity, _ := convertDiagnosticCategoryToSeverity(diag.Category)

	out := jsonDiagnostic{
		File:     filePath,
		Severity: severity,
		Message:  diag.Message,
		Rule:     diag.GetRule(),
	}

	if !isEmptyRange(diag.Range) {
		out.Range = &jsonRange{
			Start: jsonPosition{Line: diag.Range.Start.Line, Character: diag.Range.Start.Character},
			End:   jsonPosition{Line: diag.Range.End.Line, Character: diag.Range.End.Character},
		}
	}

	return out
}

// isDiagnosticIncluded corresponds to the function of the same name.
func isDiagnosticIncluded(diagSeverity SeverityLevel, minSeverityLevel SeverityLevel) bool {
	// The original's comment: errors are always included.
	if diagSeverity == SeverityError {
		return true
	}

	// The original's comment: warnings are included only if the min severity
	// level is below error.
	if diagSeverity == SeverityWarning {
		return minSeverityLevel != SeverityError
	}

	// The original's comment: informations are included only if the min severity
	// level is 'information'.
	return minSeverityLevel == SeverityInformation
}

// sortDiagnostics is `diagnostics.sort(compareDiagnostics)`: by start line, then
// start character, and stable in the ties, since Array.prototype.sort is
// required to be stable and compareDiagnostics returns 0 for them.
func sortDiagnostics(diagnostics []*common.Diagnostic) []*common.Diagnostic {
	out := append([]*common.Diagnostic{}, diagnostics...)
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i].Range.Start, out[j].Range.Start
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		return a.Character < b.Character
	})
	return out
}

// reportDiagnosticsAsJSON corresponds to reportDiagnosticsAsJson.
//
// Note which counts move: accumulateReportDiagnosticStats runs for every
// diagnostic in one of the three categories, including the ones the severity
// filter excluded from the list. So `--level error` shrinks generalDiagnostics
// but leaves warningCount reporting what was actually found.
func reportDiagnosticsAsJSON(
	fileDiags []common.FileDiagnostics,
	minSeverityLevel SeverityLevel,
	filesInProgram int,
	timeInSec float64,
) diagnosticResult {
	report := jsonResults{
		Version:            version,
		Time:               fmt.Sprint(time.Now().UnixMilli()),
		GeneralDiagnostics: []jsonDiagnostic{},
	}
	report.Summary.FilesAnalyzed = filesInProgram
	report.Summary.TimeInSec = timeInSec

	for _, fileDiag := range fileDiags {
		for _, diag := range sortDiagnostics(fileDiag.Diagnostics) {
			severity, ok := convertDiagnosticCategoryToSeverity(diag.Category)
			if !ok {
				continue
			}

			jsonDiag := convertDiagnosticToJSON(filePathOf(fileDiag.FileUri), diag)
			if isDiagnosticIncluded(severity, minSeverityLevel) {
				report.GeneralDiagnostics = append(report.GeneralDiagnostics, jsonDiag)
			}

			switch severity {
			case SeverityError:
				report.Summary.ErrorCount++
			case SeverityWarning:
				report.Summary.WarningCount++
			case SeverityInformation:
				report.Summary.InformationCount++
			}
		}
	}

	// `JSON.stringify(report, undefined, 4)`.
	encoded, err := json.MarshalIndent(report, "", "    ")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
	} else {
		fmt.Println(string(encoded))
	}

	// The original's comment: output a blank line to help tools that are
	// attempting to parse the JSON output when used in watch mode.
	fmt.Println()

	return diagnosticResult{
		errorCount:       report.Summary.ErrorCount,
		warningCount:     report.Summary.WarningCount,
		informationCount: report.Summary.InformationCount,
	}
}

// reportDiagnosticsAsText corresponds to the function of the same name.
func reportDiagnosticsAsText(
	fileDiags []common.FileDiagnostics,
	minSeverityLevel SeverityLevel,
) diagnosticResult {
	result := diagnosticResult{}

	for _, fileDiag := range fileDiags {
		// The original's comment: don't report unused code or deprecated
		// diagnostics.
		included := []*common.Diagnostic{}
		for _, diag := range fileDiag.Diagnostics {
			severity, ok := convertDiagnosticCategoryToSeverity(diag.Category)
			if !ok {
				continue
			}
			if isDiagnosticIncluded(severity, minSeverityLevel) {
				included = append(included, diag)
			}
		}

		if len(included) == 0 {
			continue
		}

		fmt.Println(userVisibleStringOf(fileDiag.FileUri))
		for _, diag := range sortDiagnostics(included) {
			jsonDiag := convertDiagnosticToJSON(filePathOf(fileDiag.FileUri), diag)
			logDiagnosticToConsole(jsonDiag, "  ")

			switch diag.Category {
			case common.DiagnosticCategoryError:
				result.errorCount++
			case common.DiagnosticCategoryWarning:
				result.warningCount++
			case common.DiagnosticCategoryInformation:
				result.informationCount++
			}
		}
	}

	fmt.Printf("%d %s, %d %s, %d %s\n",
		result.errorCount, plural(result.errorCount, "error", "errors"),
		result.warningCount, plural(result.warningCount, "warning", "warnings"),
		result.informationCount, plural(result.informationCount, "information", "informations"))

	return result
}

// logDiagnosticToConsole corresponds to the function of the same name.
//
// The original colors the line number, the severity and the rule with chalk,
// which disables itself when stdout is not a terminal. Nothing here is colored:
// the uncolored form is exactly what chalk produces when piped, which is the
// case that has to match for the output to be diffable, and adding color for the
// terminal case would be a divergence with no test behind it.
func logDiagnosticToConsole(diag jsonDiagnostic, prefix string) {
	message := prefix
	if diag.File != "" {
		message += diag.File + ":"
	}

	if diag.Range != nil {
		message += fmt.Sprintf("%d:%d - ", diag.Range.Start.Line+1, diag.Range.Start.Character+1)
	} else {
		message += " "
	}

	lines := strings.Split(diag.Message, "\n")
	message += string(diag.Severity) + ": " + lines[0]
	if len(lines) > 1 {
		message += "\n" + prefix + strings.Join(lines[1:], "\n"+prefix)
	}

	if diag.Rule != nil {
		message += " (" + *diag.Rule + ")"
	}

	fmt.Println(message)
}

func plural(count int, singular string, pluralForm string) string {
	if count == 1 {
		return singular
	}
	return pluralForm
}

// common.Uri is the two-method interface common/diagnostic.go declares to avoid
// importing common/uri, which imports it. The values are always real Uris, so
// the two richer methods the reporters need are reached by assertion, with the
// interface's own String() as the fallback that cannot happen.
type richUri interface {
	GetFilePath() string
	ToUserVisibleString() string
}

func filePathOf(u common.Uri) string {
	if rich, ok := u.(richUri); ok {
		return rich.GetFilePath()
	}
	return u.String()
}

func userVisibleStringOf(u common.Uri) string {
	if rich, ok := u.(richUri); ok {
		return rich.ToUserVisibleString()
	}
	return u.String()
}
