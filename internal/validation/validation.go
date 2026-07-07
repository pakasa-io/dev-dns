// Package validation checks DNS hostnames, IP addresses, and record types.
//
// It has no dependencies on the rest of the project so it can be reused freely.
package validation

import (
	"fmt"
	"net"
	"strings"
)

// SupportedTypes lists the DNS record types devdns can emit.
var SupportedTypes = []string{"A", "AAAA", "CNAME"}

// NormalizeType upper-cases t and verifies it is a supported record type.
func NormalizeType(t string) (string, error) {
	up := strings.ToUpper(strings.TrimSpace(t))
	for _, s := range SupportedTypes {
		if up == s {
			return up, nil
		}
	}
	return "", fmt.Errorf("unsupported record type %q (supported: %s)", t, strings.Join(SupportedTypes, ", "))
}

// ValidIPv4 reports whether s is a valid dotted-quad IPv4 address.
func ValidIPv4(s string) bool {
	s = strings.TrimSpace(s)
	if strings.Contains(s, ":") {
		return false
	}
	ip := net.ParseIP(s)
	return ip != nil && ip.To4() != nil
}

// ValidIPv6 reports whether s is a valid IPv6 address.
func ValidIPv6(s string) bool {
	s = strings.TrimSpace(s)
	if !strings.Contains(s, ":") {
		return false
	}
	return net.ParseIP(s) != nil
}

// ValidateHostname verifies name is a syntactically valid DNS domain name.
// A single leading "*" label (wildcard) is allowed, as is a trailing dot.
func ValidateHostname(name string) error {
	h := strings.TrimSuffix(strings.TrimSpace(name), ".")
	if h == "" {
		return fmt.Errorf("hostname is empty")
	}
	if len(h) > 253 {
		return fmt.Errorf("hostname %q exceeds 253 characters", name)
	}
	for i, label := range strings.Split(h, ".") {
		if label == "*" && i == 0 {
			continue // wildcard leftmost label
		}
		if err := validateLabel(label); err != nil {
			return fmt.Errorf("invalid hostname %q: %w", name, err)
		}
	}
	return nil
}

func validateLabel(l string) error {
	switch {
	case l == "":
		return fmt.Errorf("empty label (check for consecutive or leading dots)")
	case len(l) > 63:
		return fmt.Errorf("label %q exceeds 63 characters", l)
	case l[0] == '-' || l[len(l)-1] == '-':
		return fmt.Errorf("label %q must not start or end with a hyphen", l)
	}
	for _, r := range l {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-':
		default:
			return fmt.Errorf("label %q contains invalid character %q", l, string(r))
		}
	}
	return nil
}

// ValidateValue checks that value is well-formed for the given record type.
// recType must already be normalized (see NormalizeType).
func ValidateValue(recType, value string) error {
	switch recType {
	case "A":
		if !ValidIPv4(value) {
			return fmt.Errorf("%q is not a valid IPv4 address", value)
		}
	case "AAAA":
		if !ValidIPv6(value) {
			return fmt.Errorf("%q is not a valid IPv6 address", value)
		}
	case "CNAME":
		return ValidateHostname(value)
	default:
		return fmt.Errorf("unsupported record type %q", recType)
	}
	return nil
}
