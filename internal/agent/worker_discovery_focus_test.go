package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"supercli/internal/tools"
)

func TestWorkerCodeDiscoveryDoesNotActivateWeakOfficeMatch(t *testing.T) {
	for _, thin := range []bool{false, true} {
		t.Run(fmt.Sprint(thin), func(t *testing.T) {
			all := workerSchemaFixture(t)
			base := tools.NewRegistry()
			for _, name := range all.Names() {
				if name != "create_file" {
					spec, _ := all.Get(name)
					base.MustRegister(spec)
				}
			}
			base.MustRegister(tools.NewReadContext(t.TempDir()).Spec())
			child := codeWorkerForSchemaTest(t, base, thin)
			before, _ := json.Marshal(child.buildToolDefs())
			result, err := child.registry.Execute(context.Background(), "tool_search", json.RawMessage(`{"query":"patch file editing"}`))
			if err != nil || result.Err != nil {
				t.Fatalf("%v %+v", err, result)
			}
			after, _ := json.Marshal(child.buildToolDefs())
			t.Logf("thin=%t discovery=%d bytes, definitions=%d -> %d bytes", thin, len(result.Text), len(before), len(after))
			if !strings.Contains(result.Text, `"name":"patch_file"`) || !strings.Contains(result.Text, `"name":"read_context"`) {
				t.Fatal("strong code matches lost")
			}
			if strings.Contains(result.Text, `"name":"edit_docx"`) || child.registry.IsActive("edit_docx") {
				t.Fatal("weak file-only match loaded the Word schema")
			}
			// An explicit tool request still discovers the optional capability.
			word, err := child.registry.Execute(context.Background(), "tool_search", json.RawMessage(`{"query":"edit_docx"}`))
			if err != nil || word.Err != nil || !child.registry.IsActive("edit_docx") {
				t.Fatal("Word tool became unreachable")
			}
			if base.IsActive("edit_docx") {
				t.Fatal("child discovery changed parent")
			}
		})
	}
}
