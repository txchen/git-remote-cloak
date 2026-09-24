package engine

import (
	"testing"

	cloakformat "github.com/txchen/git-remote-cloak/internal/format"
)

func TestAutomaticCompactionPolicy(t *testing.T) {
	tests := []struct {
		name        string
		packs       int
		compacted   uint64
		added       uint64
		legacy      bool
		wantCompact bool
	}{
		{name: "tiny snapshot with thirty-two packs", packs: 32, compacted: 1024, added: 100_000},
		{name: "thirty-third pack", packs: 33, compacted: 1024, added: 100_000, wantCompact: true},
		{name: "single large update", packs: 2, compacted: 2 << 20, added: 2 << 20},
		{name: "seven large packs", packs: 7, compacted: 2 << 20, added: 2 << 20},
		{name: "eight substantial packs", packs: 8, compacted: 2 << 20, added: 2 << 20, wantCompact: true},
		{name: "absolute byte floor", packs: 8, compacted: 1024, added: (1 << 20) - 1},
		{name: "relative byte threshold", packs: 8, compacted: 4 << 20, added: 1 << 20},
		{name: "legacy snapshot", packs: 1, legacy: true, wantCompact: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repository := cloakformat.SnapshotState{
				CompactedSize: test.compacted, AddedSinceCompaction: test.added,
			}
			if got := automaticCompactionDue(repository, test.packs, test.legacy); got != test.wantCompact {
				t.Fatalf("automaticCompactionDue() = %t, want %t", got, test.wantCompact)
			}
		})
	}
}
