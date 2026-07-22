// Command pagemark fetches a web page and writes extracted Markdown.
package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ryanfowler/pagemark"
	"golang.org/x/net/html"
	"golang.org/x/net/html/charset"
)

const defaultMaxBytes int64 = 10 << 20

func main() {
	client := newHTTPClient()
	if err := run(context.Background(), os.Args[1:], os.Stdout, os.Stderr, client); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer, client *http.Client) error {
	flags := flag.NewFlagSet("pagemark", flag.ContinueOnError)
	flags.SetOutput(stderr)
	timeout := flags.Duration("timeout", 30*time.Second, "maximum fetch time")
	maxBytes := flags.Int64("max-bytes", defaultMaxBytes, "maximum HTML input bytes")
	userAgent := flags.String("user-agent", "pagemark/0.1", "HTTP User-Agent value")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 1 {
		return errors.New("usage: pagemark [options] URL")
	}
	if *timeout <= 0 {
		return errors.New("pagemark: timeout must be greater than zero")
	}
	if *maxBytes <= 0 {
		return errors.New("pagemark: max-bytes must be greater than zero")
	}
	pageURL, err := parsePageURL(flags.Arg(0))
	if err != nil {
		return err
	}

	requestCtx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, pageURL.String(), nil)
	if err != nil {
		return fmt.Errorf("pagemark: create request: %w", err)
	}
	req.Header.Set("User-Agent", *userAgent)
	req.Header.Set("Accept", "text/html, application/xhtml+xml")

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("pagemark: fetch page: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("pagemark: server returned %s", resp.Status)
	}
	contentType := resp.Header.Get("Content-Type")
	normalizedContentType := strings.ToLower(contentType)
	if contentType != "" && !strings.Contains(normalizedContentType, "text/html") && !strings.Contains(normalizedContentType, "application/xhtml+xml") {
		return fmt.Errorf("pagemark: response is not HTML: %s", contentType)
	}
	// Buffer at most maxBytes plus one byte so encoding decisions can consider
	// the complete (bounded) response without weakening the input limit.
	readLimit := *maxBytes
	if readLimit < math.MaxInt64 {
		readLimit++
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, readLimit))
	if err != nil {
		return fmt.Errorf("pagemark: read response body: %w", err)
	}
	if int64(len(body)) > *maxBytes {
		return &pagemark.LimitError{Resource: "input bytes", Count: int64(len(body)), Max: *maxBytes}
	}
	utf8Body, err := decodeHTML(body, contentType)
	if err != nil {
		return fmt.Errorf("pagemark: decode HTML: %w", err)
	}

	doc, err := pagemark.Extract(utf8Body, resp.Request.URL.String(), pagemark.WithMaxInputBytes(*maxBytes))
	if err != nil {
		return err
	}
	if _, err := io.WriteString(stdout, doc.Markdown+"\n"); err != nil {
		return fmt.Errorf("pagemark: write output: %w", err)
	}
	return nil
}

func decodeHTML(body []byte, contentType string) (io.Reader, error) {
	// Preserve charset.DetermineEncoding's BOM, recognized HTTP charset, and
	// early HTML metadata precedence. Only override its default Windows-1252
	// fallback when checking the complete body establishes that it is UTF-8.
	_, name, certain := charset.DetermineEncoding(body, contentType)
	if certain || name != "windows-1252" || hasMetaCharset(body, name) || !utf8.Valid(body) {
		return charset.NewReader(bytes.NewReader(body), contentType)
	}
	return bytes.NewReader(body), nil
}

// hasMetaCharset distinguishes a detected Windows-1252 meta declaration from
// DetermineEncoding's indistinguishable Windows-1252 default. DetermineEncoding
// only examines the first 1024 bytes, so this helper uses the same window.
func hasMetaCharset(body []byte, detectedName string) bool {
	if len(body) > 1024 {
		body = body[:1024]
	}
	z := html.NewTokenizer(bytes.NewReader(body))
	for {
		switch z.Next() {
		case html.ErrorToken:
			return false
		case html.StartTagToken, html.SelfClosingTagToken:
			token := z.Token()
			if !strings.EqualFold(token.Data, "meta") {
				continue
			}
			attrs := make(map[string]string, len(token.Attr))
			for _, attr := range token.Attr {
				key := strings.ToLower(attr.Key)
				if _, exists := attrs[key]; !exists {
					attrs[key] = attr.Val
				}
			}
			if metaCharsetMatches(attrs["charset"], detectedName) {
				return true
			}
			if strings.EqualFold(attrs["http-equiv"], "content-type") &&
				metaCharsetMatches(metaContentCharset(attrs["content"]), detectedName) {
				return true
			}
		}
	}
}

// metaContentCharset follows the permissive extraction used by the HTML
// charset prescan. A content attribute need not be a valid MIME media type.
func metaContentCharset(content string) string {
	s := strings.ToLower(content)
	for s != "" {
		charsetAt := strings.Index(s, "charset")
		if charsetAt < 0 {
			return ""
		}
		s = strings.TrimLeft(s[charsetAt+len("charset"):], " \t\n\f\r")
		if !strings.HasPrefix(s, "=") {
			continue
		}
		s = strings.TrimLeft(s[1:], " \t\n\f\r")
		if s == "" {
			return ""
		}
		if quote := s[0]; quote == '\'' || quote == '"' {
			s = s[1:]
			if end := strings.IndexByte(s, quote); end >= 0 {
				return s[:end]
			}
			return ""
		}
		if end := strings.IndexAny(s, "; \t\n\f\r"); end >= 0 {
			return s[:end]
		}
		return s
	}
	return ""
}

func metaCharsetMatches(label, detectedName string) bool {
	if label == "" {
		return false
	}
	_, name := charset.Lookup(label)
	if strings.HasPrefix(name, "utf-16") {
		name = "utf-8"
	}
	return name == detectedName
}

func parsePageURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil {
		return nil, errors.New("pagemark: URL must be an absolute HTTP or HTTPS URL without credentials")
	}
	return u, nil
}

func safeRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 10 {
		return errors.New("pagemark: too many redirects")
	}
	if _, err := parsePageURL(req.URL.String()); err != nil {
		return errors.New("pagemark: redirect has an unsafe URL")
	}
	return nil
}
