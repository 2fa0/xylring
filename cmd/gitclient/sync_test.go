package main

import (
	"testing"
)

func TestSyncSets(t *testing.T) {
	const (
		a = "1111111111111111111111111111111111111111"
		b = "2222222222222222222222222222222222222222"
		c = "3333333333333333333333333333333333333333"
	)
	hashes := func(lines ...string) [][20]byte {
		var out [][20]byte
		for _, line := range lines {
			h, err := parseHash(line)
			if err != nil {
				t.Fatalf("parseHash(%q): %v", line, err)
			}
			out = append(out, h)
		}
		return out
	}
	same := func(t *testing.T, side string, got, want [][20]byte) {
		t.Helper()
		if len(got) != len(want) {
			t.Fatalf("%s: got %d hashes, want %d", side, len(got), len(want))
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("%s[%d]: got %s, want %s", side, i, hashHex(got[i]), hashHex(want[i]))
			}
		}
	}

	t.Run("empty", func(t *testing.T) {
		incoming, outgoing := parseSyncSets("")
		if len(incoming) != 0 || len(outgoing) != 0 {
			t.Fatalf("got incoming %v outgoing %v", incoming, outgoing)
		}
	})

	t.Run("one side only", func(t *testing.T) {
		incoming, outgoing := parseSyncSets("<" + a + "\n<" + b + "\n")
		same(t, "incoming", incoming, hashes(a, b))
		same(t, "outgoing", outgoing, nil)

		incoming, outgoing = parseSyncSets(">" + c + "\n")
		same(t, "incoming", incoming, nil)
		same(t, "outgoing", outgoing, hashes(c))
	})

	t.Run("both sides", func(t *testing.T) {
		incoming, outgoing := parseSyncSets(">" + b + "\n<" + a + "\n>" + c + "\n")
		same(t, "incoming", incoming, hashes(a))
		same(t, "outgoing", outgoing, hashes(b, c))
	})

	t.Run("noisy lines", func(t *testing.T) {
		// Trailing spaces, blank lines and a short hash are skipped, not counted.
		incoming, outgoing := parseSyncSets("  >" + c + "  \n\n<" + a + "\n<deadbeef\n")
		same(t, "incoming", incoming, hashes(a))
		same(t, "outgoing", outgoing, hashes(c))
		if ahead, behind := len(outgoing), len(incoming); ahead != 1 || behind != 1 {
			t.Fatalf("counts: ahead %d behind %d", ahead, behind)
		}
	})
}
