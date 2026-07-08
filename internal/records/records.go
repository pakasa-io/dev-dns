// Package records loads, validates, mutates, and persists the devdns records
// file (records.yaml). It is the single source of truth from which the CoreDNS
// zone file and Corefile are generated.
package records

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"dev-dns/internal/validation"
)

// Defaults applied at generation time when the corresponding field is unset.
const (
	DefaultTTL  = 60
	DefaultPort = 53
)

// DefaultUpstreams are the Cloudflare and Google public resolvers.
var DefaultUpstreams = []string{"1.1.1.1", "1.0.0.1", "8.8.8.8", "8.8.4.4"}

// Record is a single DNS record. Name is stored relative to the zone, with the
// apex written as "@".
type Record struct {
	Name  string `yaml:"name"`
	Type  string `yaml:"type"`
	Value string `yaml:"value"`
	TTL   int    `yaml:"ttl,omitempty"`
}

// Config is the full devdns configuration together with its record set.
type Config struct {
	Zone      string   `yaml:"zone"`
	TTL       int      `yaml:"ttl,omitempty"`
	Address   string   `yaml:"address,omitempty"`
	Port      int      `yaml:"port,omitempty"`
	Upstreams []string `yaml:"upstreams,omitempty"`
	Records   []Record `yaml:"records"`
}

// ResolvedTTL returns the configured TTL or the default.
func (c *Config) ResolvedTTL() int {
	if c.TTL > 0 {
		return c.TTL
	}
	return DefaultTTL
}

// ResolvedPort returns the configured listen port or the default.
func (c *Config) ResolvedPort() int {
	if c.Port > 0 {
		return c.Port
	}
	return DefaultPort
}

// ResolvedUpstreams returns the configured upstream resolvers or the defaults.
func (c *Config) ResolvedUpstreams() []string {
	if len(c.Upstreams) > 0 {
		return c.Upstreams
	}
	return DefaultUpstreams
}

// Load reads and parses a records file, normalizing the zone and every record
// name so that generation is correct regardless of how the file was written by
// hand. It does not fail on individual invalid records; call Validate for that.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("records file %s not found", path)
		}
		return nil, err
	}
	var c Config
	if err := yaml.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	c.Zone = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(c.Zone)), ".")
	for i := range c.Records {
		if n, err := NormalizeName(c.Records[i].Name, c.Zone); err == nil {
			c.Records[i].Name = n
		}
		c.Records[i].Type = strings.ToUpper(strings.TrimSpace(c.Records[i].Type))
		c.Records[i].Value = strings.TrimSpace(c.Records[i].Value)
	}
	return &c, nil
}

// Save writes the config back to path (YAML). The write is atomic via a
// temporary file and rename. Note: comments in the original file are not
// preserved.
func Save(path string, c *Config) error {
	data, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// NormalizeName converts an input name (short label, FQDN, or "@") into a name
// relative to zone. The apex is returned as "@".
func NormalizeName(input, zone string) (string, error) {
	n := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(input)), ".")
	if n == "" {
		return "", fmt.Errorf("name is empty")
	}
	if n == "@" || n == zone {
		return "@", nil
	}
	if zone != "" && strings.HasSuffix(n, "."+zone) {
		if rel := strings.TrimSuffix(n, "."+zone); rel != "" {
			return rel, nil
		}
		return "@", nil
	}
	return n, nil
}

// FQDN returns the fully qualified name (with trailing dot) for a relative name.
func FQDN(name, zone string) string {
	if name == "@" {
		return zone + "."
	}
	return name + "." + zone + "."
}

// Display returns a human-friendly fully qualified name (no trailing dot).
func Display(name, zone string) string {
	if name == "@" {
		return zone
	}
	return name + "." + zone
}

func (c *Config) find(name, recType string) int {
	for i, r := range c.Records {
		if r.Name == name && strings.EqualFold(r.Type, recType) {
			return i
		}
	}
	return -1
}

// Add inserts a record. It is idempotent when an identical record already
// exists (returns added=false, nil), and errors on conflicts: a duplicate
// name/type with a different value, or a CNAME coexisting with other records.
func (c *Config) Add(rec Record) (added bool, err error) {
	if err := c.validateRecord(rec); err != nil {
		return false, err
	}
	if err := c.checkConflicts(rec, -1); err != nil {
		return false, err
	}
	if i := c.find(rec.Name, rec.Type); i >= 0 {
		if c.Records[i].Value == rec.Value && c.Records[i].TTL == rec.TTL {
			return false, nil
		}
		return false, fmt.Errorf("a %s record for %q already exists (value %s); use `update` to change it",
			rec.Type, Display(rec.Name, c.Zone), c.Records[i].Value)
	}
	c.Records = append(c.Records, rec)
	c.sortRecords()
	return true, nil
}

