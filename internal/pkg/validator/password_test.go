package validator

import "testing"

func TestValidatePasswordComplexity(t *testing.T) {
	cases := []struct {
		name     string
		password string
		wantOK   bool
	}{
		{"valid", "Str0ng!Passw0rd", true},
		{"too short", "Sh0rt!Aa", false},
		{"no upper", "str0ng!passw0rd", false},
		{"no lower", "STR0NG!PASSW0RD", false},
		{"no digit", "Strong!Password", false},
		{"no special", "Str0ngPassw0rd12", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msg := ValidatePasswordComplexity(tc.password)
			gotOK := msg == ""
			if gotOK != tc.wantOK {
				t.Errorf("ValidatePasswordComplexity(%q) = %q, wantOK=%v", tc.password, msg, tc.wantOK)
			}
		})
	}
}
