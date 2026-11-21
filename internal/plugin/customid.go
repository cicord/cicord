package plugin

import (
	"fmt"
	"strings"
)

// MaxCustomIDLength is Discord's limit for component and modal custom IDs.
const MaxCustomIDLength = 100

const customIDPrefix = "c:"

// EncodeCustomID namespaces a plugin-local component or modal id so the bot host can route
// the resulting interaction back to the instance: "c:<instance>:<localID>".
//
// Instance names are keys (no ':'); localID may contain any character, including ':'.
// IDs longer than MaxCustomIDLength are an error (never truncated: a truncated ID would be
// routed to the wrong handler). The SDK surfaces this error to the plugin when it sends the
// component.
func EncodeCustomID(instance, localID string) (string, error) {
	if instance == "" || strings.Contains(instance, ":") {
		return "", fmt.Errorf("invalid instance name %q", instance)
	}
	if localID == "" {
		return "", fmt.Errorf("component id must not be empty")
	}
	id := customIDPrefix + instance + ":" + localID
	if len(id) > MaxCustomIDLength {
		return "", fmt.Errorf("component id %q is too long: at most %d bytes for instance %q (Discord custom_id limit is %d)",
			localID, MaxLocalIDLength(instance), instance, MaxCustomIDLength)
	}
	return id, nil
}

// MaxLocalIDLength is the longest local id EncodeCustomID accepts for an instance.
func MaxLocalIDLength(instance string) int {
	return MaxCustomIDLength - len(customIDPrefix) - len(instance) - 1
}

// ParseCustomID reverses EncodeCustomID. ok is false for custom IDs that were not produced
// by EncodeCustomID (e.g. components sent by something else).
func ParseCustomID(customID string) (instance, localID string, ok bool) {
	rest, found := strings.CutPrefix(customID, customIDPrefix)
	if !found {
		return "", "", false
	}
	instance, localID, found = strings.Cut(rest, ":")
	if !found || instance == "" || localID == "" {
		return "", "", false
	}
	return instance, localID, true
}
