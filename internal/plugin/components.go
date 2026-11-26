package plugin

import (
	"fmt"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/cicord/cicord/internal/discord"
)

var entityTypes = map[string]discord.ComponentType{"userSelect": discord.ComponentUserSelect, "roleSelect": discord.ComponentRoleSelect, "channelSelect": discord.ComponentChannelSelect, "mentionableSelect": discord.ComponentMentionableSelect}

func (i *Instance) entitySelect(c compIn, modal bool) (discord.Component, error) {
	out := discord.Component{Type: entityTypes[c.Type], CustomID: c.ID, Placeholder: c.Placeholder, MinValues: c.MinValues, MaxValues: c.MaxValues, Disabled: c.Disabled}
	if !modal {
		id, err := EncodeCustomID(i.env.InstanceName, c.ID)
		if err != nil {
			return out, err
		}
		out.CustomID = id
	}
	minV, maxV := 1, 1
	if c.MinValues != nil {
		minV = *c.MinValues
	}
	if c.MaxValues != nil {
		maxV = *c.MaxValues
	}
	if minV < 0 || maxV < 1 || maxV > 25 || minV > maxV || utf8.RuneCountInString(c.Placeholder) > 150 {
		return out, fmt.Errorf("invalid select bounds or placeholder")
	}
	if len(c.DefaultValues) > 0 && (len(c.DefaultValues) < minV || len(c.DefaultValues) > maxV) {
		return out, fmt.Errorf("defaultValues must fit select bounds")
	}
	seen := map[string]bool{}
	for _, v := range c.DefaultValues {
		allowed := c.Type == "userSelect" && v.Type == "user" || c.Type == "roleSelect" && v.Type == "role" || c.Type == "channelSelect" && v.Type == "channel" || c.Type == "mentionableSelect" && (v.Type == "role" || v.Type == "user")
		if !allowed || !discord.IsSnowflake(v.ID) || seen[v.ID] {
			return out, fmt.Errorf("invalid default selection %q", v.ID)
		}
		seen[v.ID] = true
	}
	out.DefaultValues = c.DefaultValues
	for _, name := range c.ChannelTypes {
		if c.Type != "channelSelect" {
			return out, fmt.Errorf("channelTypes is only available on channelSelect")
		}
		if name == "thread" {
			out.ChannelTypes = append(out.ChannelTypes, discord.ChannelPublicThread, discord.ChannelPrivateThread, discord.ChannelAnnouncementThread)
			continue
		}
		t, err := discord.ParseChannelType(name)
		if err != nil {
			return out, err
		}
		out.ChannelTypes = append(out.ChannelTypes, t)
	}
	return out, nil
}

func validMedia(media *discord.ComponentMedia, file bool) bool {
	if media == nil {
		return false
	}
	u, err := url.Parse(media.URL)
	if err != nil {
		return false
	}
	if u.Scheme == "attachment" {
		return u.Host != ""
	}
	return !file && (u.Scheme == "https" || u.Scheme == "http") && u.Host != ""
}

