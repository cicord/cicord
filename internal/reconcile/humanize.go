package reconcile

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// HumanError is an error explained for people (https://cicord.dev/docs/reference/http-api/, "Error object"): a stable
// machine code, one sentence saying what went wrong, an optional hint saying how to fix
// it, and the raw technical text kept for copy/paste. The admin UI shows message + hint
// and keeps details collapsed.
type HumanError struct {
	// Code is a stable snake_case identifier (see the catalog below), "unknown" when the
	// error is not recognized.
	Code    string `json:"code"`
	Message string `json:"message"`
	Hint    string `json:"hint,omitempty"`
	// Details is the raw error text.
	Details string `json:"details,omitempty"`
	// DiscordCode / HTTPStatus are Discord's JSON error code and HTTP status, when the
	// error came from the Discord REST API.
	DiscordCode int `json:"discordCode,omitempty"`
	HTTPStatus  int `json:"httpStatus,omitempty"`
	// Resource is the planned change that failed, when known.
	Resource *ErrorResource `json:"resource,omitempty"`
	// Fields summarizes Discord's "Invalid Form Body" field errors.
	Fields []FieldError `json:"fields,omitempty"`
	// Failures is the number of changes that failed in the same sync (when more than one).
	Failures int `json:"failures,omitempty"`
}

// ErrorResource identifies the change an error is about.
type ErrorResource struct {
	Action string `json:"action"`
	Kind   string `json:"kind"`
	Key    string `json:"key,omitempty"`
}

// FieldError is one leaf of Discord's `errors` tree ({"type": {"_errors": [...]}}).
type FieldError struct {
	Path    string `json:"path"`
	Code    string `json:"code,omitempty"`
	Message string `json:"message"`
}

// Error codes of the catalog.
const (
	ErrCodeUnknown            = "unknown"
	ErrCodeCommunityRequired  = "community_required"
	ErrCodeBoostRequired      = "boost_required"
	ErrCodeMissingPermissions = "missing_permissions"
	ErrCodeMissingAccess      = "missing_access"
	ErrCodeRoleHierarchy      = "role_hierarchy"
	ErrCodeUnknownResource    = "unknown_resource"
	ErrCodeInvalidFormBody    = "invalid_form_body"
	ErrCodeLimitReached       = "limit_reached"
	ErrCodeInvalidToken       = "invalid_token"
	ErrCodeIntentsDisallowed  = "intents_disallowed"
	ErrCodeRateLimited        = "rate_limited"
	ErrCodeDiscordUnavailable = "discord_unavailable"
	ErrCodeTimeout            = "timeout"
	ErrCodeBotNotConnected    = "bot_not_connected"
	ErrCodeNoBot              = "no_bot"
	ErrCodeUnresolvedRef      = "unresolved_reference"
	ErrCodeRevisionRejected   = "revision_rejected"
	ErrCodeFetchFailed        = "fetch_failed"
	ErrCodeBadRequest         = "bad_request"

	// Blocked plan operations (ExplainBlocked).
	ErrCodeDeletionProtection = "deletion_protection"
	ErrCodeOwnership          = "ownership"
	ErrCodeManagedRole        = "managed_role"
	ErrCodeTypeChange         = "type_change"
	ErrCodeRecreateRequired   = "recreate_required"
	ErrCodeUndeletable        = "undeletable"
)

var (
	reFailedCount = regexp.MustCompile(`(\d+) change\(s\) failed: `)
	reChange      = regexp.MustCompile(`^(create|update|delete|adopt|reorder|unmanage) (server|role|category|channel|everyone|widget|automod|onboarding|welcomeScreen|emoji|sticker|sound) (\S*): `)
	reREST        = regexp.MustCompile(`HTTP (\d{3}) (\d+): `)
	reStatusOnly  = regexp.MustCompile(`HTTP (\d{3})\b`)
	reUnresolved  = regexp.MustCompile(`unresolved (channel|category|role) "([^"]*)"`)
	rePreflight   = regexp.MustCompile(`preflight: the bot lacks permissions this sync needs: ([A-Za-z, ]+)`)
	reLockout     = regexp.MustCompile(`preflight: (?:.*; )?the bot cannot see or manage (\S+(?:, \S+)*)`)
	reGrant       = regexp.MustCompile(`cannot (?:grant|allow or deny) permissions you do not have: ([A-Za-z, ]+)`)
	reIntents     = regexp.MustCompile(`disallowed intents ([A-Za-z, ]+)`)
	reRevision    = regexp.MustCompile(`^revision (\S+) rejected: `)
	reMaximum     = regexp.MustCompile(`maximum number of [^(:]+(\(\d+\))?`)
)

