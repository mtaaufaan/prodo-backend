package validator

import (
	"strings"
	"testing"
)

func TestIsValidSlug(t *testing.T) {
	cases := []struct {
		slug string
		want bool
	}{
		{"acme-corp", true},
		{"acme", true},
		{"acme123", true},
		{"", false},
		{"Acme-Corp", false},
		{"-acme", false},
		{"acme-", false},
		{"acme--corp", false},
		{"acme corp", false},
		{"acme_corp", false},
		{strings.Repeat("a", 101), false},
		{strings.Repeat("a", 100), true},
	}

	for _, tc := range cases {
		if got := IsValidSlug(tc.slug); got != tc.want {
			t.Errorf("IsValidSlug(%q) = %v, want %v", tc.slug, got, tc.want)
		}
	}
}
