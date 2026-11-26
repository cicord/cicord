package config

// GatewayIntents returns the names of the gateway intents the runtime requests for a
// bot: Servers, the bot's spec.intents, its plugins' intents, and the intents of the
// live updates the reconciler reacts to for the servers it lists (AutoMod rule events
// when spec.automod is declared, expression events for emojis, stickers and
// soundboard sounds). Names may repeat.
func (r *Repo) GatewayIntents(bot string) []string {
	names := []string{"Servers"}
	b := r.Bots[bot]
	if b == nil {
		return names
	}
	names = append(names, b.Spec.Intents...)
	for _, in := range r.Instances {
		if in.Bot == bot {
			names = append(names, in.Plugin.Manifest.Spec.Intents...)
		}
	}
	for _, key := range b.Spec.ServerKeys() {
		srv := r.Servers[key]
		if srv == nil {
			continue
		}
		sp := &srv.Spec
		if len(sp.AutoMod) > 0 {
			names = append(names, "AutoModerationConfiguration")
		}
		if len(sp.Emojis) > 0 || len(sp.Stickers) > 0 || len(sp.Soundboard) > 0 {
			names = append(names, "ServerExpressions")
		}
	}
	return names
}
