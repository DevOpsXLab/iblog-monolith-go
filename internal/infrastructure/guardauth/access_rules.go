package guardauth

import (
	"context"
	"fmt"
	"net"
	"regexp"
	"strings"

	"github.com/bakhod1r/guard"
	accessdomain "github.com/bakhod1r/guard/access/domain"
)

// AccessRules closes the API (app.access) for moderators outside an office
// network or working hours. Set in the environment, applied on every start:
// the "config:" policies follow the config, edits made in the admin panel to
// them are reset, and a rule left empty removes its policy.
type AccessRules struct {
	// ModeratorIPs: moderators may use the API only from these addresses.
	ModeratorIPs []string
	// ModeratorHours "09:00-18:00": moderators may use the API only inside it
	// (env.time, ACCESS_TIMEZONE). A window like "22:00-06:00" spans midnight.
	ModeratorHours string
}

// NewAccessRules builds the rules from their settings: ips is a
// comma-separated list (blanks dropped), hours an "HH:MM-HH:MM" window.
func NewAccessRules(ips, hours string) AccessRules {
	var list []string
	for _, v := range strings.Split(ips, ",") {
		if v = strings.TrimSpace(v); v != "" {
			list = append(list, v)
		}
	}
	return AccessRules{ModeratorIPs: list, ModeratorHours: hours}
}

const (
	policyModeratorIPs   = "config: moderators only from the office network"
	policyModeratorHours = "config: moderators only in working hours"
)

var clock = regexp.MustCompile(`^([01]\d|2[0-3]):[0-5]\d$`)

// ParseHours splits "09:00-18:00" into its from and to times.
func ParseHours(s string) (from, to string, err error) {
	from, to, ok := strings.Cut(strings.ReplaceAll(s, " ", ""), "-")
	if !ok || !clock.MatchString(from) || !clock.MatchString(to) || from == to {
		return "", "", fmt.Errorf("hours %q: want HH:MM-HH:MM", s)
	}
	return from, to, nil
}

// SeedAccessRules writes the policies of r, and deletes those whose rule is empty.
func SeedAccessRules(ctx context.Context, g *guard.Guard, r AccessRules) error {
	want, err := r.policies()
	if err != nil {
		return err
	}
	existing, err := g.Access.ListPolicies(ctx)
	if err != nil {
		return err
	}
	ids := map[string]string{}
	for _, p := range existing {
		ids[p.Name] = p.ID
	}
	for name, p := range want {
		switch {
		case p != nil:
			p.ID = ids[name]
			if err := g.Access.SavePolicy(ctx, p); err != nil {
				return fmt.Errorf("policy %q: %w", name, err)
			}
		case ids[name] != "":
			if err := g.Access.DeletePolicy(ctx, ids[name]); err != nil {
				return fmt.Errorf("policy %q: %w", name, err)
			}
		}
	}
	return g.InvalidateAccess(ctx)
}

// policies maps each config policy name to its policy, nil when its rule is empty.
func (r AccessRules) policies() (map[string]*accessdomain.Policy, error) {
	want := map[string]*accessdomain.Policy{policyModeratorIPs: nil, policyModeratorHours: nil}
	for _, ip := range r.ModeratorIPs {
		if net.ParseIP(ip) == nil {
			return nil, fmt.Errorf("moderator IP %q: not an IP address (networks are not supported)", ip)
		}
	}
	if len(r.ModeratorIPs) > 0 {
		want[policyModeratorIPs] = gatePolicy(policyModeratorIPs, accessdomain.ConditionGroup{
			Operator:   accessdomain.And,
			Conditions: []accessdomain.Condition{{Field: "env.ip", Operator: accessdomain.OpNotIn, Value: r.ModeratorIPs}},
		})
	}
	if r.ModeratorHours != "" {
		from, to, err := ParseHours(r.ModeratorHours)
		if err != nil {
			return nil, err
		}
		// Outside [from, to): before from or from to on. Across midnight
		// (from > to) the closed part is the day window [to, from).
		closed := accessdomain.ConditionGroup{Operator: accessdomain.Or, Conditions: []accessdomain.Condition{
			{Field: "env.time", Operator: accessdomain.OpLt, Value: []string{from}},
			{Field: "env.time", Operator: accessdomain.OpGte, Value: []string{to}},
		}}
		if from > to {
			closed = accessdomain.ConditionGroup{Operator: accessdomain.And, Conditions: []accessdomain.Condition{
				{Field: "env.time", Operator: accessdomain.OpGte, Value: []string{to}},
				{Field: "env.time", Operator: accessdomain.OpLt, Value: []string{from}},
			}}
		}
		want[policyModeratorHours] = gatePolicy(policyModeratorHours, closed)
	}
	return want, nil
}

// gatePolicy denies app.access to moderators when root holds. Priority 1
// outranks every allow the blog seeds (100).
func gatePolicy(name string, root accessdomain.ConditionGroup) *accessdomain.Policy {
	return &accessdomain.Policy{
		Name: name, Resource: "app", Action: "access",
		Effect: accessdomain.Deny, Priority: 1, Enabled: true,
		Roles: []string{"moderator"}, Root: &root,
	}
}
