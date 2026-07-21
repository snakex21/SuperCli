//go:build windows

package webgui

import "golang.org/x/sys/windows"

func suggestedUserFolders() []folderIndexLocation {
	definitions := []struct {
		label string
		id    *windows.KNOWNFOLDERID
	}{
		{"Pulpit", windows.FOLDERID_Desktop},
		{"Dokumenty", windows.FOLDERID_Documents},
		{"Pobrane", windows.FOLDERID_Downloads},
		{"Obrazy", windows.FOLDERID_Pictures},
	}
	locations := make([]folderIndexLocation, 0, len(definitions))
	for _, definition := range definitions {
		path, err := windows.KnownFolderPath(definition.id, windows.KF_FLAG_DEFAULT)
		if err != nil || path == "" {
			continue
		}
		locations = append(locations, folderIndexLocation{
			Label: definition.label,
			Path:  path,
			Kind:  "system",
		})
	}
	return locations
}
