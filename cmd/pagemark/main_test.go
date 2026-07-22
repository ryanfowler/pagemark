package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRunFetchesAndWritesMarkdown(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Accept"); !strings.Contains(got, "text/html") {
			t.Errorf("Accept = %q", got)
		}
		if got := r.Header.Get("User-Agent"); got != "test-agent" {
			t.Errorf("User-Agent = %q", got)
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<main><h1>Guide</h1><p>Install the tool.</p></main>`))
	}))
	defer server.Close()

	var stdout, stderr bytes.Buffer
	err := run(context.Background(), []string{"-user-agent", "test-agent", server.URL}, &stdout, &stderr, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if got, want := stdout.String(), "# Guide\n\nInstall the tool.\n"; got != want {
		t.Fatalf("output:\n%q\nwant:\n%q", got, want)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr: %s", &stderr)
	}
}

func TestRunDoesNotDecodeUTF8ResponseTwice(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<main><h1>The Psychology of Software Teams</h1><p>You’ll read “the team’s guide” ↩</p></main>`))
	}))
	defer server.Close()

	var stdout bytes.Buffer
	if err := run(context.Background(), []string{server.URL}, &stdout, &bytes.Buffer{}, server.Client()); err != nil {
		t.Fatal(err)
	}
	if got, want := stdout.String(), "# The Psychology of Software Teams\n\nYou’ll read “the team’s guide” ↩\n"; got != want {
		t.Fatalf("output:\n%q\nwant:\n%q", got, want)
	}
}

func TestRunPreservesUTF8WhenCharsetDeclarationIsLate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		body := `<html><head><style>` + strings.Repeat("a", 2048) +
			`</style><meta http-equiv="content-type" content="text/html; charset=utf-8"></head>` +
			`<body><main><p>It’s correct and doesn’t become mojibake.</p></main></body></html>`
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()

	var stdout bytes.Buffer
	if err := run(context.Background(), []string{server.URL}, &stdout, &bytes.Buffer{}, server.Client()); err != nil {
		t.Fatal(err)
	}
	got := stdout.String()
	if !strings.Contains(got, "It’s") || !strings.Contains(got, "doesn’t") {
		t.Fatalf("output does not preserve UTF-8: %q", got)
	}
	if strings.Contains(got, "â€™") {
		t.Fatalf("output contains mojibake: %q", got)
	}
}

func TestRunHonorsUTF8BOMOverHTTPCharset(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=windows-1252")
		_, _ = w.Write(append([]byte{0xef, 0xbb, 0xbf}, []byte(`<main><p>It’s UTF-8.</p></main>`)...))
	}))
	defer server.Close()

	var stdout bytes.Buffer
	if err := run(context.Background(), []string{server.URL}, &stdout, &bytes.Buffer{}, server.Client()); err != nil {
		t.Fatal(err)
	}
	if got, want := stdout.String(), "It’s UTF-8.\n"; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}

func TestRunDecodesISO2022JPFromMetaCharset(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		body := []byte("<meta charset=\"iso-2022-jp\"><main><p>\x1b$BF|K\\8l$G$9!#\x1b(B</p></main>")
		_, _ = w.Write(body)
	}))
	defer server.Close()

	var stdout bytes.Buffer
	if err := run(context.Background(), []string{server.URL}, &stdout, &bytes.Buffer{}, server.Client()); err != nil {
		t.Fatal(err)
	}
	if got, want := stdout.String(), "日本語です。\n"; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}

func TestRunHonorsPermissiveMetaCharset(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<meta http-equiv="content-type" content="charset=windows-1252"><main><p>It’s labeled Windows-1252.</p></main>`))
	}))
	defer server.Close()

	var stdout bytes.Buffer
	if err := run(context.Background(), []string{server.URL}, &stdout, &bytes.Buffer{}, server.Client()); err != nil {
		t.Fatal(err)
	}
	if got, want := stdout.String(), "Itâ€™s labeled Windows-1252.\n"; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}

func TestRunIgnoresUnknownHTTPCharset(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=unknown")
		_, _ = w.Write([]byte(`<main><p>It’s valid UTF-8.</p></main>`))
	}))
	defer server.Close()

	var stdout bytes.Buffer
	if err := run(context.Background(), []string{server.URL}, &stdout, &bytes.Buffer{}, server.Client()); err != nil {
		t.Fatal(err)
	}
	if got, want := stdout.String(), "It’s valid UTF-8.\n"; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}

func TestRunDecodesResponseToUTF8(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=iso-8859-1")
		_, _ = w.Write([]byte("<main><h1>Caf\xe9</h1><p>Cr\xe8me br\xfbl\xe9e.</p></main>"))
	}))
	defer server.Close()

	var stdout bytes.Buffer
	if err := run(context.Background(), []string{server.URL}, &stdout, &bytes.Buffer{}, server.Client()); err != nil {
		t.Fatal(err)
	}
	if got, want := stdout.String(), "# Café\n\nCrème brûlée.\n"; got != want {
		t.Fatalf("output:\n%q\nwant:\n%q", got, want)
	}
}

func TestRunRejectsNonHTMLAndBadStatus(t *testing.T) {
	tests := []struct {
		name        string
		status      int
		contentType string
		want        string
	}{
		{"non-HTML", http.StatusOK, "application/json", "response is not HTML"},
		{"bad status", http.StatusNotFound, "text/html", "server returned 404 Not Found"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", test.contentType)
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(`{"error":true}`))
			}))
			defer server.Close()
			err := run(context.Background(), []string{server.URL}, &bytes.Buffer{}, &bytes.Buffer{}, server.Client())
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want text %q", err, test.want)
			}
		})
	}
}

func TestRunValidatesArgumentsAndLimits(t *testing.T) {
	client := &http.Client{}
	for _, args := range [][]string{{}, {"ftp://example.com"}, {"https://user:pass@example.com"}, {"-timeout", "0s", "https://example.com"}, {"-max-bytes", "0", "https://example.com"}} {
		if err := run(context.Background(), args, &bytes.Buffer{}, &bytes.Buffer{}, client); err == nil {
			t.Fatalf("run(%q) did not return an error", args)
		}
	}
}

func TestSafeRedirect(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "https://example.com", nil)
	if err := safeRedirect(req, nil); err != nil {
		t.Fatal(err)
	}
	bad := httptest.NewRequest(http.MethodGet, "http://example.com", nil)
	bad.URL.Scheme = "file"
	if err := safeRedirect(bad, nil); err == nil {
		t.Fatal("unsafe redirect was accepted")
	}
	via := make([]*http.Request, 10)
	if err := safeRedirect(req, via); err == nil {
		t.Fatal("redirect limit was not enforced")
	}
}