// Humanize explains a raw error text produced by the runtime (a sync failure, a server
// or bot status message, a rejected revision, a Discord REST error…). It returns nil for
// an empty text. The runtime stores errors as strings (status messages, sync history),
// so the parsing works on the text forms written by internal/discord/disgoadapter
// (`discord <Op>: discord: <sentinel> (HTTP <status> <code>: <message> <errors JSON>)`),
// internal/sim (`discord: <sentinel>: <text>`) and this package
// (`reconcile <server>: <n> change(s) failed: <action> <kind> <key>: …`).
func Humanize(raw string) *HumanError {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	h := &HumanError{Code: ErrCodeUnknown, Details: raw}

	// The first failure carries the explanation; errors.Join puts the others on the
	// following lines.
	text := firstLineOf(raw)
	if m := reFailedCount.FindStringSubmatchIndex(text); m != nil {
		if n, _ := strconv.Atoi(text[m[2]:m[3]]); n > 1 {
			h.Failures = n
		}
		text = text[m[1]:]
	}
	text = strings.TrimPrefix(text, "self-heal: ")
	if m := reChange.FindStringSubmatch(text); m != nil {
		h.Resource = &ErrorResource{Action: m[1], Kind: m[2], Key: m[3]}
		text = text[len(m[0]):]
	}

	// Discord REST details.
	discordMsg := ""
	var tree map[string]any
	if m := reREST.FindStringSubmatchIndex(text); m != nil {
		h.HTTPStatus, _ = strconv.Atoi(text[m[2]:m[3]])
		h.DiscordCode, _ = strconv.Atoi(text[m[4]:m[5]])
		rest := text[m[1]:]
		if i := strings.Index(rest, "{"); i >= 0 {
			discordMsg = strings.TrimSpace(rest[:i])
			if obj := balancedJSON(rest[i:]); obj != "" {
				_ = json.Unmarshal([]byte(obj), &tree)
			}
		} else {
			discordMsg = balanceParens(strings.TrimSpace(rest))
		}
	} else if m := reStatusOnly.FindStringSubmatch(text); m != nil {
		h.HTTPStatus, _ = strconv.Atoi(m[1])
	}
	if tree != nil {
		h.Fields = fieldErrors(tree)
	}

	classify(h, raw, text, discordMsg)
	return h
}

// HumanizeErr is Humanize(err.Error()) (nil for a nil error).
func HumanizeErr(err error) *HumanError {
	if err == nil {
		return nil
	}
	return Humanize(err.Error())
}

