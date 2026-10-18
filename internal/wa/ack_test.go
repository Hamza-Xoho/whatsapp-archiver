package wa

import (
	"testing"

	"whatsapp-archiver/internal/config"
)

// A message whose first delivery is not stored is redelivered by WhatsApp. Its ratchet has already advanced, so it is
// decrypted again only from the decrypted-event buffer; without the buffer a redelivered message is acked and lost.
func TestOpenBuffersDecryptedEventsUntilStored(t *testing.T) {
	cfg := &config.Config{DataDir: t.TempDir(), History: config.HistoryRules{DaysLimit: 1, SizeMBLimit: 1}}
	c, err := Open(t.Context(), cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if !c.client.EnableDecryptedEventBuffer {
		t.Fatal("the decrypted-event buffer is off: a redelivered message would be acked and never stored")
	}
	if !c.client.SynchronousAck {
		t.Fatal("acks must wait for the handler")
	}
}
