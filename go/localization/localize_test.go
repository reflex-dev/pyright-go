package localization

import "testing"

// These check the runtime half of the package against the string tables, so
// that a lookup regression shows up here rather than as a panic deep inside the
// parser.

func TestPlainMessageLookup(t *testing.T) {
	got := LocMessage.BreakOutsideLoop()
	want := `"break" can be used only within a loop`
	if got != want {
		t.Errorf("BreakOutsideLoop() = %q, want %q", got, want)
	}
}

func TestParameterizedMessageFormat(t *testing.T) {
	got := LocMessage.AbstractMethodInvocation().Format("foo")
	want := `Method "foo" cannot be called because it is abstract and unimplemented`
	if got != want {
		t.Errorf("AbstractMethodInvocation().Format = %q, want %q", got, want)
	}
}

func TestNumericParameterFormat(t *testing.T) {
	ps := LocMessage.ArgPositionalExpectedCount()
	got := ps.Format(3)
	want := "Expected 3 positional arguments"
	if got != want {
		t.Errorf("ArgPositionalExpectedCount().Format = %q, want %q", got, want)
	}
}

func TestCommentedStringValueIsUnwrapped(t *testing.T) {
	// Many entries in the JSON are { "message": ..., "comment": [...] } rather
	// than a bare string; getRawStringFromMap has to unwrap those.
	if got := LocMessage.UnaccessedClass().GetFormatString(); got == "" {
		t.Error("expected a non-empty format string for a commented entry")
	}
}

func TestAddendumNamespace(t *testing.T) {
	// Both Diagnostic and DiagnosticAddendum declare orPatternMissingName, but
	// only the addendum form takes a parameter -- the namespaces must stay
	// distinct.
	if got := LocAddendum.OrPatternMissingName().Format("x"); got == "" {
		t.Error("expected a non-empty string for LocAddendum.OrPatternMissingName")
	}
	if got := LocMessage.OrPatternMissingName(); got == "" {
		t.Error("expected a non-empty string for LocMessage.OrPatternMissingName")
	}
}

func TestEveryAccessorResolves(t *testing.T) {
	// getRawString panics on a missing key, so simply reading every message
	// verifies that every generated accessor's key exists in the default table.
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("an accessor failed to resolve: %v", r)
		}
	}()
	forEachMessage(func(_ string) {})
}

func TestGetRawStringFromMapMissingKey(t *testing.T) {
	m := StringLookupMap{"Diagnostic": map[string]any{"present": "yes"}}
	if got := GetRawStringFromMap(m, []string{"Diagnostic", "absent"}); got != "" {
		t.Errorf("expected empty string for a missing key, got %q", got)
	}
	if got := GetRawStringFromMap(m, []string{"Diagnostic", "present"}); got != "yes" {
		t.Errorf("got %q, want %q", got, "yes")
	}
}

func TestLoadStringsForLocaleFallsBackToGeneralLocale(t *testing.T) {
	// "de-de" is not in the table, but "de" is.
	m := LoadStringsForLocale("de-de")
	if len(m) == 0 {
		t.Error("expected de-de to fall back to the de string table")
	}

	// The default locale deliberately returns an empty override map.
	if got := LoadStringsForLocale("en-us"); len(got) != 0 {
		t.Errorf("expected en-us to return an empty override map, got %d entries", len(got))
	}

	// A locale with no table at all yields an empty map rather than an error.
	if got := LoadStringsForLocale("xx-yy"); len(got) != 0 {
		t.Errorf("expected an unknown locale to return an empty map, got %d entries", len(got))
	}
}
