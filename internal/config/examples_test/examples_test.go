package examplestest

import (
	"github.com/glours/hakoniwa/internal/config"
	"testing"
)

func TestExamplesValidate(t *testing.T) {
	for _, f := range []string{
		"../../../examples/debug-collab/hakoniwa.yaml",
		"../../../examples/cross-review/hakoniwa.yaml",
	} {
		t.Run(f, func(t *testing.T) {
			lr, err := config.Load(f)
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			if ve := config.Validate(lr); ve != nil {
				t.Fatalf("validate:\n%v", ve)
			}
			t.Logf("OK %s — %d agents, %d channels", f, len(lr.Project.Agents), len(lr.Project.Channels))
		})
	}
}
