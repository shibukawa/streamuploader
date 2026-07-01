package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/feature/s3/manager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

type S3Config struct {
	Bucket         string
	Endpoint       string
	Region         string
	AccessKey      string
	SecretKey      string
	ForcePathStyle bool
	PublicEndpoint string
	PublicRead     bool
}

type S3Store struct {
	uploader *manager.Uploader
	client   *s3.Client
	presign  *s3.PresignClient
}

func NewS3Store(ctx context.Context, cfg S3Config) (*S3Store, error) {
	awsCfg, err := config.LoadDefaultConfig(
		ctx,
		config.WithRegion(cfg.Region),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(cfg.AccessKey, cfg.SecretKey, "")),
	)
	if err != nil {
		return nil, err
	}
	client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		o.UsePathStyle = cfg.ForcePathStyle
		if cfg.Endpoint != "" {
			o.BaseEndpoint = aws.String(cfg.Endpoint)
		}
	})
	presignClient := s3.NewPresignClient(s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		o.UsePathStyle = cfg.ForcePathStyle
		if cfg.PublicEndpoint != "" {
			o.BaseEndpoint = aws.String(cfg.PublicEndpoint)
		} else if cfg.Endpoint != "" {
			o.BaseEndpoint = aws.String(cfg.Endpoint)
		}
	}))
	if cfg.Bucket != "" {
		if _, err := client.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String(cfg.Bucket)}); err != nil {
			if _, createErr := client.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(cfg.Bucket)}); createErr != nil {
				return nil, createErr
			}
		}
		if cfg.PublicRead {
			policy := publicReadPolicy(cfg.Bucket)
			if _, err := client.PutBucketPolicy(ctx, &s3.PutBucketPolicyInput{
				Bucket: aws.String(cfg.Bucket),
				Policy: aws.String(policy),
			}); err != nil {
				return nil, fmt.Errorf("set public bucket policy: %w", err)
			}
		}
	}
	return &S3Store{
		client:  client,
		presign: presignClient,
		uploader: manager.NewUploader(client, func(u *manager.Uploader) {
			u.PartSize = 8 * 1024 * 1024
			u.Concurrency = 2
		}),
	}, nil
}

func (s *S3Store) PutObject(ctx context.Context, input PutInput) (PutResult, error) {
	out, err := s.uploader.Upload(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(input.Bucket),
		Key:         aws.String(input.Key),
		Body:        input.Body,
		ContentType: aws.String(input.ContentType),
		Metadata:    input.Metadata,
	})
	if err != nil {
		return PutResult{}, err
	}
	return PutResult{ETag: aws.ToString(out.ETag)}, nil
}

func (s *S3Store) CopyObject(ctx context.Context, input CopyInput) (CopyResult, error) {
	out, err := s.client.CopyObject(ctx, &s3.CopyObjectInput{
		Bucket:            aws.String(input.Bucket),
		Key:               aws.String(input.Key),
		CopySource:        aws.String(url.PathEscape(input.Bucket + "/" + input.SourceKey)),
		ContentType:       aws.String(input.ContentType),
		Metadata:          input.Metadata,
		MetadataDirective: types.MetadataDirectiveReplace,
	})
	if err != nil {
		return CopyResult{}, err
	}
	etag := ""
	if out.CopyObjectResult != nil {
		etag = aws.ToString(out.CopyObjectResult.ETag)
	}
	return CopyResult{ETag: etag}, nil
}

func (s *S3Store) GetObject(ctx context.Context, input GetInput) (GetResult, error) {
	req := &s3.GetObjectInput{
		Bucket: aws.String(input.Bucket),
		Key:    aws.String(input.Key),
	}
	if input.Range != "" {
		req.Range = aws.String(input.Range)
	}
	out, err := s.client.GetObject(ctx, req)
	if err != nil {
		return GetResult{}, err
	}
	return GetResult{
		Body:          out.Body,
		ContentType:   aws.ToString(out.ContentType),
		ContentLength: aws.ToInt64(out.ContentLength),
		ContentRange:  aws.ToString(out.ContentRange),
		ETag:          aws.ToString(out.ETag),
		LastModified:  aws.ToTime(out.LastModified),
		Metadata:      out.Metadata,
	}, nil
}

func (s *S3Store) HeadObject(ctx context.Context, input HeadInput) (HeadResult, error) {
	out, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(input.Bucket),
		Key:    aws.String(input.Key),
	})
	if err != nil {
		return HeadResult{}, err
	}
	return HeadResult{
		ContentType:   aws.ToString(out.ContentType),
		ContentLength: aws.ToInt64(out.ContentLength),
		ETag:          aws.ToString(out.ETag),
		LastModified:  aws.ToTime(out.LastModified),
		Metadata:      out.Metadata,
	}, nil
}

func (s *S3Store) ListObjects(ctx context.Context, input ListInput) (ListResult, error) {
	var keys []string
	p := s3.NewListObjectsV2Paginator(s.client, &s3.ListObjectsV2Input{
		Bucket: aws.String(input.Bucket),
		Prefix: aws.String(input.Prefix),
	})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			return ListResult{}, err
		}
		for _, object := range page.Contents {
			keys = append(keys, aws.ToString(object.Key))
		}
	}
	return ListResult{Keys: keys}, nil
}

func (s *S3Store) PresignGetObject(ctx context.Context, input PresignGetInput) (PresignGetResult, error) {
	if input.Expires <= 0 {
		input.Expires = 15 * time.Minute
	}
	out, err := s.presign.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket:                     aws.String(input.Bucket),
		Key:                        aws.String(input.Key),
		ResponseContentDisposition: aws.String(input.ResponseContentDisposition),
	}, s3.WithPresignExpires(input.Expires))
	if err != nil {
		return PresignGetResult{}, err
	}
	return PresignGetResult{URL: out.URL, ExpiresAt: time.Now().UTC().Add(input.Expires)}, nil
}

func (s *S3Store) DeleteObject(ctx context.Context, input DeleteInput) error {
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(input.Bucket),
		Key:    aws.String(input.Key),
	})
	return err
}

func publicReadPolicy(bucket string) string {
	body, _ := json.Marshal(map[string]any{
		"Version": "2012-10-17",
		"Statement": []map[string]any{
			{
				"Effect":    "Allow",
				"Principal": map[string]string{"AWS": "*"},
				"Action":    []string{"s3:GetObject"},
				"Resource":  []string{"arn:aws:s3:::" + bucket + "/*"},
			},
		},
	})
	return string(body)
}
