package runtime

import (
	"crypto/sha256"
	"fmt"

	"github.com/cicord/cicord/internal/config"
)

// PluginSecretSlot addresses only an instance's own admin-entered value. Git binds
// its logical name to an operator ref; rotating that binding also changes the slot.
func PluginSecretSlot(in config.Instance, name string) string {
	return fmt.Sprintf("CICORD_PLUGIN_%X", sha256.Sum256([]byte(in.ID()+"\x00"+name+"\x00"+in.SecretRefs[name].SecretRef)))
}

func (r *Runtime) instanceSecret(in config.Instance, name string) (string, []string, bool) {
	ref, ok := in.SecretRefs[name]
	if !ok || r.opts.Secrets == nil {
		return "", nil, false
	}
	for _, s := range in.Plugin.Manifest.Spec.Secrets {
		if s.Name == name {
			for _, key := range []string{ref.SecretRef, PluginSecretSlot(in, name)} {
				if v, ok := r.opts.Secrets(key); ok && v != "" {
					return v, s.Hosts, true
				}
			}
		}
	}
	return "", nil, false
}
