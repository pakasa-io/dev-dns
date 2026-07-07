package validation

import "testing"

func TestNormalizeType(t *testing.T) {
	ok := map[string]string{"a": "A", "A": "A", "aaaa": "AAAA", "CNAME": "CNAME", " cname ": "CNAME"}
	for in, want := range ok {
		got, err := NormalizeType(in)
		if err != nil || got != want {
			t.Errorf("NormalizeType(%q) = %q, %v; want %q, nil", in, got, err, want)
		}
	}
	for _, bad := range []string{"MX", "TXT", "SRV", ""} {
		if _, err := NormalizeType(bad); err == nil {
			t.Errorf("NormalizeType(%q) = nil error; want error", bad)
		}
	}
}

func TestValidIPv4(t *testing.T) {
	good := []string{"127.0.0.1", "0.0.0.0", "192.168.1.1", "255.255.255.255"}
	bad := []string{"256.0.0.1", "1.2.3", "1.2.3.4.5", "::1", "abc", "", "1.2.3.04 "}
	for _, s := range good {
		if !ValidIPv4(s) {
			t.Errorf("ValidIPv4(%q) = false; want true", s)
		}
	}
	for _, s := range bad {
		if ValidIPv4(s) {
			t.Errorf("ValidIPv4(%q) = true; want false", s)
		}
	}
}

func TestValidIPv6(t *testing.T) {
	good := []string{"::1", "2001:db8::1", "fe80::1", "::"}
	bad := []string{"127.0.0.1", "", "nope", "2001:zzzz::1"}
	for _, s := range good {
		if !ValidIPv6(s) {
			t.Errorf("ValidIPv6(%q) = false; want true", s)
		}
	}
	for _, s := range bad {
		if ValidIPv6(s) {
			t.Errorf("ValidIPv6(%q) = true; want false", s)
		}
	}
}

func TestValidateHostname(t *testing.T) {
	good := []string{"app.example.internal", "app.example.internal.", "a", "a-b.c", "*.example.internal", "x1.y2.z3"}
	bad := []string{"", "-bad.example", "bad-.example", "a..b", ".leading", "under_score.example", "a b.c"}
	for _, s := range good {
		if err := ValidateHostname(s); err != nil {
			t.Errorf("ValidateHostname(%q) = %v; want nil", s, err)
		}
	}
	for _, s := range bad {
		if err := ValidateHostname(s); err == nil {
			t.Errorf("ValidateHostname(%q) = nil; want error", s)
		}
	}
}

func TestValidateValue(t *testing.T) {
	cases := []struct {
		typ, val string
		wantErr  bool
	}{
		{"A", "127.0.0.1", false},
		{"A", "::1", true},
		{"AAAA", "2001:db8::1", false},
		{"AAAA", "127.0.0.1", true},
		{"CNAME", "app.example.internal", false},
		{"CNAME", "-bad", true},
	}
	for _, c := range cases {
		err := ValidateValue(c.typ, c.val)
		if (err != nil) != c.wantErr {
			t.Errorf("ValidateValue(%q,%q) err=%v; wantErr=%v", c.typ, c.val, err, c.wantErr)
		}
	}
}