// Update inserts the record, or replaces the existing record for the same
// (name, type). It returns created=true when no prior record existed.
func (c *Config) Update(rec Record) (created bool, err error) {
	if err := c.validateRecord(rec); err != nil {
		return false, err
	}
	self := c.find(rec.Name, rec.Type)
	if err := c.checkConflicts(rec, self); err != nil {
		return false, err
	}
	if self >= 0 {
		c.Records[self] = rec
		return false, nil
	}
	c.Records = append(c.Records, rec)
	c.sortRecords()
	return true, nil
}

// Remove deletes records matching name, and type when recType is non-empty.
// It returns the number of records removed.
func (c *Config) Remove(name, recType string) int {
	kept := c.Records[:0]
	removed := 0
	for _, r := range c.Records {
		if r.Name == name && (recType == "" || strings.EqualFold(r.Type, recType)) {
			removed++
			continue
		}
		kept = append(kept, r)
	}
	c.Records = kept
	return removed
}

func (c *Config) checkConflicts(rec Record, selfIdx int) error {
	newIsCNAME := strings.EqualFold(rec.Type, "CNAME")
	for i, r := range c.Records {
		if i == selfIdx || r.Name != rec.Name {
			continue
		}
		existingIsCNAME := strings.EqualFold(r.Type, "CNAME")
		if newIsCNAME && !existingIsCNAME {
			return fmt.Errorf("%q already has a %s record; a CNAME cannot coexist with other records",
				Display(rec.Name, c.Zone), strings.ToUpper(r.Type))
		}
		if !newIsCNAME && existingIsCNAME {
			return fmt.Errorf("%q is a CNAME and cannot also have a %s record",
				Display(rec.Name, c.Zone), strings.ToUpper(rec.Type))
		}
	}
	return nil
}

func (c *Config) validateRecord(rec Record) error {
	if rec.TTL < 0 {
		return fmt.Errorf("ttl must not be negative")
	}
	if _, err := validation.NormalizeType(rec.Type); err != nil {
		return err
	}
	if err := validation.ValidateHostname(FQDN(rec.Name, c.Zone)); err != nil {
		return err
	}
	return validation.ValidateValue(strings.ToUpper(rec.Type), rec.Value)
}

// Validate checks the zone and every record, including duplicate detection and
// CNAME exclusivity across the whole set. It normalizes record types in place.
func (c *Config) Validate() error {
	if c.Zone == "" {
		return fmt.Errorf("zone is required")
	}
	if err := validation.ValidateHostname(c.Zone); err != nil {
		return fmt.Errorf("invalid zone %q: %w", c.Zone, err)
	}
	seen := map[string]bool{}
	cnames := map[string]bool{}
	others := map[string]bool{}
	for i := range c.Records {
		r := &c.Records[i]
		if r.Name == "" {
			return fmt.Errorf("record with value %q has an empty name", r.Value)
		}
		nt, err := validation.NormalizeType(r.Type)
		if err != nil {
			return fmt.Errorf("record %q: %w", Display(r.Name, c.Zone), err)
		}
		r.Type = nt
		if err := c.validateRecord(*r); err != nil {
			return fmt.Errorf("record %q: %w", Display(r.Name, c.Zone), err)
		}
		key := r.Name + "|" + r.Type
		if seen[key] {
			return fmt.Errorf("duplicate %s record for %q", r.Type, Display(r.Name, c.Zone))
		}
		seen[key] = true
		if r.Type == "CNAME" {
			cnames[r.Name] = true
		} else {
			others[r.Name] = true
		}
	}
	for name := range cnames {
		if others[name] {
			return fmt.Errorf("%q has both a CNAME and other records", Display(name, c.Zone))
		}
	}
	return nil
}

func (c *Config) sortRecords() {
	sort.SliceStable(c.Records, func(i, j int) bool {
		if c.Records[i].Name != c.Records[j].Name {
			return nameLess(c.Records[i].Name, c.Records[j].Name)
		}
		return c.Records[i].Type < c.Records[j].Type
	})
}

// nameLess orders names alphabetically, with the apex ("@") first.
func nameLess(a, b string) bool {
	switch {
	case a == b:
		return false
	case a == "@":
		return true
	case b == "@":
		return false
	default:
		return a < b
	}
}
