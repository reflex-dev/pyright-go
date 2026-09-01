package analyzer

import "testing"

// TestDiagnosticRuleFieldMapsMatchRuleLists pins the substitution commentUtils
// makes: the original tests membership with
// `getDiagLevelDiagnosticRules().find(r => r === rule)` and
// `getBooleanDiagnosticRules().find(...)`, while the Go version looks the rule
// up in the generated field maps. That is only equivalent if the maps hold
// exactly the rules those lists do -- bool rules including the two
// non-overridable ones, which getBooleanDiagnosticRules() omits by default.
//
// Both sides are generated from configOptions.ts, so this is really a check
// that the generator's two extraction paths agree.
func TestDiagnosticRuleFieldMapsMatchRuleLists(t *testing.T) {
	check := func(name string, list []DiagnosticRule, keys map[DiagnosticRule]struct{}) {
		seen := map[DiagnosticRule]bool{}
		for _, rule := range list {
			if _, ok := keys[rule]; !ok {
				t.Errorf("%s: rule %q is in the list but has no field accessor", name, rule)
			}
			seen[rule] = true
		}
		for rule := range keys {
			if !seen[rule] {
				t.Errorf("%s: rule %q has a field accessor but is not in the list", name, rule)
			}
		}
	}

	boolKeys := map[DiagnosticRule]struct{}{}
	for rule := range diagnosticRuleBoolFields {
		boolKeys[rule] = struct{}{}
	}
	levelKeys := map[DiagnosticRule]struct{}{}
	for rule := range diagnosticRuleLevelFields {
		levelKeys[rule] = struct{}{}
	}

	check("boolean rules", GetBooleanDiagnosticRules(true), boolKeys)
	check("diagnostic level rules", GetDiagLevelDiagnosticRules(), levelKeys)

	// The default list drops exactly the two rules a pyright comment must not
	// be able to set.
	nonOverridable := map[DiagnosticRule]bool{}
	for rule := range boolKeys {
		nonOverridable[rule] = true
	}
	for _, rule := range GetBooleanDiagnosticRules(false) {
		delete(nonOverridable, rule)
	}
	want := map[DiagnosticRule]bool{
		DiagnosticRuleEnableTypeIgnoreComments:   true,
		DiagnosticRuleEnableReachabilityAnalysis: true,
	}
	if len(nonOverridable) != len(want) {
		t.Errorf("non-overridable bool rules = %v, want %v", nonOverridable, want)
	}
	for rule := range want {
		if !nonOverridable[rule] {
			t.Errorf("expected %q to be non-overridable", rule)
		}
	}
}

// TestDiagnosticRuleSetPresetsDiffer is a smoke test that the four generated
// presets are distinct and that the accessors reach real fields.
func TestDiagnosticRuleSetPresetsDiffer(t *testing.T) {
	off := GetOffDiagnosticRuleSet()
	strict := GetStrictDiagnosticRuleSet()

	if off.ReportMissingImports != DiagnosticLevelWarning {
		t.Errorf("off.reportMissingImports = %q, want warning", off.ReportMissingImports)
	}
	if strict.ReportMissingImports != DiagnosticLevelError {
		t.Errorf("strict.reportMissingImports = %q, want error", strict.ReportMissingImports)
	}

	field := diagnosticRuleLevelFields[DiagnosticRuleReportMissingImports]
	if *field(off) != off.ReportMissingImports {
		t.Error("the reportMissingImports accessor does not reach the field it names")
	}

	// Applying the strict rules to a copy of the "off" set must not touch the
	// one rule strict mode is not allowed to override.
	ruleSet := CloneDiagnosticRuleSet(off)
	applyStrictRules(ruleSet)
	if ruleSet.ReportMissingModuleSource != off.ReportMissingModuleSource {
		t.Errorf("strict mode overrode reportMissingModuleSource: %q", ruleSet.ReportMissingModuleSource)
	}
	if ruleSet.ReportMissingImports != DiagnosticLevelError {
		t.Errorf("strict mode did not raise reportMissingImports: %q", ruleSet.ReportMissingImports)
	}
}
