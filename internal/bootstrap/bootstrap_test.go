package bootstrap

import (
	"regexp"
	"strings"
	"testing"
)

func TestValidate(t *testing.T) {
	cases := []struct {
		id, secret string
		ok         bool
	}{
		{"abc123", "s", true},
		{strings.Repeat("a", 21), strings.Repeat("s", 64), true},
		{strings.Repeat("a", 22), "s", false},
		{"ABC", "s", false},
		{"ab-c", "s", false},
		{"", "s", false},
		{"abc", "", false},
		{"abc", strings.Repeat("s", 65), false},
	}
	for _, c := range cases {
		if err := Validate(c.id, c.secret); (err == nil) != c.ok {
			t.Errorf("Validate(%q, len %d) = %v, want ok=%t", c.id, len(c.secret), err, c.ok)
		}
	}
}

func TestRandomValues(t *testing.T) {
	id, err := RandomID()
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^[a-z0-9]{21}$`).MatchString(id) {
		t.Fatalf("bad id %q", id)
	}
	secret, err := RandomSecret()
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^[A-Za-z0-9]{32}$`).MatchString(secret) {
		t.Fatalf("bad secret %q", secret)
	}
	if err := Validate(id, secret); err != nil {
		t.Fatal(err)
	}
}
