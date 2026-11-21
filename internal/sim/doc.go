// Package sim is an in-memory Discord simulator.
//
// It implements discord.Connector and discord.Session (hence discord.Client) entirely
// in-process, so the reconciler, the bot host, plugins, end-to-end tests and
// `codi dev --sim` can run without real Discord. Its behaviour follows the real API
// closely enough that code tested against it should also work against the real API.
// That covers permissions and role hierarchy, gateway intents, message-content gating,
// position normalization and the interaction lifecycle with its 3 s deadline.
//
// # Typical test
//
//	w := sim.NewWorld()
//	owner := w.CreateUser("owner")
//	alice := w.CreateUser("alice")
//	token, bot, appID := w.CreateBot("codi")
//	srv := w.CreateServer("Test", owner.ID, sim.WithGeneralChannel())
//	_ = w.AddMember(srv.ID, alice.ID)
//	_, _ = w.AddBotToServer(srv.ID, bot.ID, discord.MustPermissions("Administrator"))
//
//	sess, _ := w.Connect(ctx, token)            // *World is a discord.Connector
//	_ = sess.Open(ctx, discord.MustIntents("Servers", "ServerMessages"), handler)
//	_ = sess.SetServerCommands(ctx, appID, srv.ID, cmds)
//
//	general, _ := w.ChannelByName(srv.ID, "general")
//	h := w.InvokeCommand(alice.ID, srv.ID, general.ID, appID, "ping", nil)
//	st := h.Wait()                              // initial response or deadline failure
//	// st.ResponseType, st.Message.Content, st.Ephemeral, st.Failed ...
//
//	w.SendMessage(alice.ID, general.ID, "hello")
//	w.WaitIdle()                                // every queued event handled
//	msgs := w.Messages(general.ID)
//
// # Concepts
//
// A World holds users, bots (token, bot user, application ID), servers, channels,
// messages, application commands and interactions. A single mutex guards all of it,
// so every method is safe for concurrent use.
//
// Each opened Session has an unbounded event queue and one dispatcher goroutine, which
// calls the handler sequentially. A REST call made from a handler (for example the bot
// replying to a message) enqueues further events. WaitIdle blocks until every queue is
// empty and no handler is running, cascades included. Never call WaitIdle from inside
// a handler: it would wait for itself. In tests, report failures from inside handlers
// with t.Error, not t.Fatal: t.Fatal stops the dispatcher goroutine, and the sim then
// closes that session.
//
// "Human" actions such as SendMessage, InvokeCommand, ClickButton, Select, SubmitModal
// and AddReaction act as a regular user in the Discord client and emit the
// corresponding gateway events. They check the user's permissions like Discord does.
//
// Admin* methods act as the server owner, as if someone clicked around in the Discord
// client. They bypass permission checks and emit gateway events, which makes them the
// tool for drift tests. UserClient returns a full discord.Client acting as an arbitrary
// user, with that user's permissions.
//
// Every mutating call is recorded in a call log (Calls, CountCalls), tagged with the
// actor's user ID. Failed calls are recorded too, with their error.
//
// # Interactions
//
// InvokeCommand, ClickButton, Select and SubmitModal return an *InteractionHandle at
// once. Validation errors (unknown command, missing option, disabled button …) are
// reported by Err(). The handle exposes the interaction's state: the initial response
// type, the original response message (kept current through edits), the ephemeral flag,
// followups, the modal, and whether the interaction failed because nobody acknowledged
// it within the deadline (3 s by default, see WithInteractionDeadline). The deadline is
// measured in real time, even when WithClock is used.
//
// # Server settings, boosts and Community
//
// Servers start like a new Discord server: boost level 0, no feature flags, explicit
// content filter disabled, AFK timeout 300 s, locale en-US. SetBoosts(serverID, n) sets
// the boost count, the boost level (2 → 1, 7 → 2, 14 → 3) and the flags it unlocks
// (ANIMATED_ICON, INVITE_SPLASH, BANNER, ROLE_ICONS, ENHANCED_ROLE_COLORS, …);
// SetFeatures adds arbitrary flags (e.g. PARTNERED). Boost-gated settings (banner,
// splash, animated icon/banner, role icons, gradient role colors) fail with
// "… requires boost level N", expression slots follow the level (emojis 50/100/150/250
// static and as many animated, stickers 5/15/30/60, sounds 8/24/36/48).
//
// Community follows Discord: enabling COMMUNITY (ServerParams.Features) requires rules
// and community updates channels, verification ≥ low and the explicit content filter
// on allMembers (checked again when those settings change); rules / updates / safety
// alerts channels, announcement and stage channels, the welcome screen and native
// onboarding fail with "… requires the Community feature" without it. Enabling
// onboarding needs 7 default channels, 5 of which @everyone can send messages in
// (advanced mode also counts the channels of the questions). AutoMod rules follow
// Discord's limits (rules per trigger, keyword/regex/allow-list sizes, actions per
// trigger, timeout ≤ 4 weeks with ModerateMembers).
//
// `codi dev --sim` honors two Server annotations: sim.codi.dev/boosts ("14") and
// sim.codi.dev/features ("PARTNERED,VERIFIED"); POST /api/v1/sim/servers/{id}/boosts
// with {"count": n} changes the boosts of a running simulator.
//
// # Errors
//
// Errors wrap discord.ErrForbidden, discord.ErrNotFound, discord.ErrBadRequest or
// discord.ErrInvalidToken, so callers can use errors.Is.
package sim
