package llm

// WorkerNotificationName marks host-authored background reports. They use the
// user transport role for model delivery but are not human conversation turns.
const WorkerNotificationName = "supercli_worker_notification"

// workerNotificationContext counts only host-provenance messages, never report-
// looking human text or ordinary tool results. Bytes are UTF-8 message text in
// the supplied request view, not serialized network bytes or estimated tokens.
func workerNotificationContext(messages []Message) (count, textBytes int64) {
	for _, msg := range messages {
		if msg.Role != RoleUser || msg.Name != WorkerNotificationName {
			continue
		}
		count++
		if len(msg.Parts) == 0 {
			textBytes += int64(len(msg.Content))
			continue
		}
		for _, part := range msg.Parts {
			if part.Type == PartTypeText {
				textBytes += int64(len(part.Text))
			}
		}
	}
	return
}
