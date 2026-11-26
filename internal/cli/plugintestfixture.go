package cli

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"

	"gopkg.in/yaml.v3"
)

type testSyncPolicy struct {
	SelfHeal           *bool `yaml:"selfHeal,omitempty"`
	DeletionProtection *bool `yaml:"deletionProtection,omitempty"`
}

type testServerFixture struct {
	SyncPolicy      *testSyncPolicy `yaml:"syncPolicy"`
	PrivateChannels []string        `yaml:"privateChannels"`
}

// readTestServerFixture reads a bounded declaration for the disposable simulator.
func readTestServerFixture(pluginDir string) (*testServerFixture, error) {
	const path = "test/server.yaml"
	const maxSize = 4096
	root, err := os.OpenRoot(pluginDir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	info, err := root.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > maxSize {
		return nil, errors.New("expected a regular YAML file of at most 4 KiB")
	}

	f, err := root.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxSize+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxSize {
		return nil, errors.New("file exceeds 4 KiB")
	}

	var fixture testServerFixture
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&fixture); err != nil {
		return nil, err
	}
	if fixture.SyncPolicy != nil && fixture.SyncPolicy.SelfHeal == nil && fixture.SyncPolicy.DeletionProtection == nil {
		return nil, errors.New("expected syncPolicy.selfHeal or syncPolicy.deletionProtection")
	}
	if fixture.SyncPolicy == nil && len(fixture.PrivateChannels) == 0 {
		return nil, errors.New("expected syncPolicy or at least one privateChannels entry")
	}
	if len(fixture.PrivateChannels) > 32 {
		return nil, errors.New("privateChannels supports at most 32 channel keys")
	}
	seen := make(map[string]bool, len(fixture.PrivateChannels))
	for _, key := range fixture.PrivateChannels {
		if key == "" || seen[key] {
			return nil, fmt.Errorf("privateChannels contains an empty or duplicate key %q", key)
		}
		seen[key] = true
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err != nil {
			return nil, fmt.Errorf("trailing YAML: %w", err)
		}
		return nil, errors.New("expected a single YAML document")
	}
	return &fixture, nil
}
