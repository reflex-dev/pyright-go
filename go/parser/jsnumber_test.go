package parser

import (
	"math"
	"testing"
)

// TestJSNumberToString pins the ECMA-262 Number::toString formatting that
// createKeyForReference and printExpression interpolate into user-visible
// strings. The expectations come from running the same values through node.
func TestJSNumberToString(t *testing.T) {
	cases := []struct {
		value float64
		want  string
	}{
		{0, "0"},
		{math.Copysign(0, -1), "0"},
		{1, "1"},
		{-1, "-1"},
		{42, "42"},
		{1.5, "1.5"},
		{0.1, "0.1"},
		{1e20, "100000000000000000000"},
		{1e21, "1e+21"},
		{1.5e21, "1.5e+21"},
		{1e-6, "0.000001"},
		{1e-7, "1e-7"},
		{1.5e-7, "1.5e-7"},
		{0.000001234, "0.000001234"},
		{123456789012345680000, "123456789012345680000"},
		{math.Inf(1), "Infinity"},
		{math.Inf(-1), "-Infinity"},
		{math.NaN(), "NaN"},
	}

	for _, c := range cases {
		if got := jsFloatToString(c.value); got != c.want {
			t.Errorf("jsFloatToString(%v) = %q, want %q", c.value, got, c.want)
		}
	}
}
