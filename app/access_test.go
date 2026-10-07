package app_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/iBlog/iblog-monolith-go/config"
)

// Deny policies on app.access close the whole API for the roles they are
// bound to, by network (env.ip) or by clock (env.time / env.weekday).
func TestAccessGate(t *testing.T) {
	e := start(t)
	anon := e.anon(t)
	admin := anon.login("admin", "admin-pass-123")
	mod := anon.register("modgate")
	ali := anon.register("aligate")
	admin.do("POST", fmt.Sprintf("/api/guard/users/%d/roles", mod.user.ID), map[string]string{"role": "moderator"}, 204, nil)
	mod.do("GET", "/api/me", nil, 200, nil)

	// Only from the office IP: the test client is not it.
	var ipOnly struct{ ID string }
	admin.do("POST", "/api/guard/policies", map[string]any{
		"name": "moderators from the office only", "resource": "app", "action": "access",
		"effect": "deny", "priority": 1, "enabled": true, "roles": []string{"moderator"},
		"root": map[string]any{"operator": "and", "conditions": []map[string]any{
			{"field": "env.ip", "operator": "not_in", "value": []string{"198.51.100.7"}},
		}},
	}, 201, &ipOnly)
	mod.do("GET", "/api/me", nil, 403, nil)
	ali.do("GET", "/api/me", nil, 200, nil) // not bound to "user"
	admin.do("GET", "/api/me", nil, 200, nil)
	mod.do("POST", "/api/auth/logout", nil, 204, nil) // signing out stays open
	admin.do("DELETE", "/api/guard/policies/"+ipOnly.ID, nil, 204, nil)

	// Closed outside working hours: every weekday matches, so it is closed now.
	mod = anon.login("modgate", "secret123")
	mod.do("GET", "/api/me", nil, 200, nil)
	var night struct{ ID string }
	admin.do("POST", "/api/guard/policies", map[string]any{
		"name": "moderators: closed", "resource": "app", "action": "access",
		"effect": "deny", "priority": 1, "enabled": true, "roles": []string{"moderator"},
		"root": map[string]any{"operator": "and", "conditions": []map[string]any{
			{"field": "env.weekday", "operator": "in", "value": []string{"mon", "tue", "wed", "thu", "fri", "sat", "sun"}},
			{"field": "env.time", "operator": "gte", "value": []string{"00:00"}},
		}},
	}, 201, &night)
	mod.do("GET", "/api/me", nil, 403, nil)
	mod.do("GET", "/api/guard/roles", nil, 403, nil) // Guard's own routes too
	admin.do("DELETE", "/api/guard/policies/"+night.ID, nil, 204, nil)
	mod.do("GET", "/api/me", nil, 200, nil)
}

// ACCESS_MODERATOR_IPS becomes a policy on start; the test client is not the office.
func TestAccessRulesFromConfig(t *testing.T) {
	e := start(t, func(c *config.Config) { c.ModeratorIPs = "198.51.100.7"; c.ModeratorHours = "00:00-23:59" })
	anon := e.anon(t)
	admin := anon.login("admin", "admin-pass-123")
	mod := anon.register("modcfg")
	mod.do("GET", "/api/me", nil, 200, nil)
	admin.do("POST", fmt.Sprintf("/api/guard/users/%d/roles", mod.user.ID), map[string]string{"role": "moderator"}, 204, nil)
	mod.do("GET", "/api/me", nil, 403, nil)

	var list []struct {
		Name  string   `json:"name"`
		Roles []string `json:"roles"`
	}
	admin.do("GET", "/api/guard/policies", nil, 200, &list)
	found := 0
	for _, p := range list {
		if strings.HasPrefix(p.Name, "config: ") && len(p.Roles) == 1 && p.Roles[0] == "moderator" {
			found++
		}
	}
	if found != 2 {
		t.Fatalf("config policies = %d, want 2: %+v", found, list)
	}
}
