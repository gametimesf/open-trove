//go:build integration

package main

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestMyTroveActivityEndToEnd(t *testing.T) {
	client := newClient()
	prefix := fmt.Sprintf("activity-%d", time.Now().UnixNano())
	first := uploadFile(t, client, "first.txt", []byte("first"), prefix+"-first", false)["slug"]
	var archive bytes.Buffer
	zw := zip.NewWriter(&archive)
	page, err := zw.Create("index.html")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := page.Write([]byte("<h1>Activity site</h1>")); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	site := uploadFile(t, client, "site.zip", archive.Bytes(), prefix+"-site", false)["slug"]
	get := func(c *http.Client, path string) string {
		t.Helper()
		r, err := c.Get(baseURL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Body.Close()
		b, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		if r.StatusCode != http.StatusOK {
			t.Fatalf("GET %s: %d %s", path, r.StatusCode, b)
		}
		return string(b)
	}
	for _, path := range []string{"/" + first, "/" + site, "/" + first} {
		get(client, path)
	}
	pageHTML := get(client, "/mine")
	recent := strings.Split(pageHTML, "<h2>Recently Viewed</h2>")[1]
	if a, b := strings.Index(recent, `href="/`+first+`"`), strings.Index(recent, `href="/`+site+`"`); a < 0 || b < a {
		t.Fatalf("own revisit order incorrect: %s", recent)
	}
	if strings.Count(recent, "Uploaded by "+integrationUserEmail) != 2 {
		t.Fatalf("missing attribution: %s", recent)
	}
	// Distinct browser: owner metadata must survive S3, not come from its uploads.
	viewer := newClient()
	get(viewer, "/"+first)
	get(viewer, "/"+site)
	if html := get(viewer, "/mine"); strings.Count(html, "Uploaded by "+integrationUserEmail) != 2 {
		t.Fatalf("cross-browser attribution missing: %s", html)
	}
	slugs := []string{first, site}
	for i := range 2 {
		slugs = append(slugs, uploadFile(t, client, fmt.Sprintf("doc-%d.txt", i), []byte("parallel"), fmt.Sprintf("%s-%d", prefix, i), false)["slug"])
	}
	parallel := newClient()
	get(parallel, "/") // Establish one cookie before concurrent navigations.
	var wg sync.WaitGroup
	errs := make(chan error, len(slugs))
	for _, slug := range slugs {
		wg.Go(func() {
			r, err := parallel.Get(baseURL + "/" + slug)
			if err != nil {
				errs <- err
				return
			}
			defer r.Body.Close()
			_, err = io.Copy(io.Discard, r.Body)
			if err != nil {
				errs <- err
			}
			if r.StatusCode != 200 {
				errs <- fmt.Errorf("view %s status %d", slug, r.StatusCode)
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	html := get(parallel, "/mine")
	for _, slug := range slugs {
		if strings.Count(html, `href="/`+slug+`"`) != 1 {
			t.Errorf("concurrent view missing or duplicated: %s", slug)
		}
	}
}
