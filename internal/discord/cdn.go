package discord

// CDN locations of the files behind Discord objects
// (https://discord.com/developers/docs/reference#image-formatting). Server, role and
// user images carry their URL (Server.IconURL…); expressions are addressed by ID.
const (
	CDNBase   = "https://cdn.discordapp.com"
	MediaBase = "https://media.discordapp.net"
)

// ImageURL is the CDN URL of the emoji image (GIF when animated, PNG otherwise).
func (e Emoji) ImageURL() string {
	if e.Animated {
		return CDNBase + "/emojis/" + e.ID + ".gif"
	}
	return CDNBase + "/emojis/" + e.ID + ".png"
}

// FileURL is the CDN URL of the sticker file: PNG (also for APNG), a Lottie JSON, or
// a GIF (served from the media proxy).
func (s Sticker) FileURL() string {
	switch s.Format {
	case "lottie":
		return CDNBase + "/stickers/" + s.ID + ".json"
	case "gif":
		return MediaBase + "/stickers/" + s.ID + ".gif"
	}
	return CDNBase + "/stickers/" + s.ID + ".png"
}

// FileURL is the CDN URL of the sound file (MP3 or OGG, no extension).
func (s SoundboardSound) FileURL() string {
	return CDNBase + "/soundboard-sounds/" + s.ID
}
