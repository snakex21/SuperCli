package agent

import "testing"

func TestPublicAssetSourcesDoNotDependOnProviderName(t *testing.T) {
	for _, prompt := range []string{
		"Pobierz GIF z AzaleaMedia do Pobranych.",
		"Can you find a funny GIF on FrameDepot?",
		"Download an image from MaplePhotos.",
		"Pobierz zdjęcie ze strony internetowej.",
		"Find audio on WaveShelf.",
		"Download a PDF from PaperArchive.",
		"Pobierz zasób z serwisu AssetLibrary.",
		"Search a website for current weather.",
		"Find images at https://gallery.arbitrary.example/view/42",
		"Pobierz https://archive.different.example/document.pdf",
	} {
		if !isSelfContainedWebRequest(prompt) {
			t.Errorf("generic public source missed: %q", prompt)
		}
	}
	for _, prompt := range []string{
		"AzaleaMedia", "Find something on FrameDepot.", "Download game assets.",
		"Find an image in this repository.", "Download a PDF from my local folder.",
		"Pobierz dźwięk z dysku.", "Download a resource from the project and run tests.",
		"Find a GIF on FrameDepot and implement the preview function.",
		"Do not download a GIF from AzaleaMedia.", "The model tried to download an image from MaplePhotos.",
		"Explain the command: download a PDF from PaperArchive.", "Example:\n download audio from WaveShelf.",
		"The prompt says `find a GIF on FrameDepot`.",
	} {
		if isSelfContainedWebRequest(prompt) {
			t.Errorf("local, ambiguous or reported work bypassed normal routing: %q", prompt)
		}
	}
}

func TestPageContractUsesResourceShapeAcrossArbitraryHosts(t *testing.T) {
	for _, host := range []string{"gallery.arbitrary.example", "storage.other.example", "new-source.example"} {
		for _, path := range []string{"/view/42", "/download?id=42", "/details.html"} {
			prompt := "Download https://" + host + path
			if !needsDownloadPageContract(prompt) {
				t.Errorf("page contract missing: %q", prompt)
			}
		}
		for _, path := range []string{"/a.gif", "/photo.PNG?size=large", "/sound.mp3", "/document.pdf"} {
			prompt := "Download https://" + host + path
			if needsDownloadPageContract(prompt) {
				t.Errorf("direct asset paid for page contract: %q", prompt)
			}
		}
	}
}
