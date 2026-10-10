package webgui

import "testing"

func TestMediaGenerationIsDiscoverableWithoutAlwaysOnSchemas(t *testing.T) {
	srv := newTestServer(t, false)
	_, registry, err := srv.eng.buildLoopWithSession(nil, nil, srv.eng.Home(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"generate_image", "generate_video"} {
		if _, ok := registry.Get(name); !ok {
			t.Fatalf("missing %s", name)
		}
		for _, tool := range registry.Visible() {
			if tool.Name == name {
				t.Fatalf("%s adds a default prompt schema", name)
			}
		}
	}
}
