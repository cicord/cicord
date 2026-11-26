package discord

// Structures shared by message layouts, entity select menus and richer modals.
const (
	ComponentUserSelect        ComponentType = 5
	ComponentRoleSelect        ComponentType = 6
	ComponentMentionableSelect ComponentType = 7
	ComponentChannelSelect     ComponentType = 8
	ComponentSection           ComponentType = 9
	ComponentText              ComponentType = 10
	ComponentThumbnail         ComponentType = 11
	ComponentMediaGallery      ComponentType = 12
	ComponentFile              ComponentType = 13
	ComponentSeparator         ComponentType = 14
	ComponentContainer         ComponentType = 17
	ComponentLabel             ComponentType = 18
	MessageFlagComponentsV2    MessageFlags  = 1 << 15
)

func (t ComponentType) EntitySelect() bool {
	return t >= ComponentUserSelect && t <= ComponentChannelSelect
}

type SelectDefault struct {
	ID   string `json:"id"`
	Type string `json:"type"`
}
type ComponentMedia struct {
	URL string `json:"url"`
}
type GalleryItem struct {
	Media       ComponentMedia `json:"media"`
	Description string         `json:"description,omitempty"`
	Spoiler     bool           `json:"spoiler,omitempty"`
}
type ComponentPresentation struct {
	Content       string          `json:"content,omitempty"`
	Description   string          `json:"description,omitempty"`
	Media         *ComponentMedia `json:"media,omitempty"`
	File          *ComponentMedia `json:"file,omitempty"`
	Items         []GalleryItem   `json:"items,omitempty"`
	Divider       *bool           `json:"divider,omitempty"`
	Spacing       int             `json:"spacing,omitempty"`
	AccentColor   *int            `json:"accent_color,omitempty"`
	Spoiler       bool            `json:"spoiler,omitempty"`
	DefaultValues []SelectDefault `json:"default_values,omitempty"`
	ChannelTypes  []ChannelType   `json:"channel_types,omitempty"`
}
