package setup

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"

	"github.com/cicord/cicord/internal/discord"
)

// ErrReadOnly is returned by every mutating call of a ReadOnlyClient: an inspection never
// changes anything on Discord.
var ErrReadOnly = errors.New("read-only inspection: changes on Discord are disabled")

// ReadOnlyClient wraps a Discord client for an inspection: the read calls (Get*, List*)
// go through and are counted, every other call fails with ErrReadOnly before reaching
// Discord and is counted as blocked. Every method is written out (no embedding), so a
// method added to discord.Client does not compile until it is classified here.
type ReadOnlyClient struct {
	c       discord.Client
	reads   atomic.Int64
	blocked atomic.Int64
}

var _ discord.Client = (*ReadOnlyClient)(nil)

// ReadOnly returns a read-only view of c.
func ReadOnly(c discord.Client) *ReadOnlyClient { return &ReadOnlyClient{c: c} }

// Reads is the number of read calls made.
func (r *ReadOnlyClient) Reads() int { return int(r.reads.Load()) }

// Blocked is the number of mutating calls refused (always 0 in a correct inspection).
func (r *ReadOnlyClient) Blocked() int { return int(r.blocked.Load()) }

func (r *ReadOnlyClient) read() discord.Client { r.reads.Add(1); return r.c }

func (r *ReadOnlyClient) deny(method string) error {
	r.blocked.Add(1)
	return fmt.Errorf("%s: %w", method, ErrReadOnly)
}

// ───────────────────────────────────────────────────────────── reads

func (r *ReadOnlyClient) GetUser(ctx context.Context, userID string) (discord.User, error) {
	return r.read().GetUser(ctx, userID)
}
func (r *ReadOnlyClient) GetServer(ctx context.Context, serverID string) (discord.Server, error) {
	return r.read().GetServer(ctx, serverID)
}
func (r *ReadOnlyClient) GetServerWidget(ctx context.Context, serverID string) (discord.ServerWidget, error) {
	return r.read().GetServerWidget(ctx, serverID)
}
func (r *ReadOnlyClient) ListAutoModRules(ctx context.Context, serverID string) ([]discord.AutoModRule, error) {
	return r.read().ListAutoModRules(ctx, serverID)
}
func (r *ReadOnlyClient) GetOnboarding(ctx context.Context, serverID string) (discord.Onboarding, error) {
	return r.read().GetOnboarding(ctx, serverID)
}
func (r *ReadOnlyClient) GetWelcomeScreen(ctx context.Context, serverID string) (discord.WelcomeScreen, error) {
	return r.read().GetWelcomeScreen(ctx, serverID)
}
func (r *ReadOnlyClient) ListEmojis(ctx context.Context, serverID string) ([]discord.Emoji, error) {
	return r.read().ListEmojis(ctx, serverID)
}
func (r *ReadOnlyClient) ListStickers(ctx context.Context, serverID string) ([]discord.Sticker, error) {
	return r.read().ListStickers(ctx, serverID)
}
func (r *ReadOnlyClient) ListSoundboardSounds(ctx context.Context, serverID string) ([]discord.SoundboardSound, error) {
	return r.read().ListSoundboardSounds(ctx, serverID)
}
func (r *ReadOnlyClient) ListRoles(ctx context.Context, serverID string) ([]discord.Role, error) {
	return r.read().ListRoles(ctx, serverID)
}
func (r *ReadOnlyClient) ListChannels(ctx context.Context, serverID string) ([]discord.Channel, error) {
	return r.read().ListChannels(ctx, serverID)
}
func (r *ReadOnlyClient) GetChannel(ctx context.Context, channelID string) (discord.Channel, error) {
	return r.read().GetChannel(ctx, channelID)
}
func (r *ReadOnlyClient) GetMember(ctx context.Context, serverID, userID string) (discord.Member, error) {
	return r.read().GetMember(ctx, serverID, userID)
}
func (r *ReadOnlyClient) ListMessages(ctx context.Context, channelID string, p discord.ListMessagesParams) ([]discord.Message, error) {
	return r.read().ListMessages(ctx, channelID, p)
}
func (r *ReadOnlyClient) GetMessage(ctx context.Context, channelID, messageID string) (discord.Message, error) {
	return r.read().GetMessage(ctx, channelID, messageID)
}

