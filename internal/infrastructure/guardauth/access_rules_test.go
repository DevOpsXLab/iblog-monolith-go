package guardauth

import (
	"testing"

	"github.com/bakhod1r/guard"
	accessdomain "github.com/bakhod1r/guard/access/domain"
)

func TestParseHours(t *testing.T) {
	for in, ok := range map[string]bool{
		"09:00-18:00": true, " 22:00 - 06:00 ": true,
		"9:00-18:00": false, "09:00-24:00": false, "09:00": false, "09:00-09:00": false,
	} {
		if _, _, err := ParseHours(in); (err == nil) != ok {
			t.Errorf("%q: err = %v", in, err)
		}
	}
}

// gate decides app.access for a subject at a time and IP under rules r.
func gate(t *testing.T, r AccessRules, role, at, ip string) bool {
	t.Helper()
	want, err := r.policies()
	if err != nil {
		t.Fatal(err)
	}
	var ps []accessdomain.Policy
	for _, p := range want {
		if p != nil {
			if err := p.Validate(); err != nil {
				t.Fatal(err)
			}
			ps = append(ps, *p)
		}
	}
	d := accessdomain.Decide(accessdomain.Request{
		Subject:     accessdomain.Subject{ID: "7", Roles: []guard.Role{{Name: role, Permissions: []accessdomain.Permission{{Resource: "app", Action: "access"}}}}},
		Action:      "access",
		Resource:    guard.Resource{Type: "app"},
		Environment: map[string]any{"time": at, "ip": ip},
	}, ps)
	return d.Allowed
}

func TestAccessRules(t *testing.T) {
	day := AccessRules{ModeratorIPs: []string{"203.0.113.7"}, ModeratorHours: "09:00-18:00"}
	for _, c := range []struct {
		role, at, ip string
		open         bool
	}{
		{"moderator", "10:00", "203.0.113.7", true},
		{"moderator", "09:00", "203.0.113.7", true},
		{"moderator", "18:00", "203.0.113.7", false},
		{"moderator", "23:30", "203.0.113.7", false},
		{"moderator", "08:59", "203.0.113.7", false},
		{"moderator", "10:00", "198.51.100.1", false},
		{"user", "23:30", "198.51.100.1", true}, // rules bind moderators only
	} {
		if got := gate(t, day, c.role, c.at, c.ip); got != c.open {
			t.Errorf("%s at %s from %s: open = %v", c.role, c.at, c.ip, got)
		}
	}

	night := AccessRules{ModeratorHours: "22:00-06:00"}
	for at, open := range map[string]bool{"23:00": true, "02:00": true, "06:00": false, "12:00": false, "22:00": true} {
		if got := gate(t, night, "moderator", at, "x"); got != open {
			t.Errorf("night shift at %s: open = %v", at, got)
		}
	}

	if _, err := (AccessRules{ModeratorIPs: []string{"10.0.0.0/8"}}).policies(); err == nil {
		t.Error("a network must be refused")
	}
	if got := gate(t, AccessRules{}, "moderator", "03:00", "x"); !got {
		t.Error("no rules: open")
	}
}
