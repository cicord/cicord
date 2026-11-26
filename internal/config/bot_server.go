package config

import (
	"encoding/json"
	"gopkg.in/yaml.v3"
)

// BotServer accepts the original server key as a scalar and the extended form with a guild profile.
type BotServer struct {
	Key     string        `yaml:"key" json:"key"`
	Profile *GuildProfile `yaml:"profile,omitempty" json:"profile,omitempty"`
}
type GuildProfile struct {
	Nickname *string `yaml:"nickname,omitempty" json:"nickname,omitempty"`
	Avatar   *string `yaml:"avatar,omitempty" json:"avatar,omitempty"`
	Banner   *string `yaml:"banner,omitempty" json:"banner,omitempty"`
	Bio      *string `yaml:"bio,omitempty" json:"bio,omitempty"`
}

func (s *BotServer) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.ScalarNode {
		*s = BotServer{}
		return n.Decode(&s.Key)
	}
	type binding BotServer
	return n.Decode((*binding)(s))
}
func (s *BotServer) UnmarshalJSON(data []byte) error {
	var key string
	if err := json.Unmarshal(data, &key); err == nil {
		*s = BotServer{Key: key}
		return nil
	}
	type binding BotServer
	return json.Unmarshal(data, (*binding)(s))
}
func (s BotServer) MarshalYAML() (any, error) {
	if s.Profile == nil {
		return s.Key, nil
	}
	type binding BotServer
	return binding(s), nil
}
func (s BotServer) MarshalJSON() ([]byte, error) { v, _ := s.MarshalYAML(); return json.Marshal(v) }
func (b BotSpec) ServerKeys() []string {
	keys := make([]string, len(b.Servers))
	for i, s := range b.Servers {
		keys[i] = s.Key
	}
	return keys
}