func classify(h *HumanError, raw, text, discordMsg string) {
	lower := strings.ToLower(text)
	what := h.Resource.phrase()
	verb := h.Resource.verb()
	code := h.DiscordCode

	switch {
	// Community-only features. Discord answers 50035 "Value must be one of …" for the
	// channel type; reconcile adds an explicit suffix (withCommunityHint).
	case strings.Contains(lower, "require the community feature") || strings.Contains(lower, "requires the community feature") ||
		hasField(h.Fields, "rules_channel_id", "public_updates_channel_id", "safety_alerts_channel_id") ||
		(h.Resource != nil && h.Resource.Kind == KindChannel && code == 50035 && hasFieldCode(h.Fields, "type", "BASE_TYPE_CHOICES")):
		h.Code = ErrCodeCommunityRequired
		if h.Resource != nil && h.Resource.Kind == KindChannel {
			h.Message = "Announcement and stage channels need the Community feature"
		} else {
			h.Message = "This setting needs the Community feature"
		}
		h.Hint = "Declare community: {enabled: true, rulesChannel, updatesChannel} in the server YAML and Cicord enables Community at the next sync. " +
			"If it is already declared, enabling it failed or it was turned off outside Cicord: check the sync history, or enable it in Discord " +
			"(Server Settings → Enable Community), then sync again. Or change the channel type in the YAML."

	case reIntents.MatchString(text) || strings.Contains(lower, "privileged intents") || strings.Contains(lower, "disallowed intents"):
		h.Code = ErrCodeIntentsDisallowed
		h.Message = "The bot needs privileged intents that are not enabled"
		if m := reIntents.FindStringSubmatch(text); m != nil {
			h.Message += ": " + strings.TrimSpace(m[1])
		}
		h.Hint = "In the Discord Developer Portal (your application → Bot → Privileged Gateway Intents), enable the intents the plugins need, then restart Cicord."

	// The sync preflight (PermissionsError), before anything was applied.
	case rePreflight.MatchString(text):
		perms := strings.TrimSpace(rePreflight.FindStringSubmatch(text)[1])
		h.Code = ErrCodeMissingPermissions
		h.Message = "The bot is missing permissions this sync needs: " + perms
		h.Hint = "Nothing was applied. In Server Settings → Roles, give the bot's role these permissions, or remove them from the YAML, then sync again. " +
			"Manage Roles in a channel overwrite always needs Administrator."
		if reLockout.MatchString(text) {
			h.Hint += " Some managed channels also hide themselves from the bot: allow ViewChannel to \"@bot\" in their overwrites."
		}

	case reLockout.MatchString(text):
		paths := reLockout.FindStringSubmatch(text)[1]
		h.Code = ErrCodeMissingAccess
		h.Message = "The bot cannot see or manage " + paths
		h.Hint = "Nothing was applied. Add \"@bot\": { allow: [ViewChannel] } to the overwrites of these channels (or their category). " +
			"If the bot already cannot see a channel, also give its role View Channel there in Discord, then sync again."

	case reGrant.MatchString(text):
		perms := strings.TrimSpace(reGrant.FindStringSubmatch(text)[1])
		h.Code = ErrCodeMissingPermissions
		h.Message = "The bot cannot grant permissions it does not have: " + perms
		h.Hint = "Give the bot's role these permissions (or Administrator) in Server Settings → Roles, or remove them from the YAML, then sync again."

	case strings.Contains(lower, "highest role") || strings.Contains(lower, "role hierarchy"):
		h.Code = ErrCodeRoleHierarchy
		h.Message = "The bot's role is not high enough to change " + orDefault(what, "this role")
		h.Hint = "In Server Settings → Roles, drag the bot's role above every role Cicord manages, then sync again."

	case code == 50001 || strings.Contains(lower, "missing access"):
		h.Code = ErrCodeMissingAccess
		h.Message = "The bot cannot see " + orDefault(what, "this resource")
		h.Hint = "Give the bot's role View Channel on the channel (or its category), or check that the bot is still in the server."

	case code == 50013 || strings.Contains(lower, "missing permissions"):
		h.Code = ErrCodeMissingPermissions
		perm := requiredPermission(h.Resource)
		if perm != "" {
			h.Message = fmt.Sprintf("The bot needs the %s permission to %s", perm, orDefault(verb, "do this"))
		} else {
			h.Message = "The bot is missing a permission for this change"
		}
		h.Hint = "In Server Settings → Roles, give the bot's role " + orDefault(perm, "the required permission") +
			" (or Administrator) and make sure it is above the roles it manages, then sync again."

	// Boost-gated features: only on structured evidence (field errors naming a boost-gated
	// setting, or Discord's explicit wording), never on a bare "boost" in a role name.
	case strings.Contains(lower, "boost level") || strings.Contains(lower, "premium tier") ||
		strings.Contains(raw, "ROLE_ICONS") || code == 50101 ||
		hasField(h.Fields, "banner", "splash", "discovery_splash", "icon_emoji", "unicode_emoji", "colors"):
		h.Code = ErrCodeBoostRequired
		h.Message = "This feature needs a higher server boost level"
		h.Hint = "Boost the server to the required level, or remove the setting from the YAML, then sync again."

	case code == 10003 || code == 10011 || code == 10004 || code == 10007 || code == 10008 ||
		strings.Contains(lower, "discord: not found"):
		h.Code = ErrCodeUnknownResource
		h.Message = capitalize(orDefault(what, "the resource")) + " no longer exists on Discord"
		if code == 10004 {
			h.Message = "The server no longer exists, or the bot was removed from it"
			h.Hint = "Invite the bot to the server again, or check spec.discordId in the server YAML."
		} else {
			h.Hint = "It was probably deleted in Discord. Sync again to recreate it, or remove it from the YAML."
		}

	case code == 30005 || code == 30013 || code == 30016 || code == 30010 || code == 30018 || code == 30008 || code == 30039 ||
		code == 30045 || code == 30034 || strings.Contains(lower, "maximum number of"):
		h.Code = ErrCodeLimitReached
		h.Message = "Discord limit reached: " + strings.TrimSuffix(orDefault(discordMsg, "too many resources"), ".")
		h.Hint = "Remove unused resources (in Discord or in the YAML), or boost the server for more expression slots, then sync again."
		if discordMsg == "" {
			if m := reMaximum.FindString(text); m != "" {
				h.Message = "Discord limit reached: " + m
			}
		}

	case h.HTTPStatus == 401 || strings.Contains(lower, "invalid token") || strings.Contains(lower, "authentication failed"):
		h.Code = ErrCodeInvalidToken
		h.Message = "Discord rejected the bot token"
		h.Hint = "Reset the token in the Discord Developer Portal (your application → Bot), update the bot's secret, then restart Cicord."

	case h.HTTPStatus == 429 || strings.Contains(lower, "rate limit"):
		h.Code = ErrCodeRateLimited
		h.Message = "Discord is rate limiting the bot"
		h.Hint = "Cicord retries automatically. Wait a minute, then sync again."

	case h.HTTPStatus >= 500:
		h.Code = ErrCodeDiscordUnavailable
		h.Message = fmt.Sprintf("Discord is having trouble (HTTP %d)", h.HTTPStatus)
		h.Hint = "Wait a moment and sync again. If it persists, check discordstatus.com."

	case strings.Contains(lower, "deadline exceeded") || strings.Contains(lower, "timed out") || strings.Contains(lower, "i/o timeout"):
		h.Code = ErrCodeTimeout
		h.Message = "Discord did not answer in time"
		h.Hint = "Sync again. If it keeps happening, check the runtime's network connection."

	case strings.Contains(lower, "no connected bot"):
		h.Code = ErrCodeNoBot
		h.Message = "No connected bot can sync this server"
		h.Hint = "Check that a bot lists this server in spec.servers, is invited to it and is running (see the bot's status)."

	case lower == "not connected" || strings.Contains(lower, "bot is not connected") || strings.Contains(lower, "session is not connected"):
		h.Code = ErrCodeBotNotConnected
		h.Message = "The bot is not connected to Discord"
		h.Hint = "Check the bot token and the runtime logs. Cicord reconnects automatically."

	case reUnresolved.MatchString(text):
		m := reUnresolved.FindStringSubmatch(text)
		h.Code = ErrCodeUnresolvedRef
		h.Message = fmt.Sprintf("%s refers to the %s %q, which does not exist on Discord yet", capitalize(orDefault(what, "this change")), m[1], m[2])
		h.Hint = "Check the key in the YAML. If the " + m[1] + " is created by the same sync, sync again."

	case reRevision.MatchString(text):
		m := reRevision.FindStringSubmatch(text)
		h.Code = ErrCodeRevisionRejected
		reason := strings.TrimSpace(text[len(m[0]):])
		h.Message = fmt.Sprintf("Revision %s was rejected: %s", m[1], trimSentence(reason))
		h.Hint = "Fix the configuration and push again. The runtime keeps running the last valid revision."

	case strings.HasPrefix(lower, "fetch: "):
		h.Code = ErrCodeFetchFailed
		h.Message = "The configuration repository could not be fetched"
		h.Hint = "Check the repository URL, its credentials and the network. The runtime keeps the last revision."

	case code == 50035 || len(h.Fields) > 0:
		h.Code = ErrCodeInvalidFormBody
		h.Message = fmt.Sprintf("Discord rejected %s", orDefault(what, "the request"))
		if len(h.Fields) > 0 {
			h.Message += ": " + summarizeFields(h.Fields)
			h.Hint = "Check " + fieldNames(h.Fields) + " in the YAML, then sync again."
		} else {
			h.Hint = "Check the values in the YAML, then sync again."
		}

	case strings.Contains(lower, "discord: bad request") || h.HTTPStatus == 400:
		h.Code = ErrCodeBadRequest
		detail := discordMsg
		if detail == "" {
			detail = afterSentinel(text)
		}
		h.Message = fmt.Sprintf("Discord refused to %s", orDefault(verb, "apply this change"))
		if detail != "" {
			h.Message += ": " + trimSentence(detail)
		}
		h.Hint = "Check the values in the YAML, then sync again."

	default:
		detail := afterSentinel(text)
		if verb != "" {
			h.Message = fmt.Sprintf("Could not %s: %s", verb, trimSentence(orDefault(detail, text)))
		} else {
			h.Message = trimSentence(stripJSON(orDefault(detail, text)))
		}
	}
}

