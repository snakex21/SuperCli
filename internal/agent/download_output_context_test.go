package agent

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestDownloadOutputPathsExcludeSourcesAlternativesAndFactualMentions(t *testing.T) {
	source := filepath.Join(t.TempDir(), "source folder")
	output := filepath.Join(t.TempDir(), "output folder")
	private := filepath.Join(t.TempDir(), "private folder")
	for _, prompt := range []string{
		"Download a GIF from folder `" + source + "` into `" + output + "`",
		"Download a GIF from path `" + source + "` to `" + output + "`",
		"Pobierz GIF z katalogu `" + source + "` do `" + output + "`",
		"Download a GIF to `" + output + "` instead of `" + private + "`",
		"Pobierz GIF do `" + output + "` zamiast do `" + private + "`",
		"Download a GIF to `" + output + "` except folder `" + private + "`",
		"I have a folder `" + private + "`. Download a GIF into `" + output + "`",
		"Pobierz GIF do `" + output + "`. Folder `" + private + "` zawiera moje dokumenty",
	} {
		t.Run(prompt, func(t *testing.T) {
			got := requestedDownloadDirectories(prompt)
			if len(got) != 1 || !strings.EqualFold(got[0], output) {
				t.Fatalf("granted a source/excluded/mentioned path: %v, want only %q", got, output)
			}
		})
	}
}

func TestDownloadOutputPathsKeepNaturalChoicesAndDoNotGrantURLs(t *testing.T) {
	output := filepath.Join(t.TempDir(), "different GIFs")
	for _, prompt := range []string{
		"dzięki a możesz pobrać kolejny gif ale dać go tutaj? `" + output + "`",
		"Download two different animations into `" + output + "`",
		"Instead, download a GIF to `" + output + "`",
	} {
		if got := requestedDownloadDirectories(prompt); len(got) != 1 || !strings.EqualFold(got[0], output) {
			t.Fatalf("natural output choice lost: %v for %q", got, prompt)
		}
	}
	for _, choice := range []string{"`" + output + "`", "Folder: `" + output + "`", "Tutaj? `" + output + "`"} {
		if got := downloadDirectoriesFromPrompt(choice); len(got) != 1 || !strings.EqualFold(got[0], output) {
			t.Fatalf("directory-only choice lost: %v for %q", got, choice)
		}
	}
	for _, prompt := range []string{
		"Download https://cdn.example.org/a.gif into Downloads",
		"Download `//cdn.example.org/a.gif` into Downloads",
		"Download from directory `" + output + "`",
		"Download instead of folder `" + output + "`",
	} {
		if got := requestedDownloadDirectories(prompt); len(got) != 0 {
			t.Fatalf("source URL/path became an output directory: %v for %q", got, prompt)
		}
	}
}

func TestDownloadsDestinationUsesCurrentPositiveClause(t *testing.T) {
	for _, prompt := range []string{
		"Nie pobierz A; pobierz GIF do Pobranych",
		"Do not download the first image. Download another GIF into Downloads",
		"Nie pobierz A\nPobierz GIF do Pobranych",
	} {
		if !requestsDownloadsExport(prompt) {
			t.Fatalf("positive current clause lost its destination: %q", prompt)
		}
	}
	for _, prompt := range []string{
		"Pobierz GIF, nie do Pobranych",
		"Download a GIF from folder in Downloads",
		"Pobierz GIF zamiast do Pobranych",
		"Example:\nDownload a GIF into Downloads",
		"\"Download a GIF into Downloads\"",
	} {
		if requestsDownloadsExport(prompt) {
			t.Fatalf("source/negated/example destination authorized: %q", prompt)
		}
	}
}
