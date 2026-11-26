package discord

import (
	"maps"
	"slices"
	"unicode/utf8"
)

// ValidMenuName is shared by the canonical name and its translations. Discord
// permits mixed case and spaces in both USER and MESSAGE command names.
func ValidMenuName(name string) bool {
	n := utf8.RuneCountInString(name)
	return n >= 1 && n <= 32
}

// MenuNameCollision identifies two different canonical commands that would be
// indistinguishable in one localized user or message menu.
type MenuNameCollision struct {
	First, Second int
	Locale, Name  string
}

// LocalizedMenuCollision checks each translated locale, including the default
// displayed when a menu has no translation. Command types have separate menus.
// Ordinary duplicate canonical names are checked by command registration.
func LocalizedMenuCollision(menus []Command) *MenuNameCollision {
	tags := map[string]bool{}
	for _, menu := range menus {
		for tag := range menu.Names {
			tags[tag] = true
		}
	}
	for _, tag := range slices.Sorted(maps.Keys(tags)) {
		seen := map[string]int{}
		for i, menu := range menus {
			if menu.Type != UserMenu && menu.Type != MessageMenu {
				continue
			}
			name := menu.Names[tag]
			if name == "" {
				name = menu.Name
			}
			key := menu.Type.Key(name)
			if j, exists := seen[key]; exists && menus[j].Name != menu.Name {
				return &MenuNameCollision{First: j, Second: i, Locale: tag, Name: name}
			}
			seen[key] = i
		}
	}
	return nil
}
