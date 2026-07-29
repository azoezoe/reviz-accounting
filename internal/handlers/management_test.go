package handlers

import "testing"

func TestParseHours(t *testing.T) {
	for input, want := range map[string]int{"": 0, "1": 60, "1.5": 90, "0.25": 15} {
		got, err := parseHours(input)
		if err != nil || got != want {
			t.Fatalf("parseHours(%q) = %d, %v; want %d", input, got, err, want)
		}
	}
	if _, err := parseHours("-1"); err == nil {
		t.Fatal("negative hours should fail")
	}
}
