package media

import (
	"bytes"
	"testing"

	"whatsapp-archiver/internal/config"
	"whatsapp-archiver/internal/store"
)

func TestDecidePreservesProtoMetadataAndAssignsSourcePriorityAtEnqueue(t *testing.T) {
	for _, tt := range []struct {
		source string
		rank   int
	}{
		{"live", 1},
		{"history", 2},
		{"replay", 2},
	} {
		t.Run(tt.source, func(t *testing.T) {
			q := newTestQueue(t, nil, &fakeDown{}, config.MediaRules{Download: []string{"document"}}, newClock(), nil)
			thumb := []byte{0xff, 0xd8, 1, 2, 3}
			m := store.Media{ChatJID: "g@g.us", MessageID: "M", Source: tt.source, Type: "document", MIME: "application/pdf",
				DeclaredSize: 77_000, Filename: "syllabus.pdf", DurationS: 12, Width: 640, Height: 480, Pages: 14, Thumb: thumb}
			if err := q.Decide(t.Context(), &m); err != nil {
				t.Fatal(err)
			}
			if m.SourceRank != tt.rank {
				t.Fatalf("source rank = %d, want %d", m.SourceRank, tt.rank)
			}
			if m.Filename != "syllabus.pdf" || m.DurationS != 12 || m.Width != 640 || m.Height != 480 || m.Pages != 14 || !bytes.Equal(m.Thumb, thumb) {
				t.Fatalf("metadata changed at enqueue: %+v", m)
			}
		})
	}
}
