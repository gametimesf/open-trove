// Command scan walks the trove uploads bucket and runs every object
// through the same upload-intake Inspector the server uses. Objects the
// inspector blocks get `intake-flagged: true` plus a short reason
// written to their S3 user metadata (preserving the original bytes via
// CopyObject self-copy with MetadataDirective: REPLACE). Internal
// objects under the `_` prefix (`_users/`, `_sites/`, `_prompt`) are
// skipped.
//
// Operators run this on demand to backfill flags on uploads that
// predate the live gate or that were uploaded via paths that bypass
// it.
//
//	go run ./cmd/scan --bucket trove-uploads-staging --dry-run
//	go run ./cmd/scan --bucket trove-uploads-staging --concurrency 4
//	go run ./cmd/scan --bucket trove-uploads-staging --keys salary-jeff,promo-rec
//	go run ./cmd/scan --bucket trove-uploads-staging --report scan-results.ndjson
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/gametimesf/open-trove/intake"
	"github.com/gametimesf/open-trove/internal/config"
)

const (
	mdFlagged    = "intake-flagged"
	mdReason     = "intake-flag-reason"
	mdCategories = "intake-flag-categories"
)

// Result is the per-object record written to the report file (NDJSON).
type Result struct {
	Key         string    `json:"key"`
	Filename    string    `json:"filename,omitempty"`
	ContentType string    `json:"content_type,omitempty"`
	Bytes       int       `json:"bytes,omitempty"`
	Allowed     bool      `json:"allowed"`
	Reason      string    `json:"reason,omitempty"`
	Categories  []string  `json:"categories,omitempty"`
	Error       string    `json:"error,omitempty"`
	DurationMS  int64     `json:"duration_ms"`
	ScannedAt   time.Time `json:"scanned_at"`
	Wrote       bool      `json:"wrote_metadata,omitempty"`
}

func main() {
	var (
		bucket      = flag.String("bucket", "", "S3 bucket (defaults to cfg.Store.S3.Bucket)")
		prefix      = flag.String("prefix", "", "only scan keys under this prefix (skips _* internal keys regardless)")
		dryRun      = flag.Bool("dry-run", false, "report verdicts without writing intake-flagged metadata")
		limit       = flag.Int("limit", 0, "max objects to scan (0 = all)")
		concurrency = flag.Int("concurrency", 4, "parallel inspector calls")
		report      = flag.String("report", "", "write NDJSON report to this path (one Result per line)")
		keysCSV     = flag.String("keys", "", "comma-separated list of explicit keys to scan (skips listing)")
		keysFile    = flag.String("keys-file", "", "path to file with one key per line")
		verbose     = flag.Bool("v", false, "print verdicts for allowed objects too")
	)
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("loading config: %v", err)
	}
	bkt := *bucket
	if bkt == "" {
		bkt = cfg.Store.S3.Bucket
	}
	if bkt == "" {
		log.Fatal("scan: no bucket configured (set --bucket or cfg.Store.S3.Bucket)")
	}
	if *concurrency < 1 {
		*concurrency = 1
	}

	inspector, _, err := intake.BuildInspector(cfg)
	if err != nil {
		log.Fatalf("building inspector: %v", err)
	}
	if _, ok := inspector.(intake.NoOp); ok {
		log.Fatal("scan: intake is disabled in config — nothing to do")
	}

	ctx := context.Background()
	s3client, err := newS3Client(cfg.Store.S3.Endpoint, cfg.Store.S3.Region)
	if err != nil {
		log.Fatalf("building S3 client: %v", err)
	}

	keys, err := resolveKeys(ctx, s3client, bkt, *prefix, *keysCSV, *keysFile, *limit)
	if err != nil {
		log.Fatalf("resolving keys: %v", err)
	}
	if len(keys) == 0 {
		log.Print("scan: no keys to scan")
		return
	}

	mode := "live"
	if *dryRun {
		mode = "dry-run"
	}
	log.Printf("scan: bucket=%s objects=%d concurrency=%d mode=%s", bkt, len(keys), *concurrency, mode)

	var reportFile *bufio.Writer
	if *report != "" {
		f, err := os.Create(*report)
		if err != nil {
			log.Fatalf("opening report file: %v", err)
		}
		defer func() { _ = f.Close() }()
		reportFile = bufio.NewWriter(f)
		defer func() {
			if err := reportFile.Flush(); err != nil {
				log.Printf("WARN flushing report: %v", err)
			}
		}()
	}

	results := runScan(ctx, inspector, s3client, bkt, keys, *concurrency, *dryRun, *verbose, reportFile)
	printSummary(results)
}

