package agent

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/buildkite/agent/v3/api"
	"github.com/buildkite/agent/v3/logger"
	"github.com/buildkite/agent/v3/pool"
)

type ArtifactDownloaderConfig struct {
	// The ID of the Build
	BuildID string

	// The query used to find the artifacts
	Query string

	// Which step should we look at for the jobs
	Step string

	// Whether to include artifacts from retried jobs in the search
	IncludeRetriedJobs bool

	// Where we'll be downloading artifacts to
	Destination string

	// Whether to show HTTP debugging
	DebugHTTP bool
}

type ArtifactDownloader struct {
	// The config for downloading
	conf ArtifactDownloaderConfig

	// The logger instance to use
	logger logger.Logger

	// The APIClient that will be used when uploading jobs
	apiClient APIClient
}

func NewArtifactDownloader(l logger.Logger, ac APIClient, c ArtifactDownloaderConfig) ArtifactDownloader {
	return ArtifactDownloader{
		logger:    l,
		apiClient: ac,
		conf:      c,
	}
}

func (a *ArtifactDownloader) Download(ctx context.Context) error {
	// Turn the download destination into an absolute path and confirm it exists
	downloadDestination, _ := filepath.Abs(a.conf.Destination)
	fileInfo, err := os.Stat(downloadDestination)
	if err != nil {
		return fmt.Errorf("Could not find information about destination: %s %v",
			downloadDestination, err)
	}
	if !fileInfo.IsDir() {
		return fmt.Errorf("%s is not a directory", downloadDestination)
	}

	artifacts, err := NewArtifactSearcher(a.logger, a.apiClient, a.conf.BuildID).
		Search(ctx, a.conf.Query, a.conf.Step, a.conf.IncludeRetriedJobs, false)
	if err != nil {
		return err
	}

	artifactCount := len(artifacts)

	if artifactCount == 0 {
		return errors.New("No artifacts found for downloading")
	}

	a.logger.Info("Found %d artifacts. Starting to download to: %s", artifactCount, downloadDestination)

	p := pool.New(pool.MaxConcurrencyLimit)
	errors := []error{}
	s3Clients, err := a.generateS3Clients(artifacts)
	if err != nil {
		return fmt.Errorf("failed to generate S3 clients for artifact upload: %w", err)
	}

	for _, artifact := range artifacts {
		// Create new instance of the artifact for the goroutine
		// See: http://golang.org/doc/effective_go.html#channels
		artifact := artifact

		p.Spawn(func() {
			// Convert windows paths to slashes, otherwise we get a literal
			// download of "dir/dir/file" vs sub-directories on non-windows agents
			path := artifact.Path
			if runtime.GOOS != "windows" {
				path = strings.Replace(path, `\`, `/`, -1)
			}

			// Handle downloading from S3, GS, or RT
			var dler interface {
				Start(context.Context) error
			}
			switch {
			case strings.HasPrefix(artifact.UploadDestination, "s3://"):
				bucketName, _ := ParseS3Destination(artifact.UploadDestination)
				dler = NewS3Downloader(a.logger, S3DownloaderConfig{
					S3Client:    s3Clients[bucketName],
					Path:        path,
					S3Path:      artifact.UploadDestination,
					Destination: downloadDestination,
					Retries:     5,
					DebugHTTP:   a.conf.DebugHTTP,
				})
			case strings.HasPrefix(artifact.UploadDestination, "gs://"):
				dler = NewGSDownloader(a.logger, GSDownloaderConfig{
					Path:        path,
					Bucket:      artifact.UploadDestination,
					Destination: downloadDestination,
					Retries:     5,
					DebugHTTP:   a.conf.DebugHTTP,
				})
			case strings.HasPrefix(artifact.UploadDestination, "rt://"):
				dler = NewArtifactoryDownloader(a.logger, ArtifactoryDownloaderConfig{
					Path:        path,
					Repository:  artifact.UploadDestination,
					Destination: downloadDestination,
					Retries:     5,
					DebugHTTP:   a.conf.DebugHTTP,
				})
			default:
				dler = NewDownload(a.logger, http.DefaultClient, DownloadConfig{
					URL:         artifact.URL,
					Path:        path,
					Destination: downloadDestination,
					Retries:     5,
					DebugHTTP:   a.conf.DebugHTTP,
				})
			}

			// If the downloaded encountered an error, lock
			// the pool, collect it, then unlock the pool
			// again.
			if err := dler.Start(ctx); err != nil {
				a.logger.Error("Failed to download artifact: %s", err)

				p.Lock()
				errors = append(errors, err)
				p.Unlock()
			}
		})
	}

	p.Wait()

	if len(errors) > 0 {
		return fmt.Errorf("There were errors with downloading some of the artifacts")
	}

	return nil
}

// We want to have as few S3 clients as possible, as creating them is kind of an expensive operation
// But it's also theoretically possible that we'll have multiple artifacts with different S3 buckets, and each
// S3Client only applies to one bucket, so we need to store the S3 clients in a map, one for each bucket
func (a *ArtifactDownloader) generateS3Clients(artifacts []*api.Artifact) (map[string]*s3.S3, error) {
	s3Clients := map[string]*s3.S3{}

	for _, artifact := range artifacts {
		if !strings.HasPrefix(artifact.UploadDestination, "s3://") {
			continue
		}

		bucketName, _ := ParseS3Destination(artifact.UploadDestination)
		if _, has := s3Clients[bucketName]; !has {
			client, err := NewS3Client(a.logger, bucketName)
			if err != nil {
				return nil, fmt.Errorf("failed to create S3 client for bucket %s: %w", bucketName, err)
			}

			s3Clients[bucketName] = client
		}
	}

	return s3Clients, nil
}