// ExplainBlocked explains why the reconciler refuses an operation of the plan.
func ExplainBlocked(b Blocked) *HumanError {
	r := &ErrorResource{Action: b.Action, Kind: b.Kind, Key: b.Key}
	what := r.phrase()
	h := &HumanError{Details: b.Reason, Resource: r}
	reason := b.Reason
	switch {
	case reason == "deletionProtection":
		h.Code = ErrCodeDeletionProtection
		h.Message = capitalize(what) + " was removed from the YAML but deletion protection is on"
		h.Hint = "Set syncPolicy.deletionProtection: false to let Cicord delete it, or add it back to the YAML."
	case strings.HasPrefix(reason, "hierarchy") && b.Kind == KindMemberRole && b.Summary != "":
		h.Code = ErrCodeRoleHierarchy
		h.Message = "Cannot " + b.Summary + ": the role is at or above the bot's highest role"
		h.Hint = "In Server Settings → Roles, drag the bot's role above it, then sync again."
	case strings.HasPrefix(reason, "hierarchy"):
		h.Code = ErrCodeRoleHierarchy
		h.Message = capitalize(what) + " is at or above the bot's highest role"
		h.Hint = "In Server Settings → Roles, drag the bot's role above it, then sync again."
	case strings.HasPrefix(reason, "ownership"):
		h.Code = ErrCodeOwnership
		owner := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(reason, "ownership:"), " key is owned by"))
		h.Message = capitalize(what) + " is managed by " + orDefault(owner, "another owner")
		h.Hint = "Rename the key in the YAML, or remove it from the plugin that owns it."
	case strings.HasPrefix(reason, "managed") && b.Kind == KindMemberRole && b.Summary != "":
		h.Code = ErrCodeManagedRole
		h.Message = "Cannot " + b.Summary + ": the role belongs to an integration (a bot or app role)"
		h.Hint = "Discord does not let anyone give integration roles. Remove members from this role in the YAML."
	case strings.HasPrefix(reason, "managed"):
		h.Code = ErrCodeManagedRole
		h.Message = capitalize(what) + " belongs to an integration (a bot or app role)"
		h.Hint = "Discord does not let anyone edit integration roles. Remove it from the YAML."
	case strings.HasPrefix(reason, "type change") && b.Kind == KindAutoMod:
		h.Code = ErrCodeTypeChange
		h.Message = "Discord cannot change the trigger of " + what
		h.Hint = "Keep the trigger, or declare the new rule under another key (the old rule is then unmanaged, or deleted with prune)."
	case strings.HasPrefix(reason, "type change"):
		h.Code = ErrCodeTypeChange
		h.Message = "Discord cannot change the type of " + what + " in place"
		h.Hint = "Set syncPolicy.prune: true and deletionProtection: false to recreate it, or keep its current type in the YAML."
	case strings.HasPrefix(reason, prefixBoost):
		h.Code = ErrCodeBoostRequired
		h.Message = capitalize(strings.TrimPrefix(reason, prefixBoost))
		h.Hint = "Boost the server to the required level (Server Settings → Server Boost), or remove the setting from the YAML. Once the level is reached, Cicord applies it right away with syncPolicy.selfHeal; otherwise it shows as a pending change until the next revision or Sync now."
	case strings.HasPrefix(reason, prefixCommunity):
		h.Code = ErrCodeCommunityRequired
		h.Message = capitalize(strings.TrimPrefix(reason, prefixCommunity))
		h.Hint = "Declare community: {enabled: true, rulesChannel, updatesChannel} with profile.verificationLevel low or higher and profile.explicitContentFilter: allMembers, then sync again."
	case strings.HasPrefix(reason, prefixLimit):
		h.Code = ErrCodeLimitReached
		h.Message = "Discord limit: " + strings.TrimPrefix(reason, prefixLimit)
		h.Hint = "Remove some from Discord or from the YAML."
	case strings.HasPrefix(reason, prefixRecreate):
		h.Code = ErrCodeRecreateRequired
		h.Message = capitalize(strings.TrimPrefix(reason, prefixRecreate))
		h.Hint = "Restore the previous file, or declare the new file under a new key."
	case strings.HasPrefix(reason, prefixUndeletable):
		h.Code = ErrCodeUndeletable
		h.Message = strings.TrimPrefix(reason, prefixUndeletable)
		h.Hint = "Keep it in the YAML (set enabled: false to turn it off), or set syncPolicy.prune: false to stop managing it."
	case strings.Contains(reason, "no connected bot"):
		h.Code = ErrCodeNoBot
		h.Message = "No connected bot can sync this server"
		h.Hint = "Check that a bot lists this server in spec.servers, is invited to it and is running."
	default:
		h.Code = ErrCodeUnknown
		h.Message = capitalize(reason)
	}
	return h
}

