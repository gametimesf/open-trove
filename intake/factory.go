package intake

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/gametimesf/open-trove/internal/config"
)

// BuildInspector is the shared constructor used by every binary that
// needs the same upload-intake gate the server runs: cmd/server, the
// offline scanner, and any future tooling. When intake is disabled it
// returns NoOp{} + FailClosed so callers don't need to special-case
// nil inspectors.
//
// Side effects: in production-like environments without intake.enabled
// set, logs a warning. Reads ANTHROPIC_API_KEY from env if the YAML
// doesn't set api_key.
func BuildInspector(cfg *config.Config) (Inspector, FailMode, error) {
	if !cfg.Intake.Enabled {
		if env := os.Getenv("ENVIRONMENT"); env == "production" {
			log.Printf("WARN  intake gate is disabled in production — uploads not inspected")
		}
		return NoOp{}, FailClosed, nil
	}

	apiKey := cfg.Intake.APIKey
	if apiKey == "" {
		apiKey = os.Getenv("ANTHROPIC_API_KEY")
	}

	model := cfg.Intake.Model
	if model == "" {
		model = "claude-sonnet-4-6"
	}

	timeoutMS := cfg.Intake.TimeoutMS
	if timeoutMS <= 0 {
		timeoutMS = 15000
	}

	source, staticGuidance, err := BuildPromptSource(cfg)
	if err != nil {
		return nil, "", fmt.Errorf("intake: prompt source: %w", err)
	}

	insp, err := NewAnthropic(AnthropicConfig{
		APIKey:       apiKey,
		Model:        model,
		Endpoint:     cfg.Intake.Endpoint,
		Guidance:     staticGuidance,
		PromptSource: source,
		MaxBytes:     cfg.Intake.MaxBytes,
		Timeout:      time.Duration(timeoutMS) * time.Millisecond,
	})
	if err != nil {
		return nil, "", fmt.Errorf("intake: building inspector: %w", err)
	}

	failMode := FailClosed
	if cfg.Intake.FailMode == string(FailOpen) {
		failMode = FailOpen
	}

	log.Printf("INFO  upload intake enabled (model=%s, fail=%s)", model, failMode)
	return insp, failMode, nil
}

// BuildPromptSource decides where the inspector reads its guidance from
// at request time. Three layers, tried in order:
//  1. PromptInline / PromptPath in YAML — convenient for local dev.
//  2. S3-backed source — production path. Confidentiality is provided
//     by SSE-KMS on the uploads bucket plus IAM scoping; the object
//     itself is plaintext to the task role.
//  3. Nothing — configuration is rejected because review policy is
//     deployment-owned and must be explicit.
//
// Returns (source, staticGuidance, error). When source is non-nil it
// overrides staticGuidance; staticGuidance is the inline/path text
// otherwise.
func BuildPromptSource(cfg *config.Config) (PromptSource, string, error) {
	if cfg.Intake.PromptInline != "" || cfg.Intake.PromptPath != "" {
		text := Guidance(cfg.Intake.PromptInline, cfg.Intake.PromptPath, os.ReadFile)
		if text == "" {
			return nil, "", fmt.Errorf("configured prompt guidance is empty or unreadable")
		}
		return nil, text, nil
	}

	bucket := cfg.Intake.PromptSourceBucket
	key := cfg.Intake.PromptSourceKey
	if bucket == "" || key == "" {
		return nil, "", fmt.Errorf("prompt_inline, prompt_path, or prompt_source_bucket/key is required")
	}

	s3client, err := newPromptS3Client(cfg.Store.S3.Endpoint, cfg.Store.S3.Region)
	if err != nil {
		return nil, "", fmt.Errorf("building S3 client for prompt source: %w", err)
	}

	refresh := 10 * time.Minute
	if cfg.Intake.PromptRefresh != "" {
		if d, err := time.ParseDuration(cfg.Intake.PromptRefresh); err == nil {
			refresh = d
		}
	}

	source := &CachedSource{
		TTL: refresh,
		Inner: &S3Source{
			Client: s3client,
			Bucket: bucket,
			Key:    key,
		},
	}

	// Prefetch once at boot so we fail fast on misconfiguration.
	bootCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := source.Fetch(bootCtx); err != nil {
		return nil, "", fmt.Errorf("prompt source unreachable: %w", err)
	}
	log.Printf("INFO  intake: prompt source loaded (bucket=%s, key=%s, refresh=%s)", bucket, key, refresh)
	return source, "", nil
}

// promptS3Reader adapts the AWS SDK v2 S3 client to S3Reader.
type promptS3Reader struct {
	client *s3.Client
}

// GetObject implements S3Reader.
func (p *promptS3Reader) GetObject(ctx context.Context, bucket, key string) (io.ReadCloser, error) {
	out, err := p.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, fmt.Errorf("s3 GetObject: %w", err)
	}
	return out.Body, nil
}

func newPromptS3Client(endpoint, region string) (*promptS3Reader, error) {
	ctx := context.Background()
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
	return &promptS3Reader{client: s3.NewFromConfig(awsCfg, s3Opts...)}, nil
}
