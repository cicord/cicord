package discord

// PublicChannels resolves names only for channels visible to everyone.
func PublicChannels(roles []Role, channels []Channel, serverID string) func(string) (string, bool) {
	var everyone Permissions
	for _, ro := range roles {
		if ro.ID == serverID {
			everyone = ro.Permissions
		}
	}
	byID := make(map[string]Channel, len(channels))
	for _, c := range channels {
		byID[c.ID] = c
	}
	view := MustPermissions("ViewChannel")
	visible := func(c Channel) bool {
		p := everyone
		for _, o := range c.Overwrites {
			if o.ID == serverID {
				p = (p &^ o.Deny) | o.Allow
			}
		}
		return p&view != 0
	}
	return func(id string) (string, bool) {
		c, ok := byID[id]
		if !ok {
			return "", false
		}
		if c.Type == ChannelPrivateThread {
			return "", false // invited members only, whatever its parent
		}
		target := c
		if c.Type.IsThread() {
			if parent, ok := byID[c.ParentID]; ok {
				target = parent
			} else {
				return "", false
			}
		}
		if !visible(target) {
			return "", false
		}
		return c.Name, true
	}
}