// ───────────────────────────────────────────────────────────── helpers

var kindNoun = map[string]string{
	KindServer: "the server settings", KindRole: "role", KindCategory: "category",
	KindChannel: "channel", KindEveryone: "the @everyone role", KindMemberRole: "role assignment",
	KindWidget: "the server widget", KindOnboarding: "the onboarding", KindWelcomeScreen: "the welcome screen",
	KindAutoMod: "AutoMod rule", KindEmoji: "emoji", KindSticker: "sticker", KindSound: "soundboard sound",
}

// phrase is "the channel “news”", "the server settings"…
func (r *ErrorResource) phrase() string {
	if r == nil {
		return ""
	}
	noun, ok := kindNoun[r.Kind]
	if !ok {
		noun = r.Kind
	}
	if r.Kind == KindServer || r.Kind == KindEveryone || r.Kind == KindWidget || r.Kind == KindOnboarding || r.Kind == KindWelcomeScreen {
		return noun
	}
	if r.Action == ActionReorder {
		switch r.Kind {
		case KindRole:
			return "the role order"
		case KindCategory:
			return "the category order"
		}
		return "the channel order"
	}
	if r.Key == "" {
		return "a " + noun
	}
	return "the " + noun + " “" + r.Key + "”"
}

// verb is "create the channel “news”", "update the server settings"…
func (r *ErrorResource) verb() string {
	if r == nil {
		return ""
	}
	action := r.Action
	switch action {
	case ActionAdopt:
		action = "adopt"
	case ActionReorder:
		return "reorder " + strings.TrimPrefix(r.phrase(), "the ")
	case ActionUnmanage:
		action = "stop managing"
	}
	return action + " " + r.phrase()
}

