package agent

import "testing"

// Production schemas and the actual child-registry setup, without model/network
// time. The base setup is excluded because it occurs before any delegation.
func BenchmarkWorkerRegistrySetup(b *testing.B) {
	base := workerSchemaFixture(b)
	for _, spec := range BuiltinSubAgents() {
		if spec.Name != "code" && spec.Name != "general" {
			continue
		}
		b.Run(spec.Name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				child := restrictedRegistry(base, spec.AllowedTools, spec.DeferredTools...)
				child.EnsureReadOutput()
				if child.Len() == 0 {
					b.Fatal("empty registry")
				}
			}
		})
	}
}
