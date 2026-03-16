package main

import (
	"math"
	"testing"
)

func TestEvalExpression(t *testing.T) {
	tests := []struct {
		name string
		expr string
		want float64
	}{
		{name: "addition", expr: "1+2", want: 3},
		{name: "precedence", expr: "2+3*4", want: 14},
		{name: "parentheses", expr: "(2+3)*4", want: 20},
		{name: "whitespace", expr: " 10 / 4 ", want: 2.5},
		{name: "unary minus", expr: "-3 + 5", want: 2},
		{name: "nested", expr: "(1+(2*3)-(4/2))", want: 5},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := EvalExpression(tc.expr)
			if err != nil {
				t.Fatalf("EvalExpression(%q) error: %v", tc.expr, err)
			}
			if math.Abs(got-tc.want) > 1e-9 {
				t.Fatalf("EvalExpression(%q)=%v want %v", tc.expr, got, tc.want)
			}
		})
	}
}

func TestEvalExpressionErrors(t *testing.T) {
	tests := []string{
		"",
		"(",
		"1+",
		"2**3",
		"1/0",
		"abc",
	}

	for _, expr := range tests {
		t.Run(expr, func(t *testing.T) {
			if _, err := EvalExpression(expr); err == nil {
				t.Fatalf("EvalExpression(%q) expected error", expr)
			}
		})
	}
}