func requiredPermission(r *ErrorResource) string {
	if r == nil {
		return ""
	}
	switch r.Kind {
	case KindRole, KindEveryone, KindMemberRole:
		return "Manage Roles"
	case KindChannel, KindCategory:
		return "Manage Channels"
	case KindServer, KindWidget, KindOnboarding, KindWelcomeScreen, KindAutoMod:
		return "Manage Server"
	case KindEmoji, KindSticker, KindSound:
		return "Manage Expressions"
	}
	return ""
}

// fieldErrors flattens Discord's errors tree into sorted leaves.
func fieldErrors(tree map[string]any) []FieldError {
	var out []FieldError
	var walk func(path string, v any)
	walk = func(path string, v any) {
		obj, ok := v.(map[string]any)
		if !ok {
			return
		}
		for k, child := range obj {
			if k == "_errors" {
				list, _ := child.([]any)
				for _, e := range list {
					em, _ := e.(map[string]any)
					fe := FieldError{Path: path}
					fe.Code, _ = em["code"].(string)
					fe.Message, _ = em["message"].(string)
					out = append(out, fe)
				}
				continue
			}
			p := k
			if path != "" {
				p = path + "." + k
			}
			walk(p, child)
		}
	}
	walk("", tree)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

func hasField(fs []FieldError, paths ...string) bool {
	for _, f := range fs {
		for _, p := range paths {
			if f.Path == p || strings.HasSuffix(f.Path, "."+p) {
				return true
			}
		}
	}
	return false
}

func hasFieldCode(fs []FieldError, path, code string) bool {
	for _, f := range fs {
		if f.Path == path && f.Code == code {
			return true
		}
	}
	return false
}

func summarizeFields(fs []FieldError) string {
	parts := make([]string, 0, len(fs))
	for i, f := range fs {
		if i == 3 {
			parts = append(parts, fmt.Sprintf("and %d more", len(fs)-3))
			break
		}
		name := f.Path
		if name == "" {
			name = "body"
		}
		parts = append(parts, name+": "+strings.TrimSuffix(f.Message, "."))
	}
	return strings.Join(parts, "; ")
}

func fieldNames(fs []FieldError) string {
	seen := map[string]bool{}
	var names []string
	for _, f := range fs {
		if f.Path != "" && !seen[f.Path] {
			seen[f.Path] = true
			names = append(names, f.Path)
		}
	}
	if len(names) == 0 {
		return "the values"
	}
	if len(names) == 1 {
		return "the field “" + names[0] + "”"
	}
	return "the fields “" + strings.Join(names, "”, “") + "”"
}

// balancedJSON returns the JSON object at the start of s (brace matching, strings aware).
func balancedJSON(s string) string {
	depth, inStr, esc := 0, false, false
	for i, c := range s {
		switch {
		case esc:
			esc = false
		case c == '\\' && inStr:
			esc = true
		case c == '"':
			inStr = !inStr
		case inStr:
		case c == '{':
			depth++
		case c == '}':
			depth--
			if depth == 0 {
				return s[:i+1]
			}
		}
	}
	return ""
}

var sentinels = []string{"discord: bad request", "discord: missing permissions", "discord: not found", "discord: invalid token"}

// afterSentinel returns the text after the last "discord: <sentinel>" marker.
func afterSentinel(text string) string {
	best := -1
	for _, s := range sentinels {
		if i := strings.LastIndex(text, s); i >= 0 && i+len(s) > best {
			best = i + len(s)
		}
	}
	if best < 0 {
		return ""
	}
	rest := strings.TrimLeft(text[best:], ":( ")
	rest = strings.TrimSuffix(rest, ")")
	return strings.TrimSpace(stripJSON(rest))
}

// stripJSON drops a trailing JSON blob from a message.
func stripJSON(s string) string {
	if i := strings.Index(s, " {"); i >= 0 {
		if balancedJSON(s[i+1:]) != "" {
			return strings.TrimSpace(s[:i])
		}
	}
	return s
}

// balanceParens drops the closing parentheses that close an enclosing context
// ("… reached (250))" → "… reached (250)").
func balanceParens(s string) string {
	for strings.HasSuffix(s, ")") && strings.Count(s, ")") > strings.Count(s, "(") {
		s = strings.TrimSpace(s[:len(s)-1])
	}
	if i := strings.Index(s, ") ("); i >= 0 && strings.Count(s[:i+1], ")") > strings.Count(s[:i+1], "(") {
		s = s[:i]
	}
	return s
}

func firstLineOf(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return s
}

func trimSentence(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimSuffix(s, ".")
	const max = 240
	if len(s) > max {
		cut := max
		for cut > 0 && (s[cut]&0xC0) == 0x80 { // stay on a UTF-8 boundary
			cut--
		}
		s = s[:cut] + "…"
	}
	return s
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