// ───────────────────────────────────────────────────────────── writes (refused)

func (r *ReadOnlyClient) UpdateSelf(context.Context, discord.SelfParams) (discord.User, error) {
	return discord.User{}, r.deny("UpdateSelf")
}
func (r *ReadOnlyClient) UpdateServer(context.Context, string, discord.ServerParams) (discord.Server, error) {
	return discord.Server{}, r.deny("UpdateServer")
}
func (r *ReadOnlyClient) UpdateServerWidget(context.Context, string, discord.ServerWidget, string) (discord.ServerWidget, error) {
	return discord.ServerWidget{}, r.deny("UpdateServerWidget")
}
func (r *ReadOnlyClient) CreateAutoModRule(context.Context, string, discord.AutoModRule, string) (discord.AutoModRule, error) {
	return discord.AutoModRule{}, r.deny("CreateAutoModRule")
}
func (r *ReadOnlyClient) UpdateAutoModRule(context.Context, string, string, discord.AutoModRule, string) (discord.AutoModRule, error) {
	return discord.AutoModRule{}, r.deny("UpdateAutoModRule")
}
func (r *ReadOnlyClient) DeleteAutoModRule(context.Context, string, string, string) error {
	return r.deny("DeleteAutoModRule")
}
func (r *ReadOnlyClient) UpdateOnboarding(context.Context, string, discord.Onboarding, string) (discord.Onboarding, error) {
	return discord.Onboarding{}, r.deny("UpdateOnboarding")
}
func (r *ReadOnlyClient) UpdateWelcomeScreen(context.Context, string, discord.WelcomeScreen, string) (discord.WelcomeScreen, error) {
	return discord.WelcomeScreen{}, r.deny("UpdateWelcomeScreen")
}
func (r *ReadOnlyClient) CreateEmoji(context.Context, string, discord.EmojiParams, string) (discord.Emoji, error) {
	return discord.Emoji{}, r.deny("CreateEmoji")
}
func (r *ReadOnlyClient) UpdateEmoji(context.Context, string, string, discord.EmojiParams, string) (discord.Emoji, error) {
	return discord.Emoji{}, r.deny("UpdateEmoji")
}
func (r *ReadOnlyClient) DeleteEmoji(context.Context, string, string, string) error {
	return r.deny("DeleteEmoji")
}
func (r *ReadOnlyClient) CreateSticker(context.Context, string, discord.StickerParams, string) (discord.Sticker, error) {
	return discord.Sticker{}, r.deny("CreateSticker")
}
func (r *ReadOnlyClient) UpdateSticker(context.Context, string, string, discord.StickerParams, string) (discord.Sticker, error) {
	return discord.Sticker{}, r.deny("UpdateSticker")
}
func (r *ReadOnlyClient) DeleteSticker(context.Context, string, string, string) error {
	return r.deny("DeleteSticker")
}
func (r *ReadOnlyClient) CreateSoundboardSound(context.Context, string, discord.SoundParams, string) (discord.SoundboardSound, error) {
	return discord.SoundboardSound{}, r.deny("CreateSoundboardSound")
}
func (r *ReadOnlyClient) UpdateSoundboardSound(context.Context, string, string, discord.SoundParams, string) (discord.SoundboardSound, error) {
	return discord.SoundboardSound{}, r.deny("UpdateSoundboardSound")
}
func (r *ReadOnlyClient) DeleteSoundboardSound(context.Context, string, string, string) error {
	return r.deny("DeleteSoundboardSound")
}
func (r *ReadOnlyClient) CreateRole(context.Context, string, discord.RoleParams, string) (discord.Role, error) {
	return discord.Role{}, r.deny("CreateRole")
}
func (r *ReadOnlyClient) UpdateRole(context.Context, string, string, discord.RoleParams, string) (discord.Role, error) {
	return discord.Role{}, r.deny("UpdateRole")
}
func (r *ReadOnlyClient) DeleteRole(context.Context, string, string, string) error {
	return r.deny("DeleteRole")
}
func (r *ReadOnlyClient) SetRolePositions(context.Context, string, []discord.RolePosition) error {
	return r.deny("SetRolePositions")
}
func (r *ReadOnlyClient) CreateChannel(context.Context, string, discord.ChannelParams, string) (discord.Channel, error) {
	return discord.Channel{}, r.deny("CreateChannel")
}
func (r *ReadOnlyClient) UpdateChannel(context.Context, string, discord.ChannelParams, string) (discord.Channel, error) {
	return discord.Channel{}, r.deny("UpdateChannel")
}
func (r *ReadOnlyClient) DeleteChannel(context.Context, string, string) error {
	return r.deny("DeleteChannel")
}
func (r *ReadOnlyClient) SetChannelPositions(context.Context, string, []discord.ChannelPosition) error {
	return r.deny("SetChannelPositions")
}
func (r *ReadOnlyClient) CreateThread(context.Context, string, discord.ThreadParams) (discord.Channel, error) {
	return discord.Channel{}, r.deny("CreateThread")
}
func (r *ReadOnlyClient) UpdateMember(context.Context, string, string, discord.MemberParams, string) (discord.Member, error) {
	return discord.Member{}, r.deny("UpdateMember")
}
func (r *ReadOnlyClient) AddMemberRole(context.Context, string, string, string, string) error {
	return r.deny("AddMemberRole")
}
func (r *ReadOnlyClient) RemoveMemberRole(context.Context, string, string, string, string) error {
	return r.deny("RemoveMemberRole")
}
func (r *ReadOnlyClient) KickMember(context.Context, string, string, string) error {
	return r.deny("KickMember")
}
func (r *ReadOnlyClient) ListBans(ctx context.Context, serverID, after string, limit int) ([]discord.Ban, error) {
	return r.read().ListBans(ctx, serverID, after, limit)
}
func (r *ReadOnlyClient) UnbanMember(context.Context, string, string, string) error {
	return r.deny("UnbanMember")
}
func (r *ReadOnlyClient) BanMember(context.Context, string, string, int, string) error {
	return r.deny("BanMember")
}
func (r *ReadOnlyClient) CreateDM(context.Context, string) (discord.Channel, error) {
	return discord.Channel{}, r.deny("CreateDM")
}
func (r *ReadOnlyClient) SendMessage(context.Context, string, discord.MessageCreate) (discord.Message, error) {
	return discord.Message{}, r.deny("SendMessage")
}
func (r *ReadOnlyClient) EditMessage(context.Context, string, string, discord.MessageCreate) (discord.Message, error) {
	return discord.Message{}, r.deny("EditMessage")
}
func (r *ReadOnlyClient) BulkDeleteMessages(context.Context, string, []string, string) error {
	return r.deny("BulkDeleteMessages")
}
func (r *ReadOnlyClient) DeleteMessage(context.Context, string, string, string) error {
	return r.deny("DeleteMessage")
}
func (r *ReadOnlyClient) AddReaction(context.Context, string, string, string) error {
	return r.deny("AddReaction")
}
func (r *ReadOnlyClient) RemoveOwnReaction(context.Context, string, string, string) error {
	return r.deny("RemoveOwnReaction")
}
func (r *ReadOnlyClient) SetServerCommands(context.Context, string, string, []discord.Command) error {
	return r.deny("SetServerCommands")
}
func (r *ReadOnlyClient) RespondInteraction(context.Context, string, string, discord.InteractionResponse) error {
	return r.deny("RespondInteraction")
}
func (r *ReadOnlyClient) EditOriginalResponse(context.Context, string, string, discord.MessageCreate) (discord.Message, error) {
	return discord.Message{}, r.deny("EditOriginalResponse")
}
func (r *ReadOnlyClient) CreateFollowup(context.Context, string, string, discord.MessageCreate) (discord.Message, error) {
	return discord.Message{}, r.deny("CreateFollowup")
}
