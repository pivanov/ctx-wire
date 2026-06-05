package scrub

import (
	"bytes"
	"io"
	"regexp"
)

// streamHoldMax bounds how many bytes the streaming scrubber will hold while
// waiting for an open multi-line secret region to terminate. Beyond this, the
// open region is redacted and discarded until its END marker arrives, so memory
// stays bounded without emitting raw secret bytes.
const streamHoldMax = 256 * 1024

// pemMarkerHold is enough suffix to detect an END marker split across writes.
const pemMarkerHold = 128

var (
	pemBeginRe = regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`)
	pemEndRe   = regexp.MustCompile(`-----END [A-Z ]*PRIVATE KEY-----`)
)

// Writer is an io.WriteCloser that scrubs secrets from a byte stream as it
// flows, with bounded memory. It emits whole lines (single-line secrets are
// fully contained in a line, so a line boundary is a safe emit point), and
// holds back any region from an unterminated PEM "BEGIN ... PRIVATE KEY" until
// the matching "END" arrives, so multi-line secrets are redacted even when they
// span multiple Write calls. Callers must Close to flush the final bytes.
type Writer struct {
	dst          io.Writer
	buf          []byte
	redactingPEM bool
	werr         error
}

// NewWriter returns a streaming scrubber writing scrubbed output to dst.
func NewWriter(dst io.Writer) *Writer { return &Writer{dst: dst} }

// Write buffers p, then scrubs and emits any prefix that is safe to release.
func (w *Writer) Write(p []byte) (int, error) {
	if w.werr != nil {
		return 0, w.werr
	}
	w.buf = append(w.buf, p...)
	if err := w.flushSafe(); err != nil {
		w.werr = err
		return 0, err
	}
	return len(p), nil
}

// Close scrubs and emits the remaining buffer, redacting any unterminated PEM
// region as a safety measure.
func (w *Writer) Close() error {
	if w.werr != nil {
		return w.werr
	}
	if len(w.buf) == 0 {
		return nil
	}
	if w.redactingPEM {
		if err := w.discardUntilPEMEnd(); err != nil {
			return err
		}
		if w.redactingPEM {
			w.buf = nil
			return nil
		}
		if len(w.buf) == 0 {
			return nil
		}
	}
	out := redactOpenPEM(Scrub(string(w.buf)))
	w.buf = nil
	_, err := io.WriteString(w.dst, out)
	return err
}

func (w *Writer) flushSafe() error {
	for {
		if w.redactingPEM {
			before := len(w.buf)
			if err := w.discardUntilPEMEnd(); err != nil {
				return err
			}
			if w.redactingPEM {
				return nil
			}
			if len(w.buf) == before {
				return nil
			}
			continue
		}

		end := w.safeEnd()
		if end <= 0 {
			return nil
		}
		if open := openPEMIndex(w.buf[:end]); open >= 0 && len(w.buf) >= streamHoldMax {
			if open > 0 {
				if _, err := io.WriteString(w.dst, Scrub(string(w.buf[:open]))); err != nil {
					return err
				}
			}
			if _, err := io.WriteString(w.dst, redacted+"\n"); err != nil {
				return err
			}
			w.buf = append([]byte(nil), w.buf[open:]...)
			w.redactingPEM = true
			continue
		}
		scrubbed := Scrub(string(w.buf[:end]))
		if _, err := io.WriteString(w.dst, scrubbed); err != nil {
			return err
		}
		rem := make([]byte, len(w.buf)-end)
		copy(rem, w.buf[end:])
		w.buf = rem
		return nil
	}
}

func (w *Writer) discardUntilPEMEnd() error {
	loc := pemEndRe.FindIndex(w.buf)
	if loc == nil {
		w.keepPEMSuffix()
		return nil
	}
	end := loc[1]
	if nl := bytes.IndexByte(w.buf[end:], '\n'); nl >= 0 {
		end += nl + 1
	}
	w.buf = append([]byte(nil), w.buf[end:]...)
	w.redactingPEM = false
	return nil
}

func (w *Writer) keepPEMSuffix() {
	if len(w.buf) <= pemMarkerHold {
		return
	}
	suffix := make([]byte, pemMarkerHold)
	copy(suffix, w.buf[len(w.buf)-pemMarkerHold:])
	w.buf = suffix
}

// safeEnd returns the length of the prefix of buf that can be scrubbed and
// emitted now without risking a partially-emitted secret. It is the position
// after the last newline, pulled back to before any unterminated PEM region.
func (w *Writer) safeEnd() int {
	nl := bytes.LastIndexByte(w.buf, '\n')
	if nl < 0 {
		if len(w.buf) >= streamHoldMax {
			return len(w.buf)
		}
		return 0
	}
	end := nl + 1
	if open := openPEMIndex(w.buf[:end]); open >= 0 {
		if len(w.buf) >= streamHoldMax {
			return end
		}
		return open
	}
	return end
}

// openPEMIndex returns the start index of an unterminated PEM private-key block
// in b (a BEGIN with no matching END after it), or -1 if none.
func openPEMIndex(b []byte) int {
	begins := pemBeginRe.FindAllIndex(b, -1)
	if len(begins) == 0 {
		return -1
	}
	last := begins[len(begins)-1]
	if pemEndRe.Match(b[last[1]:]) {
		return -1
	}
	return last[0]
}

// redactOpenPEM redacts from an unterminated PEM BEGIN to the end of s. A
// terminated block is left for Scrub to handle.
func redactOpenPEM(s string) string {
	loc := pemBeginRe.FindStringIndex(s)
	if loc == nil || pemEndRe.MatchString(s[loc[1]:]) {
		return s
	}
	return s[:loc[0]] + redacted
}
