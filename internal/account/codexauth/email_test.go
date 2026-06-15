package codexauth
import (
	"encoding/base64"
	"encoding/json"
	"testing"
)
func mkJWT(t *testing.T, payload map[string]any) string {
	t.Helper()
	b, _ := json.Marshal(payload)
	enc := base64.RawURLEncoding.EncodeToString
	return "h." + enc(b) + ".s"
}
func TestParseEmail_TopLevelClaim(t *testing.T) {
	jwt := mkJWT(t, map[string]any{"email": "user@example.com"})
	if got := ParseEmail(jwt); got != "user@example.com" {
		t.Errorf("ParseEmail = %q, want user@example.com", got)
	}
}
func TestParseEmail_MissingClaim(t *testing.T) {
	jwt := mkJWT(t, map[string]any{"sub": "x"})
	if got := ParseEmail(jwt); got != "" {
		t.Errorf("ParseEmail = %q, want empty", got)
	}
}
func TestParseEmail_GarbageToken(t *testing.T) {
	if got := ParseEmail("not-a-jwt"); got != "" {
		t.Errorf("ParseEmail = %q, want empty for garbage", got)
	}
}