// resolveKeys returns the list of keys to scan based on flags. Explicit
// --keys / --keys-file always wins; otherwise list the bucket.
func resolveKeys(ctx context.Context, c *s3.Client, bucket, prefix, csv, file string, limit int) ([]string, error) {
	if csv != "" || file != "" {
		var out []string
		for _, k := range strings.Split(csv, ",") {
			if k = strings.TrimSpace(k); k != "" {
				out = append(out, k)
			}
		}
		if file != "" {
			f, err := os.Open(file)
			if err != nil {
				return nil, fmt.Errorf("opening keys file: %w", err)
			}
			defer f.Close()
			scanner := bufio.NewScanner(f)
			for scanner.Scan() {
				if k := strings.TrimSpace(scanner.Text()); k != "" {
					out = append(out, k)
				}
			}
			if err := scanner.Err(); err != nil {
				return nil, fmt.Errorf("reading keys file: %w", err)
			}
		}
		return out, nil
	}
	return listKeys(ctx, c, bucket, prefix, limit)
}

// listKeys returns object keys under the prefix, skipping internal `_*`
// keys and any nested keys (site assets are scanned via their slug
// rather than per-page for now).
func listKeys(ctx context.Context, c *s3.Client, bucket, prefix string, limit int) ([]string, error) {
	var (
		keys     []string
		nextTok  *string
	)
	for {
		out, err := c.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket:            aws.String(bucket),
			Prefix:            aws.String(prefix),
			ContinuationToken: nextTok,
		})
		if err != nil {
			return nil, err
		}
		for _, obj := range out.Contents {
			k := aws.ToString(obj.Key)
			if strings.HasPrefix(k, "_") || strings.Contains(k, "/") {
				continue
			}
			keys = append(keys, k)
			if limit > 0 && len(keys) >= limit {
				return keys, nil
			}
		}
		if out.NextContinuationToken == nil {
			return keys, nil
		}
		nextTok = out.NextContinuationToken
	}
}

// runScan dispatches keys to a worker pool, prints per-object progress,
// and returns the collected results.
func runScan(ctx context.Context, insp intake.Inspector, c *s3.Client, bucket string,
	keys []string, concurrency int, dryRun, verbose bool, report *bufio.Writer) []Result {

	type job struct {
		i   int
		key string
	}
	jobs := make(chan job)
	resultsCh := make(chan Result, len(keys))

	var wg sync.WaitGroup
	for w := 0; w < concurrency; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				resultsCh <- inspectOne(ctx, insp, c, bucket, j.key, dryRun)
			}
		}()
	}

	go func() {
		for i, k := range keys {
			jobs <- job{i: i, key: k}
		}
		close(jobs)
		wg.Wait()
		close(resultsCh)
	}()

	results := make([]Result, 0, len(keys))
	done := 0
	for r := range resultsCh {
		done++
		results = append(results, r)
		printProgress(done, len(keys), r, verbose)
		if report != nil {
			line, _ := json.Marshal(r)
			report.Write(line)
			report.WriteByte('\n')
		}
	}
	return results
}

// inspectOne is one unit of work: HEAD + GET + Inspect + (optionally) write metadata.
func inspectOne(ctx context.Context, insp intake.Inspector, c *s3.Client, bucket, key string, dryRun bool) (r Result) {
	r = Result{Key: key, ScannedAt: time.Now().UTC()}
	start := time.Now()
	defer func() { r.DurationMS = time.Since(start).Milliseconds() }()

	body, head, err := getObject(ctx, c, bucket, key)
	if err != nil {
		r.Error = "get: " + err.Error()
		return r
	}
	r.Bytes = len(body)
	r.Filename = head.Metadata["filename"]
	if r.Filename == "" {
		r.Filename = filepath.Base(key)
	}
	r.ContentType = aws.ToString(head.ContentType)

	v, err := insp.Inspect(ctx, intake.Input{
		Filename:    r.Filename,
		ContentType: r.ContentType,
		Body:        body,
	})
	if err != nil {
		r.Error = "inspect: " + err.Error()
		return r
	}
	r.Allowed = v.Allowed
	r.Reason = v.Reason
	r.Categories = v.Categories

	if !v.Allowed && !dryRun {
		if err := setFlag(ctx, c, bucket, key, head, v); err != nil {
			r.Error = "write metadata: " + err.Error()
			return r
		}
		r.Wrote = true
	}
	return r
}

