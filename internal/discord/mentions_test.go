package discord

import (
	"errors"
	"strings"
	"testing"
)

func TestAllowedMentionsValidate(t *testing.T) {
	many := make([]string, MaxAllowedMentionIDs+1)
	for i := range many {
		many[i] = "1"
	}
	cases := []struct {
		in      AllowedMentions
		wantErr string
	}{
		{AllowedMentions{}, ""},
		{AllowedMentions{Users: true, Roles: true, Everyone: true}, ""},
		{AllowedMentions{RoleIDs: []string{"42"}, UserIDs: []string{}}, ""},
		{AllowedMentions{Users: true, RoleIDs: []string{"42"}}, ""},
		{AllowedMentions{Roles: true, RoleIDs: []string{"42"}}, "both"},
		{AllowedMentions{Users: true, UserIDs: []string{"42"}}, "both"},
		{AllowedMentions{RoleIDs: []string{"<@&42>"}}, "snowflake"},
		{AllowedMentions{UserIDs: []string{""}}, "snowflake"},
		{AllowedMentions{UserIDs: many}, "at most"},
		{AllowedMentions{RoleIDs: many[:MaxAllowedMentionIDs]}, ""},
	}
	for _, c := range cases {
		err := c.in.Validate()
		if c.wantErr == "" {
			if err != nil {
				t.Errorf("%+v: %v", c.in, err)
			}
			continue
		}
		if err == nil || !errors.Is(err, ErrBadRequest) || !strings.Contains(err.Error(), c.wantErr) {
			t.Errorf("%+v: err = %v, want %q", c.in, err, c.wantErr)
		}
	}
}
