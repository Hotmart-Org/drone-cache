package s3

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/feature/s3/manager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/go-kit/kit/log"
	"github.com/go-kit/kit/log/level"

	"github.com/meltwater/drone-cache/internal"
)

// Backend implements storage.Backend for AWs S3.
type Backend struct {
	logger     log.Logger
	bucket     string
	acl        string
	encryption string
	client     *s3.Client
}

// New creates an S3 backend.
func New(l log.Logger, c Config, debug bool) (*Backend, error) {

	var cfg aws.Config
	var err error

	if c.Profile != "" {
		cfg, err = config.LoadDefaultConfig(context.TODO(),
			config.WithRegion(c.Region), 
			config.WithSharedConfigProfile(c.Profile),
		)
	} else {
		cfg, err = config.LoadDefaultConfig(context.TODO(),
			config.WithRegion(c.Region), 
		)
	}

	if err != nil {
		level.Error(l).Log("err", err)
	}

	client := s3.NewFromConfig(cfg)

	return &Backend{
		logger:     l,
		bucket:     c.Bucket,
		acl:        c.ACL,
		encryption: c.Encryption,
		client:     client,
	}, nil
}

// Get writes downloaded content to the given writer.
func (b *Backend) Get(ctx context.Context, p string, w io.Writer) error {
	in := &s3.GetObjectInput{
		Bucket: aws.String(b.bucket),
		Key:    aws.String(p),
	}

	errCh := make(chan error)

	go func() {
		defer close(errCh)

		out, err := b.client.GetObject(ctx, in)
		if err != nil {
			errCh <- fmt.Errorf("get the object, %w", err)
			return
		}

		defer internal.CloseWithErrLogf(b.logger, out.Body, "response body, close defer")

		_, err = io.Copy(w, out.Body)
		if err != nil {
			errCh <- fmt.Errorf("copy the object, %w", err)
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Put uploads contents of the given reader.
func (b *Backend) Put(ctx context.Context, p string, r io.Reader) error {
	in := &s3.PutObjectInput{
		Bucket: aws.String(b.bucket),
		Key:    aws.String(p),
		ACL:    types.ObjectCannedACL(b.acl),
		Body:   r,
	}

	uploader := manager.NewUploader(b.client)

	if b.encryption != "" {
		in.ServerSideEncryption = types.ServerSideEncryption(b.encryption)
	}

	if _, err := uploader.Upload(ctx, in); err != nil {
		return fmt.Errorf("put the object, %w", err)
	}

	return nil
}

// Exists checks if object already exists.
func (b *Backend) Exists(ctx context.Context, p string) (bool, error) {
	in := &s3.HeadObjectInput{
		Bucket: aws.String(b.bucket),
		Key:    aws.String(p),
	}

	out, err := b.client.HeadObject(ctx, in)
	if err != nil {
		var nsk *types.NoSuchKey
		if errors.As(err, &nsk) {
			return false, nil
		}

		return false, fmt.Errorf("head the object, %w", err)
	}

	// Normally if file not exists it will be already detected by error above but in some cases
	// Minio can return success status for without ETag, detect that here.
	return *out.ETag != "", nil
}
