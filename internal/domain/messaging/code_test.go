package messaging

import (
	"strings"
	"testing"
)

// A character somebody cannot tell apart is a failed link they have no way to
// diagnose, because the bot can only say the code was wrong.
func TestTheAlphabetHasNoAmbiguousCharacters(t *testing.T) {
	for _, r := range "01OIL" {
		if strings.ContainsRune(codeAlphabet, r) {
			t.Errorf("%q is in the alphabet; it cannot be told apart from another character in it", r)
		}
	}
	if len(codeAlphabet) < 20 {
		t.Errorf("the alphabet is %d characters; too few to be worth typing", len(codeAlphabet))
	}
}

func TestNewCodeIsTheRightShape(t *testing.T) {
	seen := map[string]bool{}
	for range 100 {
		code, err := NewCode()
		if err != nil {
			t.Fatalf("new code: %v", err)
		}
		if len(code) != codeLength {
			t.Fatalf("code %q is %d characters, want %d", code, len(code), codeLength)
		}
		for _, r := range code {
			if !strings.ContainsRune(codeAlphabet, r) {
				t.Fatalf("code %q contains %q, which is not in the alphabet", code, r)
			}
		}
		seen[code] = true
	}
	// Not a randomness test; it catches a constant.
	if len(seen) < 90 {
		t.Errorf("100 codes produced %d distinct values", len(seen))
	}
}

// People send the code with the case their keyboard chose, spaces the chat
// client inserted, and whatever they typed around it. Refusing a correct code
// because of a space, or because somebody wrote "Code:" in front of it, is a
// failure they cannot diagnose — the bot can only say it was wrong.
func TestFindCodeAcceptsWhatPeopleActuallySend(t *testing.T) {
	const want = "ABCD2345"

	for _, raw := range []string{
		"ABCD2345",
		"abcd2345",
		"  ABCD2345  ",
		"ABCD 2345",
		"abcd-2345",
		"Code: ABCD2345",
		"here is my code ABCD2345 thanks",
	} {
		if got := FindCode(raw); got != want {
			t.Errorf("FindCode(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestFindCodeReportsNothingWhenThereIsNoCode(t *testing.T) {
	for _, raw := range []string{
		"",
		"three days in Lisbon",
		"hello",
		// Too short and too long are both "not a code".
		"ABCD234",
		"ABCD23456",
	} {
		if got := FindCode(raw); got != "" {
			t.Errorf("FindCode(%q) = %q, want no code", raw, got)
		}
	}
}

func TestHashCodeIsStableAcrossHowItWasTyped(t *testing.T) {
	a := HashCode("abcd 2345")
	b := HashCode("ABCD-2345")

	if !SameCode(a, b) {
		t.Fatal("the same code hashed differently depending on how it was typed")
	}
	if SameCode(a, HashCode("ABCD2346")) {
		t.Fatal("two different codes hashed the same")
	}
	if strings.Contains(string(a), "ABCD") {
		t.Fatal("the hash contains the code")
	}
}

func TestParseCommand(t *testing.T) {
	cases := []struct {
		in   string
		want command
		arg  string
	}{
		{"/start", cmdStart, ""},
		{"/start ABCD2345", cmdStart, "ABCD2345"},
		// Telegram appends the bot name in group chats.
		{"/help@loci_bot", cmdHelp, ""},
		{"/HELP", cmdHelp, ""},
		{"  /unlink  ", cmdUnlink, ""},
		{"/disconnect", cmdUnlink, ""},
		{"/nonsense", cmdUnknown, ""},
		{"three days in Lisbon", cmdNone, ""},
		{"", cmdNone, ""},
		{"not/a/command", cmdNone, ""},
	}

	for _, tc := range cases {
		gotCmd, gotArg := parseCommand(tc.in)
		if gotCmd != tc.want || gotArg != tc.arg {
			t.Errorf("parseCommand(%q) = (%v, %q), want (%v, %q)", tc.in, gotCmd, gotArg, tc.want, tc.arg)
		}
	}
}
