package check

import "testing"

func TestFromThreshold(t *testing.T) {
	tests := []struct {
		name             string
		value, warn, crit float64
		want             Status
	}{
		{"below all", 50, 80, 90, StatusOK},
		{"at warn", 80, 80, 90, StatusWarn},
		{"between", 85, 80, 90, StatusWarn},
		{"at crit", 90, 80, 90, StatusCrit},
		{"above crit", 99, 80, 90, StatusCrit},
		{"warn disabled", 85, 0, 90, StatusOK},
		{"crit disabled, hits warn", 95, 80, 0, StatusWarn},
		{"both disabled", 100, 0, 0, StatusOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := FromThreshold(tt.value, tt.warn, tt.crit); got != tt.want {
				t.Errorf("FromThreshold(%v,%v,%v) = %v, want %v", tt.value, tt.warn, tt.crit, got, tt.want)
			}
		})
	}
}

func TestCountStatus(t *testing.T) {
	tests := []struct {
		count, warn, crit int
		want              Status
	}{
		{0, 1, 10, StatusOK},
		{1, 1, 10, StatusWarn},
		{9, 1, 10, StatusWarn},
		{10, 1, 10, StatusCrit},
		{5, 0, 10, StatusOK}, // warn disabled
	}
	for _, tt := range tests {
		if got := countStatus(tt.count, tt.warn, tt.crit); got != tt.want {
			t.Errorf("countStatus(%d,%d,%d) = %v, want %v", tt.count, tt.warn, tt.crit, got, tt.want)
		}
	}
}

func TestStatusSeverityOrdering(t *testing.T) {
	if !(StatusOK.Severity() < StatusWarn.Severity() &&
		StatusWarn.Severity() < StatusCrit.Severity() &&
		StatusCrit.Severity() < StatusUnknown.Severity()) {
		t.Fatal("severity ordering is wrong")
	}
}

func TestParseStatus(t *testing.T) {
	cases := map[string]Status{
		"ok": StatusOK, "warn": StatusWarn, "warning": StatusWarn,
		"crit": StatusCrit, "critical": StatusCrit, "unknown": StatusUnknown,
	}
	for in, want := range cases {
		got, err := ParseStatus(in)
		if err != nil || got != want {
			t.Errorf("ParseStatus(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	if _, err := ParseStatus("bogus"); err == nil {
		t.Error("expected error for bogus status")
	}
}
