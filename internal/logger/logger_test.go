package logger

import "testing"

func TestVisibleLen(t *testing.T) {
	tests := []struct {
		input    string
		expected int
	}{
		{"hello", 5},
		{"", 0},
		{"\033[31mred\033[0m", 3},
		{"\033[1m\033[32mbold green\033[0m", 10},
		{"plain text", 10},
		{"héllo", 5},
		{"你好", 2},
	}

	for _, tc := range tests {
		got := visibleLen(tc.input)
		if got != tc.expected {
			t.Errorf("visibleLen(%q) = %d, want %d", tc.input, got, tc.expected)
		}
	}
}
