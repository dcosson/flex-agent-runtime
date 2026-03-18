package main

import (
	"reflect"
	"testing"
)

func TestNormalizeBaseURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "bare host", in: "localhost:8080", want: "http://localhost:8080"},
		{name: "http", in: "http://localhost:8080/", want: "http://localhost:8080"},
		{name: "https", in: "https://example.com/", want: "https://example.com"},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := normalizeBaseURL(tc.in)
			if got != tc.want {
				t.Fatalf("normalizeBaseURL(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestAuthHeaderValue(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		in        string
		wantValue string
		wantOK    bool
	}{
		{name: "empty", in: "", wantValue: "", wantOK: false},
		{name: "token only", in: "abc123", wantValue: "Bearer abc123", wantOK: true},
		{name: "already bearer", in: "Bearer abc123", wantValue: "Bearer abc123", wantOK: true},
		{name: "already bearer lowercase", in: "bearer abc123", wantValue: "bearer abc123", wantOK: true},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, ok := authHeaderValue(tc.in)
			if ok != tc.wantOK || got != tc.wantValue {
				t.Fatalf("authHeaderValue(%q) = (%q,%v), want (%q,%v)", tc.in, got, ok, tc.wantValue, tc.wantOK)
			}
		})
	}
}

func TestParseTools(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want []string
	}{
		{name: "empty", in: "", want: nil},
		{name: "spaces", in: "  ", want: nil},
		{name: "single", in: "bash", want: []string{"bash"}},
		{name: "multi", in: "bash, read_file,write_file", want: []string{"bash", "read_file", "write_file"}},
		{name: "skip empty", in: "bash,,read_file, ", want: []string{"bash", "read_file"}},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := parseTools(tc.in)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("parseTools(%q) = %#v, want %#v", tc.in, got, tc.want)
			}
		})
	}
}
