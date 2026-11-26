package plugin

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestIncidentGrantsAndRestrictions(t *testing.T) {
	for _, op := range []struct {
		cap string
		fn  asyncOp
	}{{"server.overlays", opOverlayApply}, {"server.overlays", opOverlayRemove}, {"server.overlays", opOverlayList}, {"server.incident", opIncident}, {"server.sync", opRequestSync}} {
		_, err := op.fn(&Instance{}, nil, nil)
		var denied *CapabilityError
		if !errors.As(err, &denied) || denied.Capability != op.cap {
			t.Fatalf("%s: %v", op.cap, err)
		}
	}
	i := &Instance{grants: map[string]bool{"server.overlays": true}}
	_, err := opOverlayApply(i, nil, json.RawMessage(`{"channels":{"general":{"overwrites":{"@everyone":{"allow":["SendMessages"],"deny":["AttachFiles"]}}}}}`))
	if err == nil {
		t.Fatal("accepted an allow")
	}
}
