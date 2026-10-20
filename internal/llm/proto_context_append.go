package llm

import "crypto/sha256"

// AppendContextText preserves the existing wire representation and owns new
// metadata/part slices. In particular, runtime text appended after admission
// must not acquire the human source of the original request.
func AppendContextText(message Message, source ContextSource, text string) Message {
	if text == "" {
		return message
	}
	suffix := "\n\n" + text
	if len(message.Parts) > 0 {
		parts := make([]ContentPart, len(message.Parts), len(message.Parts)+1)
		copy(parts, message.Parts)
		message.Parts = append(parts, ContentPart{Type: PartTypeText, Source: knownSource(source), Text: suffix})
		return message
	}
	priorSource := ContextUnknown
	var spans []ContextSpan
	if origin := message.Origin; origin != nil {
		if len(origin.Spans) == 0 {
			priorSource = knownSource(origin.Source)
		} else if origin.Digest == sha256.Sum256([]byte(message.Content)) {
			total := 0
			for _, span := range origin.Spans {
				if span.Bytes < 0 || span.Bytes > len(message.Content)-total {
					total = -1
					break
				}
				total += span.Bytes
			}
			if total == len(message.Content) {
				spans = append([]ContextSpan(nil), origin.Spans...)
			}
		}
	}
	if len(spans) == 0 && message.Content != "" {
		spans = []ContextSpan{{Source: priorSource, Bytes: len(message.Content)}}
	}
	source = knownSource(source)
	if len(spans) > 0 && spans[len(spans)-1].Source == source {
		spans[len(spans)-1].Bytes += len(suffix)
	} else {
		spans = append(spans, ContextSpan{Source: source, Bytes: len(suffix)})
	}
	message.Content += suffix
	message.Origin = &ContextOrigin{Spans: spans, Digest: sha256.Sum256([]byte(message.Content))}
	return message
}
