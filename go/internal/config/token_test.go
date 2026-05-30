package config

import "testing"

func TestBotTokenRE(t *testing.T) {
	valid := []string{
		"123456789:AAHdqTcvCH1vGWJxfSeofSAs0K5PALDsaw",
		"7000000000:ABCdef_GHI-jklMNOpqrSTUvwxYZ0123456",
	}
	invalid := []string{
		"",
		"123456789",                   // no secret
		"abc:AAHdqTcvCH1vGWJxfSeofSA", // non-numeric id
		"123:short",                   // secret too short
		"123456789AAHdqTcvCH1vGWJxfSeofSAs0K5PALDsaw", // no colon
	}
	for _, tok := range valid {
		if !botTokenRE.MatchString(tok) {
			t.Errorf("expected valid: %q", tok)
		}
	}
	for _, tok := range invalid {
		if botTokenRE.MatchString(tok) {
			t.Errorf("expected invalid: %q", tok)
		}
	}
}
