// Command pagemark fetches a web page and writes extracted Markdown.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/ryanfowler/pagemark"
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
	utf8Body, err := charset.NewReader(resp.Body, contentType)
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
