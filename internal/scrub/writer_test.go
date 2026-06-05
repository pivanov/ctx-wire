package scrub

import (
	"bytes"
	"strings"
	"testing"
)

// writeInChunks feeds s to a Writer in fixed-size chunks to exercise boundary
// handling, then closes and returns the emitted output.
func writeInChunks(t *testing.T, s string, chunk int) string {
	t.Helper()
	var dst bytes.Buffer
	w := NewWriter(&dst)
	for i := 0; i < len(s); i += chunk {
		end := i + chunk
		if end > len(s) {
			end = len(s)
		}
		if _, err := w.Write([]byte(s[i:end])); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	return dst.String()
}

func TestStreamingScrubSingleLine(t *testing.T) {
	in := "line one\nPASSWORD=hunter2 here\nline three\n"
	for _, chunk := range []int{1, 3, 7, 1000} {
		got := writeInChunks(t, in, chunk)
		if strings.Contains(got, "hunter2") {
			t.Errorf("chunk=%d: secret leaked: %q", chunk, got)
		}
		if !strings.Contains(got, "[REDACTED]") {
			t.Errorf("chunk=%d: expected redaction: %q", chunk, got)
		}
		if !strings.Contains(got, "line one") || !strings.Contains(got, "line three") {
			t.Errorf("chunk=%d: dropped non-secret content: %q", chunk, got)
		}
	}
}

func TestStreamingScrubMultiLinePEMAcrossChunks(t *testing.T) {
	pem := "-----BEGIN RSA PRIVATE KEY-----\n" +
		"MIIBVgIBADANBgkqhkiG9w0BAQEFAASCAUAwggE8\n" +
		"AgEAAkEA1234567890abcdefSECRETKEYMATERIAL\n" +
		"-----END RSA PRIVATE KEY-----\n"
	in := "before the key\n" + pem + "after the key\n"

	// Tiny chunks force the PEM block to span many Write calls.
	for _, chunk := range []int{1, 4, 16, 64} {
		got := writeInChunks(t, in, chunk)
		if strings.Contains(got, "SECRETKEYMATERIAL") {
			t.Errorf("chunk=%d: PEM body leaked: %q", chunk, got)
		}
		if strings.Contains(got, "MIIBVgIBADANBg") {
			t.Errorf("chunk=%d: PEM body leaked: %q", chunk, got)
		}
		if !strings.Contains(got, "before the key") || !strings.Contains(got, "after the key") {
			t.Errorf("chunk=%d: dropped surrounding content: %q", chunk, got)
		}
	}
}

func TestStreamingScrubUnterminatedPEMAtClose(t *testing.T) {
	in := "log\n-----BEGIN RSA PRIVATE KEY-----\nMIIBVgIBADANBgkLEAKED\n" // no END, stream ends
	got := writeInChunks(t, in, 8)
	if strings.Contains(got, "MIIBVgIBADANBgLEAKED") || strings.Contains(got, "LEAKED") {
		t.Errorf("unterminated PEM body leaked: %q", got)
	}
	if !strings.Contains(got, "[REDACTED]") {
		t.Errorf("expected redaction of unterminated PEM: %q", got)
	}
}

func TestStreamingScrubOversizedPEMDoesNotLeakBeforeEnd(t *testing.T) {
	var dst bytes.Buffer
	w := NewWriter(&dst)
	if _, err := w.Write([]byte("before\n-----BEGIN RSA PRIVATE KEY-----\n")); err != nil {
		t.Fatalf("Write begin: %v", err)
	}
	body := bytes.Repeat([]byte("SECRETKEYMATERIAL\n"), streamHoldMax/18+100)
	for i := 0; i < len(body); i += 4096 {
		end := i + 4096
		if end > len(body) {
			end = len(body)
		}
		if _, err := w.Write(body[i:end]); err != nil {
			t.Fatalf("Write body: %v", err)
		}
		if strings.Contains(dst.String(), "SECRETKEYMATERIAL") {
			t.Fatalf("PEM body leaked before END marker")
		}
		if len(w.buf) > streamHoldMax+4096 {
			t.Fatalf("buffer grew unbounded: %d bytes", len(w.buf))
		}
	}
	if _, err := w.Write([]byte("-----END RSA PRIVATE KEY-----\nafter\n")); err != nil {
		t.Fatalf("Write end: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	got := dst.String()
	if strings.Contains(got, "SECRETKEYMATERIAL") || strings.Contains(got, "BEGIN RSA PRIVATE KEY") || strings.Contains(got, "END RSA PRIVATE KEY") {
		t.Fatalf("PEM leaked in output: %q", got)
	}
	if !strings.Contains(got, "before\n") || !strings.Contains(got, "[REDACTED]") || !strings.Contains(got, "after\n") {
		t.Fatalf("expected surrounding content and redaction, got %q", got)
	}
}

func TestStreamingScrubBoundedMemory(t *testing.T) {
	// A huge run of non-newline bytes must not grow the buffer without bound.
	var dst bytes.Buffer
	w := NewWriter(&dst)
	blob := bytes.Repeat([]byte("x"), 4096)
	for i := 0; i < 80; i++ { // ~320 KiB, no newline; crosses the hold cap
		if _, err := w.Write(blob); err != nil {
			t.Fatalf("Write: %v", err)
		}
		if len(w.buf) > streamHoldMax+len(blob) {
			t.Fatalf("buffer grew unbounded: %d bytes", len(w.buf))
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}
