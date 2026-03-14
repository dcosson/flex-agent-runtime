package zfs

import "testing"

func TestParseRatioPercentValues(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want float64
	}{
		{in: "0", want: 0.0},
		{in: "1", want: 0.01},
		{in: "20", want: 0.2},
		{in: "100", want: 1.0},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got, err := parseRatio(tc.in)
			if err != nil {
				t.Fatalf("parseRatio(%q) error = %v", tc.in, err)
			}
			if got != tc.want {
				t.Fatalf("parseRatio(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}
