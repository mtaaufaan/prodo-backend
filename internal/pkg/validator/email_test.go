package validator

import "testing"

func TestIsValidEmail(t *testing.T) {
	cases := []struct {
		email string
		want  bool
	}{
		{"ga@example.com", true},
		{"ga.new+tag@example.co.id", true},
		{"not-an-email", false},
		{"", false},
		{"@example.com", false},
		{"ga@", false},
	}

	for _, tc := range cases {
		if got := IsValidEmail(tc.email); got != tc.want {
			t.Errorf("IsValidEmail(%q) = %v, want %v", tc.email, got, tc.want)
		}
	}
}
