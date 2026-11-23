package discord

// Application flags (https://discord.com/developers/docs/resources/application#application-object-application-flags).
// A *_LIMITED flag means the intent is enabled for an application in fewer than 100
// servers (no verification needed): it counts as enabled.
const (
	FlagGatewayPresence              = 1 << 12
	FlagGatewayPresenceLimited       = 1 << 13
	FlagGatewayGuildMembers          = 1 << 14
	FlagGatewayGuildMembersLimited   = 1 << 15
	FlagGatewayMessageContent        = 1 << 18
	FlagGatewayMessageContentLimited = 1 << 19
)

// IntentsFromFlags returns the privileged intents enabled for an application (the
// "Privileged Gateway Intents" toggles of the developer portal) from its flags.
func IntentsFromFlags(flags int) Intents {
	var in Intents
	if flags&(FlagGatewayGuildMembers|FlagGatewayGuildMembersLimited) != 0 {
		in |= MustIntents("ServerMembers")
	}
	if flags&(FlagGatewayPresence|FlagGatewayPresenceLimited) != 0 {
		in |= MustIntents("ServerPresences")
	}
	if flags&(FlagGatewayMessageContent|FlagGatewayMessageContentLimited) != 0 {
		in |= MustIntents("MessageContent")
	}
	return in
}

// FlagsFromIntents is the inverse of IntentsFromFlags (non-limited flags; simulator).
func FlagsFromIntents(in Intents) int {
	f := 0
	if in.Has(MustIntents("ServerMembers")) {
		f |= FlagGatewayGuildMembers
	}
	if in.Has(MustIntents("ServerPresences")) {
		f |= FlagGatewayPresence
	}
	if in.Has(MustIntents("MessageContent")) {
		f |= FlagGatewayMessageContent
	}
	return f
}

// ApplicationFlagsReader is implemented by sessions that know the flags of their
// application (read by Connect from GET /oauth2/applications/@me). The runtime uses it
// to tell which privileged intents are really enabled before opening the gateway.
type ApplicationFlagsReader interface {
	ApplicationFlags() int
}

// PortalBotURL is the developer portal page of an application's bot (token and
// privileged intents).
func PortalBotURL(appID string) string {
	if appID == "" {
		return "https://discord.com/developers/applications"
	}
	return "https://discord.com/developers/applications/" + appID + "/bot"
}
