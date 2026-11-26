package plugin

import (
	"context"
	"encoding/json"
)

type ProtectionResource struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Kind string `json:"kind"`
}
type ServerProtection struct {
	Known              bool                 `json:"known"`
	SelfHeal           bool                 `json:"selfHeal"`
	DeletionProtection bool                 `json:"deletionProtection"`
	Paused             bool                 `json:"paused"`
	Unmanaged          []ProtectionResource `json:"unmanaged"`
	TotalResources     int                  `json:"totalResources"`
	BotTopPosition     *int                 `json:"botTopPosition,omitempty"`
}

func opServerProtection(i *Instance, _ *invocation, _ json.RawMessage) (func(context.Context) (any, error), error) {
	if err := i.require(capChannelsRead); err != nil {
		return nil, err
	}
	return func(ctx context.Context) (any, error) {
		if i.env.Protection == nil {
			return ServerProtection{Unmanaged: []ProtectionResource{}}, nil
		}
		return i.env.Protection(ctx), nil
	}, nil
}
