package memory

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestLMStudioEmbeddingDetectionUsesOnlyLoadedEmbeddingModels(t *testing.T) {
	for _, test := range []struct {
		name, v1, v0, want string
		status             int
		paths              []string
	}{
		{name: "chat only", v1: `{"models":[{"type":"llm","key":"qwen","loaded_instances":[{"id":"qwen"}]}]}`, status: 200, paths: []string{"/api/v1/models"}},
		{name: "unloaded embedding", v1: `{"models":[{"type":"embedding","key":"nomic","loaded_instances":[]}]}`, status: 200, paths: []string{"/api/v1/models"}},
		{name: "loaded custom instance", v1: `{"models":[{"type":"llm","loaded_instances":[{"id":"chat"}]},{"type":"embedding","key":"downloaded-model","loaded_instances":[{"id":"custom-embedding@q8"}]}]}`, want: "custom-embedding@q8", status: 200, paths: []string{"/api/v1/models"}},
		{name: "legacy loaded", v0: `{"data":[{"type":"embeddings","id":"unloaded","state":"not-loaded"},{"type":"embeddings","id":"actual-embedding","state":"loaded"}]}`, want: "actual-embedding", status: 404, paths: []string{"/api/v1/models", "/api/v0/models"}},
		{name: "auth error", status: 401, paths: []string{"/api/v1/models"}},
		{name: "malformed", v1: "{", status: 200, paths: []string{"/api/v1/models"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			paths := []string{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				paths = append(paths, r.URL.Path)
				if r.Method != http.MethodGet {
					t.Error("detection requested model work")
				}
				if r.URL.Path == "/api/v1/models" {
					w.WriteHeader(test.status)
					w.Write([]byte(test.v1))
				} else if r.URL.Path == "/api/v0/models" {
					w.Write([]byte(test.v0))
				} else {
					t.Error("unexpected request", r.URL.Path)
					w.WriteHeader(404)
				}
			}))
			defer server.Close()
			got := detectLMStudioEmbedder(server.Client(), server.URL)
			if test.want == "" {
				if got != nil {
					t.Fatalf("incorrect embedding backend: %s", got.Name())
				}
			} else {
				backend, ok := got.(*openAIEmbedder)
				if !ok || backend.model != test.want {
					t.Fatalf("wrong selected model: %+v", got)
				}
			}
			if !reflect.DeepEqual(paths, test.paths) {
				t.Fatalf("extra probing: %v want %v", paths, test.paths)
			}
		})
	}
}
func TestDetectedLMStudioEmbeddingUsesItsActualInstanceID(t *testing.T) {
	var selected string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/models":
			w.Write([]byte(`{"models":[{"type":"embedding","loaded_instances":[{"id":"loaded-alias"}]}]}`))
		case "/v1/embeddings":
			var body struct {
				Model string `json:"model"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			selected = body.Model
			w.Write([]byte(`{"data":[{"embedding":[1,0],"index":0}]}`))
		default:
			t.Error("unexpected path", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	e := detectLMStudioEmbedder(server.Client(), server.URL)
	if e == nil {
		t.Fatal("not detected")
	}
	vector, err := e.Embed(t.Context(), "note")
	if err != nil || len(vector) != 2 || selected != "loaded-alias" {
		t.Fatalf("wrong request/result: model=%s vec=%v err=%v", selected, vector, err)
	}
	if got := loadedLMStudioEmbedModel(strings.NewReader(`{"models":[{"type":"embedding","loaded_instances":[{"id":"  "}]}]}`)); got != "" {
		t.Fatal("accepted empty instance")
	}
}