// printProgress writes a single human-friendly line per object as
// results land. Order is non-deterministic with concurrency, so we
// number by completion not list-position.
func printProgress(done, total int, r Result, verbose bool) {
	switch {
	case r.Error != "":
		fmt.Printf("[%d/%d] %s  ERROR  %s\n", done, total, r.Key, r.Error)
	case !r.Allowed:
		cats := strings.Join(r.Categories, ",")
		flag := "FLAGGED"
		if r.Wrote {
			flag = "FLAGGED+marked"
		}
		fmt.Printf("[%d/%d] %s  %s  [%s]  %s\n", done, total, r.Key, flag, cats, truncate(r.Reason, 120))
	case verbose:
		fmt.Printf("[%d/%d] %s  ok\n", done, total, r.Key)
	default:
		// allowed objects are silent unless --v
	}
}

func printSummary(results []Result) {
	var allowed, flagged, wrote, errored int
	for _, r := range results {
		switch {
		case r.Error != "":
			errored++
		case !r.Allowed:
			flagged++
			if r.Wrote {
				wrote++
			}
		default:
			allowed++
		}
	}
	fmt.Printf("\nsummary: total=%d allowed=%d flagged=%d marked=%d errored=%d\n",
		len(results), allowed, flagged, wrote, errored)
}

// getObject reads the body and metadata in one HEAD + GET pair.
func getObject(ctx context.Context, c *s3.Client, bucket, key string) ([]byte, *s3.HeadObjectOutput, error) {
	head, err := c.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, nil, fmt.Errorf("head: %w", err)
	}
	get, err := c.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, nil, fmt.Errorf("get: %w", err)
	}
	defer get.Body.Close()
	body, err := io.ReadAll(get.Body)
	if err != nil {
		return nil, nil, fmt.Errorf("read body: %w", err)
	}
	return body, head, nil
}

// setFlag writes intake-flagged + intake-flag-reason via CopyObject
// self-copy with MetadataDirective: REPLACE. Preserves all other user
// metadata.
func setFlag(ctx context.Context, c *s3.Client, bucket, key string, head *s3.HeadObjectOutput, v *intake.Verdict) error {
	merged := make(map[string]string, len(head.Metadata)+3)
	for k, val := range head.Metadata {
		if k == mdFlagged || k == mdReason || k == mdCategories {
			continue
		}
		merged[k] = val
	}
	merged[mdFlagged] = "true"
	if v.Reason != "" {
		merged[mdReason] = v.Reason
	}
	if len(v.Categories) > 0 {
		merged[mdCategories] = strings.Join(v.Categories, ",")
	}
	_, err := c.CopyObject(ctx, &s3.CopyObjectInput{
		Bucket:            aws.String(bucket),
		Key:               aws.String(key),
		CopySource:        aws.String(bucket + "/" + key),
		ContentType:       head.ContentType,
		Metadata:          merged,
		MetadataDirective: types.MetadataDirectiveReplace,
	})
	return err
}

func newS3Client(endpoint, region string) (*s3.Client, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var loadOpts []func(*awsconfig.LoadOptions) error
	if region != "" {
		loadOpts = append(loadOpts, awsconfig.WithRegion(region))
	}
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, loadOpts...)
	if err != nil {
		return nil, fmt.Errorf("loading AWS config: %w", err)
	}
	var s3Opts []func(*s3.Options)
	if endpoint != "" {
		s3Opts = append(s3Opts, func(o *s3.Options) {
			o.BaseEndpoint = aws.String(endpoint)
			o.UsePathStyle = true
		})
	}
	return s3.NewFromConfig(awsCfg, s3Opts...), nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