func (i *Instance) layoutComponent(c compIn, parent string, count *int) (discord.Component, error) {
	var out discord.Component
	*count++
	if *count > 40 {
		return out, fmt.Errorf("at most 40 components")
	}
	if parent == "section" && c.Type != "text" {
		return out, fmt.Errorf("section children must be text")
	}
	if parent == "accessory" && c.Type != "button" && c.Type != "link" && c.Type != "thumbnail" {
		return out, fmt.Errorf("section accessory must be a thumbnail or button")
	}
	switch c.Type {
	case "row":
		row, err := i.convertRow(rowIn{Type: "row", Components: c.Components})
		*count += len(c.Components)
		if *count > 40 {
			return out, fmt.Errorf("at most 40 components")
		}
		return row, err
	case "button", "link":
		if parent != "accessory" {
			return out, fmt.Errorf("button needs a row or section")
		}
		row, err := i.convertRow(rowIn{Type: "row", Components: []compIn{c}})
		if err != nil {
			return out, err
		}
		return row.Components[0], nil
	case "container", "section":
		if c.Type == "container" && parent != "message" {
			return out, fmt.Errorf("container must be top-level")
		}
		if len(c.Components) < 1 || c.Type == "section" && len(c.Components) > 3 {
			return out, fmt.Errorf("invalid %s child count", c.Type)
		}
		if c.Type == "container" {
			out.Type = discord.ComponentContainer
			out.Spoiler = c.Spoiler
			if c.AccentColor != "" {
				color, err := ParseColor(c.AccentColor)
				if err != nil {
					return out, err
				}
				out.AccentColor = &color
			}
		} else {
			out.Type = discord.ComponentSection
			if c.Accessory == nil {
				return out, fmt.Errorf("section needs an accessory")
			}
			acc, err := i.layoutComponent(*c.Accessory, "accessory", count)
			if err != nil {
				return out, err
			}
			out.Accessory = &acc
		}
		for _, child := range c.Components {
			item, err := i.layoutComponent(child, c.Type, count)
			if err != nil {
				return out, err
			}
			out.Components = append(out.Components, item)
		}
	case "text":
		out.Type = discord.ComponentText
		out.Content = c.Content
		if n := utf8.RuneCountInString(c.Content); n < 1 || n > 4000 {
			return out, fmt.Errorf("text content must be 1–4000 characters")
		}
	case "thumbnail":
		if parent != "accessory" || !validMedia(c.Media, false) {
			return out, fmt.Errorf("invalid thumbnail")
		}
		out.Type = discord.ComponentThumbnail
		out.Media = c.Media
		out.Description = c.Description
		out.Spoiler = c.Spoiler
	case "mediaGallery":
		if len(c.Items) < 1 || len(c.Items) > 10 {
			return out, fmt.Errorf("mediaGallery expects 1–10 items")
		}
		for _, item := range c.Items {
			if !validMedia(&item.Media, false) || utf8.RuneCountInString(item.Description) > 1024 {
				return out, fmt.Errorf("invalid gallery item")
			}
		}
		out.Type = discord.ComponentMediaGallery
		out.Items = c.Items
	case "file":
		if !validMedia(c.File, true) {
			return out, fmt.Errorf("file requires an attachment:// URL")
		}
		out.Type = discord.ComponentFile
		out.File = c.File
		out.Spoiler = c.Spoiler
	case "separator":
		if c.Spacing < 0 || c.Spacing > 2 {
			return out, fmt.Errorf("separator spacing must be 1 or 2")
		}
		out.Type = discord.ComponentSeparator
		out.Divider = c.Divider
		out.Spacing = c.Spacing
	default:
		return out, fmt.Errorf("unknown layout component %q", c.Type)
	}
	return out, nil
}

func (i *Instance) modalLabel(label compIn) (discord.Component, error) {
	out := discord.Component{Type: discord.ComponentLabel, Label: label.Label}
	out.Description = label.Description
	c := label.Component
	if c == nil || len(c.ID) == 0 || len(c.ID) > MaxCustomIDLength || label.Label == "" || utf8.RuneCountInString(label.Label) > 45 || utf8.RuneCountInString(label.Description) > 100 {
		return out, fmt.Errorf("invalid modal label or component ID")
	}
	var child discord.Component
	var err error
	switch {
	case c.Type == "textInput":
		style := 1
		if c.Style == "paragraph" {
			style = 2
		} else if c.Style != "" && c.Style != "short" {
			return out, fmt.Errorf("invalid text style")
		}
		child = discord.Component{Type: discord.ComponentTextInput, CustomID: c.ID, TextStyle: style, Value: c.Value, Placeholder: c.Placeholder, MinLength: c.MinLength, MaxLength: c.MaxLength}
	case c.Type == "select":
		// Modal field ids are local to the modal and are delivered unqualified.
		temp := *c
		temp.ID = "input"
		row, e := i.convertRow(rowIn{Type: "row", Components: []compIn{temp}})
		if e != nil {
			return out, e
		}
		child = row.Components[0]
		child.CustomID = c.ID
	case entityTypes[c.Type] != 0:
		child, err = i.entitySelect(*c, true)
		if err != nil {
			return out, err
		}
	default:
		return out, fmt.Errorf("unsupported label input %q", c.Type)
	}
	required := c.Required == nil || *c.Required
	child.Required = required
	child.ModalRequired = &required
	if child.Disabled || strings.TrimSpace(child.CustomID) == "" {
		return out, fmt.Errorf("invalid modal input")
	}
	out.Components = []discord.Component{child}
	return out, nil
}
