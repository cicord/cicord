package discord

import "testing"

func TestCompareAudiencePolicies(t *testing.T) {
	roles := []Role{{ID: "1", Permissions: readBoth}, {ID: "2"}, {ID: "3"}}
	channel := func(id string, ovs ...Overwrite) *Audience {
		return NewAudience(Channel{ServerID: "1", ID: id, Type: ChannelText, Overwrites: ovs}, roles)
	}
	everyone := Overwrite{ID: "1", Type: OverwriteRole, Deny: readBoth}
	staff := Overwrite{ID: "2", Type: OverwriteRole, Allow: readBoth}
	open := channel("10")
	secret := channel("11", everyone, staff)
	scoped := channel("12", everyone, staff, Overwrite{ID: "42", Type: OverwriteMember, Deny: readHistory})
	for _, tc := range []struct {
		a, b *Audience
		want string
	}{
		{open, secret, "narrower"}, {secret, open, "wider"}, {secret, scoped, "narrower"}, {scoped, secret, "wider"},
		{secret, channel("13", everyone, staff), "same"}, {nil, open, "unknown"},
	} {
		if got := CompareAudience(tc.a, tc.b); got != tc.want {
			t.Errorf("audience = %s; want %s", got, tc.want)
		}
	}
	p, ok := secret.Permissions("42", []string{"2"}, "owner", false)
	if !ok || !p.Has(readBoth) {
		t.Fatal("staff cannot view source")
	}
	p, ok = scoped.Permissions("42", []string{"2"}, "owner", false)
	if !ok || p.Has(readBoth) {
		t.Fatal("member deny ignored")
	}
	p, ok = secret.Permissions("42", []string{"99"}, "owner", false)
	if ok || p.Has(readBoth) {
		t.Fatal("unknown role allowed read")
	}
	// Two independent role grants combine to expose the destination. Comparing each
	// single role in isolation would accidentally approve this copy.
	split := channel("20", everyone, Overwrite{ID: "2", Type: OverwriteRole, Allow: readView}, Overwrite{ID: "3", Type: OverwriteRole, Allow: readHistory})
	if got := CompareAudience(channel("21", everyone), split); got != "wider" {
		t.Fatalf("combined roles: %s", got)
	}
	private := NewAudience(Channel{ServerID: "1", ID: "30", Type: ChannelPrivateThread}, roles)
	if got := CompareAudience(private, open); got != "unknown" {
		t.Fatalf("private thread = %s", got)
	}
}

func TestAudienceComparisonAgreesWithEnumeration(t *testing.T) {
	roles := []Role{{ID: "1", Permissions: readView}, {ID: "2", Permissions: readHistory}, {ID: "3"}, {ID: "4", Permissions: readView}}
	channels := []Channel{{ID: "10", ServerID: "1", Type: ChannelText}}
	for _, kind := range []OverwriteType{OverwriteRole, OverwriteMember} {
		for _, id := range []string{"1", "2", "3", "42"} {
			if kind == OverwriteRole && id == "42" {
				continue
			}
			for _, allow := range []Permissions{0, readView, readHistory, readBoth} {
				for _, deny := range []Permissions{0, readView, readHistory, readBoth} {
					if allow&deny != 0 {
						continue
					}
					ch := Channel{ID: "example", ServerID: "1", Type: ChannelText, Overwrites: []Overwrite{{ID: id, Type: kind, Allow: allow, Deny: deny}}}
					channels = append(channels, ch)
				}
			}
		}
	}
	for _, a := range channels {
		for _, b := range channels {
			b.ID = "destination"
			src, dst := NewAudience(a, roles), NewAudience(b, roles)
			got := CompareAudience(src, dst)
			for mask := 0; mask < 8; mask++ {
				var assigned []string
				for j, r := range roles[1:] {
					if mask&(1<<j) != 0 {
						assigned = append(assigned, r.ID)
					}
				}
				for _, user := range []string{"1", "2", "3", "42", "ordinary"} {
					p, _ := src.Permissions(user, assigned, "owner", false)
					q, _ := dst.Permissions(user, assigned, "owner", false)
					if (got == "same" || got == "narrower") && q.Has(readBoth) && !p.Has(readBoth) {
						t.Fatalf("copy would widen access: source %+v destination %+v user %s roles %v", a, b, user, assigned)
					}
				}
			}
		}
	}
}
