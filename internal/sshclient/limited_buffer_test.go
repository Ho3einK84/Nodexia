package sshclient

import (
	"strings"
	"testing"
)

func TestLimitedBuffer_WritesWithinCap(t *testing.T) {
	buf := newLimitedBuffer()
	data := strings.Repeat("a", 100)
	n, err := buf.Write([]byte(data))
	if err != nil {
		t.Fatalf("Write error: %v", err)
	}
	if n != 100 {
		t.Errorf("Write returned n=%d, want 100", n)
	}
	if buf.String() != data {
		t.Errorf("buf.String() = %q, want %q", buf.String(), data)
	}
}

func TestLimitedBuffer_TruncatesAtCap(t *testing.T) {
	buf := &limitedBuffer{remaining: 10}
	_, _ = buf.Write([]byte("12345678901234567890")) // 20 bytes, cap is 10

	got := buf.String()
	if !strings.HasPrefix(got, "1234567890") {
		t.Errorf("truncated output should start with first 10 bytes, got %q", got)
	}
	if !strings.Contains(got, "truncated") {
		t.Errorf("truncated output should contain truncation notice, got %q", got)
	}
}

func TestLimitedBuffer_DropsAfterCap(t *testing.T) {
	buf := &limitedBuffer{remaining: 5}
	_, _ = buf.Write([]byte("hello")) // exactly fills the cap

	// First overflow write adds the truncation notice.
	_, err := buf.Write([]byte("overflow"))
	if err != nil {
		t.Fatalf("Write after cap should not return error: %v", err)
	}
	if !strings.Contains(buf.String(), "truncated") {
		t.Errorf("first overflow write should add truncation notice, got %q", buf.String())
	}

	afterFirst := buf.String()

	// Further writes should be fully dropped (capped = true now).
	_, _ = buf.Write([]byte("even more data"))
	if buf.String() != afterFirst {
		t.Errorf("write after capped state should be dropped; got %q", buf.String())
	}
}

func TestLimitedBuffer_ExactCapFill(t *testing.T) {
	const cap = 16
	buf := &limitedBuffer{remaining: cap}
	payload := strings.Repeat("x", cap)
	n, err := buf.Write([]byte(payload))
	if err != nil {
		t.Fatalf("Write error: %v", err)
	}
	if n != cap {
		t.Errorf("n=%d, want %d", n, cap)
	}
	// No truncation notice should be added for an exact-cap fill.
	if buf.String() != payload {
		t.Errorf("exact-cap fill should not add truncation notice, got %q", buf.String())
	}
}

func TestLimitedBuffer_FullCapReportsLength(t *testing.T) {
	buf := newLimitedBuffer()
	// Write exactly maxCommandOutputBytes.
	chunk := make([]byte, maxCommandOutputBytes)
	for i := range chunk {
		chunk[i] = 'z'
	}
	buf.Write(chunk)

	// One more write should be dropped entirely.
	buf.Write([]byte("extra"))

	result := buf.String()
	if strings.HasSuffix(result, "extra") {
		t.Error("write after full cap should be dropped")
	}
}
