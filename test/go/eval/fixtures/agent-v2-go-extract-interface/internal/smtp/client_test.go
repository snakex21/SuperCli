package smtp

import (
	"context"
	"testing"
)

func TestClient(t *testing.T) {
	c, err := Dial("relay:25")
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Send(context.Background(), Message{To: "a@b"}); err != nil {
		t.Fatal(err)
	}
	if c.Stats().Sent != 1 {
		t.Fatal("stats")
	}
}
