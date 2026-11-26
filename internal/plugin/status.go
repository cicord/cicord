package plugin

import "encoding/json"

func opFeatureStatus(i *Instance, _ *invocation, args json.RawMessage) (any, error) {
	a, err := decode[struct{ Key, Message string }](args)
	if err != nil {
		return nil, err
	}
	if len(a.Key) == 0 || len(a.Key) > 96 || len(a.Message) > 320 {
		return nil, argErr("invalid feature diagnostic")
	}
	if i.env.ReportStatus != nil {
		i.env.ReportStatus(a.Key, a.Message)
	}
	return nil, nil
}
