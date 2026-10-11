package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

// Actual application specs, with no execution, network or model inference.
// Constructors are excluded so this measures fresh registry registration.
func productionRegistrationSpecs(root string) []tools.Tool {
	return []tools.Tool{
		tools.NewReadLines(root).Spec(), tools.NewReadContext(root).Spec(),
		tools.NewReadMany(root).Spec(), tools.NewReadImage(root, 0).Spec(),
		tools.NewListDir(root).Spec(), tools.NewPatchFile(root).Spec(),
		tools.NewCreateFile(root).Spec(), tools.NewMakeDir(root).Spec(),
		tools.NewMove(root).Spec(), tools.NewCopy(root).Spec(),
		tools.NewTrash(root).Spec(), tools.NewSearchCode(root).Spec(),
		tools.NewCtxExecuteTool(nil, root).Spec(), tools.NewScratchpad(root).Spec(),
		tools.NewWriteFile(root).Spec(), tools.NewReadPdf(root, 0).Spec(),
		tools.NewReadDocx(root, 0).Spec(), tools.NewReadXlsx(root, 0).Spec(),
		tools.NewReadZip(root, 0).Spec(), tools.NewEditDocx(root).Spec(),
		tools.NewEditXlsx(root).Spec(),
	}
}

func BenchmarkProductionRegistryRegistration(b *testing.B) {
	specs := productionRegistrationSpecs(b.TempDir())
	b.ReportAllocs()
	for b.Loop() {
		reg := tools.NewRegistry()
		for _, spec := range specs {
			reg.MustRegister(spec)
		}
		reg.MustRegister(tools.NewToolSearcher(reg, nil).Spec())
		reg.MustRegister(NewInvokeTool(reg).Spec())
		reg.EnsureReadOutput()
		if reg.Len() != len(specs)+3 {
			b.Fatal("lost tools")
		}
	}
	b.ReportMetric(float64(len(specs)+3), "tools/registry")
}

// A cache miss control: each fresh registry gets a different plugin schema.
// The 1024-source working set exceeds the bounded cache; no timed execution.
func BenchmarkRegistrySchemaChurn(b *testing.B) {
	noop := func(context.Context, json.RawMessage) (tools.Result, error) { return tools.Result{Text: "ok"}, nil }
	b.ReportAllocs()
	for i := 0; b.Loop(); i++ {
		schema := fmt.Sprintf(`{"type":"object","title":"plugin_%d","properties":{"path":{"type":"string"},"limit":{"type":"integer","minimum":1,"maximum":100}},"required":["path"]}`, i%1024)
		reg := tools.NewRegistry()
		reg.MustRegister(tools.Tool{Name: "plugin", Description: "Changing plugin contract fixture", Schema: schema, Fn: noop})
	}
}

func requestedRenderPerfFixture(b *testing.B, tail bool) *Loop {
	l := contextPreparationFixture(b, true)
	if _, ok := l.registry.Get(invokeToolName); !ok {
		l.registry.MustRegister(NewInvokeTool(l.registry).Spec())
		l.registry.MarkAlwaysOn(invokeToolName)
	}
	// Freeze the native contracts before later generic workflow admission.
	l.buildToolDefs()
	if tail {
		root := b.TempDir()
		for _, spec := range []tools.Tool{
			tools.NewPatchFile(root).Spec(), tools.NewCreateFile(root).Spec(),
			tools.NewMove(root).Spec(), tools.NewCopy(root).Spec(),
			tools.NewReadPdf(root, 0).Spec(), tools.NewReadDocx(root, 0).Spec(),
			tools.NewEditDocx(root).Spec(), tools.NewEditXlsx(root).Spec(),
		} {
			// Core fixture names may already exist. Only late general contracts
			// are needed here; names and actual schema bytes remain untouched.
			if _, exists := l.registry.Get(spec.Name); exists {
				continue
			}
			l.registry.MustRegister(spec)
			l.workflowTools = append(l.workflowTools, spec.Name)
		}
		l.workflowRevision++
		if l.requestedToolContext() == "" {
			b.Fatal("fixture has no late dispatcher contracts")
		}
	}
	return l
}

// Two threshold estimates and final preparation mirror the actual loop path.
// The no-tail control protects ordinary turns with no requested contracts.
func BenchmarkRequestedContractPreparation(b *testing.B) {
	for _, tail := range []bool{false, true} {
		b.Run(fmt.Sprintf("tail=%v", tail), func(b *testing.B) {
			l := requestedRenderPerfFixture(b, tail)
			before := l.requestedToolContext()
			l.EstimateNextRequestTokens()
			b.ReportAllocs()
			b.ResetTimer()
			var requestTokens int
			for i := 0; i < b.N; i++ {
				l.EstimateNextRequestTokens()
				l.EstimateNextRequestTokens()
				request := l.prepareToolRequest()
				messages, messageTokens := l.prepareProviderMessages(true)
				if len(messages) == 0 || len(request.defs) == 0 {
					b.Fatal("lost provider request")
				}
				requestTokens = messageTokens + request.tokens
			}
			b.StopTimer()
			if l.requestedToolContext() != before {
				b.Fatal("request contract changed")
			}
			b.ReportMetric(float64(len(before)), "tail-bytes")
			b.ReportMetric(float64(requestTokens), "request-tokens")
			b.ReportMetric(float64(llm.EstimateTokens(l.VisibleMessages())), "history-tokens")
		})
	}
}
