package main

import (
	"bytes"
	"mime/multipart"
	"net/http/httptest"
	"strings"
	"testing"
)

// The content type is never the uploader's, and the inline list has one entry.
//
// This matters more here than in most apps: an install may run single-origin,
// APPS_PORT == PORT, which is supported — and on such an install an attachment
// that executed would execute on the platform's own origin, against the
// viewer's platform session.
func TestOnlyARealPDFIsEverInline(t *testing.T) {
	if !isPDF([]byte("%PDF-1.7\n...")) {
		t.Error("a real PDF was not recognised")
	}
	for name, body := range map[string]string{
		"html":          "<html><script>alert(1)</script>",
		"svg":           `<svg xmlns="http://www.w3.org/2000/svg"><script/></svg>`,
		"text":          "just words",
		"a lying name":  "GIF89a",
		"empty":         "",
		"nearly a pdf ": "%PD",
	} {
		if isPDF([]byte(body)) {
			t.Errorf("%s was treated as a PDF; nothing else may ever render inline", name)
		}
	}
}

// A name that is really a path is refused rather than sanitised into something
// the uploader did not write, and control characters never reach a header.
func TestFilenamesAreSafeInAHeader(t *testing.T) {
	for in, want := range map[string]string{
		`report.pdf`:    "report.pdf",
		"a\"b.pdf":      "a_b.pdf",
		"a\nb.pdf":      "a_b.pdf",
		`back\slash.md`: "back_slash.md",
		"":              "attachment",
	} {
		if got := safeFilename(in); got != want {
			t.Errorf("safeFilename(%q) = %q, want %q", in, got, want)
		}
	}
}

// A name that was sent as a path never lands as one.
//
// The interesting half is *where* that is decided: `mime/multipart` reduces the
// filename to its last segment before this app ever reads it, so a refusal here
// could never name the path it refused. The rule is still asserted, because it
// is the rule — the record must hold a filename and the disk must never see a
// directory — and the assertion is about the behaviour rather than about which
// layer produced it.
func TestAPathNeverLandsAsAFilename(t *testing.T) {
	upload := func(filename string) (string, error) {
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		f, err := mw.CreateFormFile("file", filename)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = f.Write([]byte("%PDF-1.4 hello"))
		_ = mw.Close()
		r := httptest.NewRequest("POST", "/requests/r1/attachments", &buf)
		r.Header.Set("Content-Type", mw.FormDataContentType())
		name, _, _, err := readUpload(r, 1<<20)
		return name, err
	}
	for _, bad := range []string{`../../etc/passwd`, `/etc/passwd`, `docs/change.pdf`, `C:\temp\a.pdf`} {
		name, err := upload(bad)
		if err != nil {
			continue
		}
		if strings.ContainsAny(name, `/\`) || name == ".." {
			t.Errorf("%q was stored as %q, which is still a path", bad, name)
		}
	}
	if name, err := upload("change-4001.pdf"); err != nil || name != "change-4001.pdf" {
		t.Errorf("an ordinary name came back as %q, %v", name, err)
	}
}
