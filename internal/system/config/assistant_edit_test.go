package config

import "testing"

func TestAssistantEditDefaultsOffAndMerge(t *testing.T) {
	if AssistantEditEnabled(TomlConfig{}) || AssistantEditModelSeesEnabled(TomlConfig{}) {
		t.Fatal("assistant edit settings must default to off")
	}
	on := true
	dst := TomlConfig{}
	mergeToml(&dst, TomlConfig{AssistantEdit: &on, AssistantEditModelSees: &on})
	if !AssistantEditEnabled(dst) || !AssistantEditModelSeesEnabled(dst) {
		t.Fatal("explicit on did not merge")
	}
	mergeToml(&dst, TomlConfig{})
	if !AssistantEditEnabled(dst) {
		t.Fatal("nil source cleared an explicit on")
	}
}
