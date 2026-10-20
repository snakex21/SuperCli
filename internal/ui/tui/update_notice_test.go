package tui

import "testing"

func TestSystemNoticeAddsTranscriptLineAndSettingRow(t *testing.T) {
	m := New(Options{Home: t.TempDir(), DataDir: t.TempDir(), Language: "pl"})
	next, _ := m.update(SystemNoticeMsgFrom("Dostępna jest wersja SuperCli 9.9.9 — wpisz /update."))
	got := next.(Model)
	n := len(got.chat.msgs)
	if n == 0 || got.chat.msgs[n-1].text != "Dostępna jest wersja SuperCli 9.9.9 — wpisz /update." || got.chat.msgs[n-1].role != roleSystem {
		t.Fatalf("notice not appended: %+v", got.chat.msgs)
	}
	found := false
	for _, row := range settingsRowsFor("pl") {
		if row.key == "update_notify" {
			found = row.label == "Powiadomienia o aktualizacjach"
		}
	}
	if !found {
		t.Fatal("update_notify settings row missing or untranslated")
	}
}
