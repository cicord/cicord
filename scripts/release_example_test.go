package scripts_test

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestReleaseExampleSnapshotAndTags(t *testing.T) {
	awk, err := exec.LookPath("awk")
	if err != nil {
		t.Skip("awk unavailable")
	}
	raw, err := os.ReadFile("../examples/basic/.github/workflows/cicord.yml")
	if err != nil {
		t.Fatal(err)
	}
	const current = "0.2.0-rc.1"
	// This fixture exercises the transition regardless of which version the live
	// example is currently pinned to.
	const pin = "0123456789abcdef0123456789abcdef01234567"
	const path = "github.com/cicord/cicord"
	const prefix = "      - run: go install "
	lines := strings.Split(string(raw), "\n")
	for i, line := range lines {
		if strings.HasPrefix(line, prefix) {
			lines[i] = prefix + `-ldflags="-X ` + path + "/internal/version.Version=" + current + " -X " + path + "/internal/version.SourceRef=" + pin + `" ` + path + "/cmd/cicord@" + pin
		}
	}
	snapshot := strings.Join(lines, "\n")
	convert := func(in, old, next string) (string, error) {
		c := exec.Command(awk, "-v", "old="+old, "-v", "new="+next, "-f", "release-example.awk")
		c.Stdin = strings.NewReader(in)
		b, e := c.Output()
		return string(b), e
	}
	candidate, err := convert(snapshot, current, "0.2.0-rc.2")
	if err != nil || strings.Count(candidate, path+"/cmd/cicord@v0.2.0-rc.2") != 3 || strings.Contains(candidate, "-ldflags") {
		t.Fatalf("candidate conversion: %v", err)
	}
	stable, err := convert(candidate, "0.2.0-rc.2", "0.2.0")
	if err != nil || strings.Count(stable, path+"/cmd/cicord@v0.2.0\n") != 3 {
		t.Fatalf("release conversion: %v", err)
	}
	for _, bad := range []string{strings.Replace(snapshot, pin, "main", 1), strings.Replace(snapshot, "Version="+current, "Version=0.1.0", 1), strings.Replace(snapshot, prefix, "      - run: true # ", 1)} {
		if _, err := convert(bad, current, "0.2.0"); err == nil {
			t.Fatal("accepted unexpected previous install step")
		}
	}
}
