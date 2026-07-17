package etcdsnapshot

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	steveV1 "github.com/rancher/shepherd/clients/rancher/v1"
)

// awsS3Config builds AWS config using static credentials and region
func awsS3Config(ctx context.Context, region, accessKey, secretKey string) (aws.Config, error) {
	creds := credentials.NewStaticCredentialsProvider(accessKey, secretKey, "")

	return awsconfig.LoadDefaultConfig(
		ctx,
		awsconfig.WithRegion(region),
		awsconfig.WithCredentialsProvider(creds),
	)
}

// CreateS3Bucket creates an S3 bucket and waits until it exists
func CreateS3Bucket(bucketName, region, accessKey, secretKey string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	cfg, err := awsS3Config(ctx, region, accessKey, secretKey)
	if err != nil {
		return err
	}

	client := s3.NewFromConfig(cfg)

	input := &s3.CreateBucketInput{
		Bucket: &bucketName,
		CreateBucketConfiguration: &s3types.CreateBucketConfiguration{
			LocationConstraint: s3types.BucketLocationConstraint(region),
		},
	}

	_, err = client.CreateBucket(ctx, input)
	if err != nil {
		return err
	}

	waiter := s3.NewBucketExistsWaiter(client)
	return waiter.Wait(ctx, &s3.HeadBucketInput{Bucket: &bucketName}, 2*time.Minute)
}

// DeleteS3Bucket deletes all objects in the bucket and then deletes the bucket
func DeleteS3Bucket(bucketName, region, accessKey, secretKey string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	cfg, err := awsS3Config(ctx, region, accessKey, secretKey)
	if err != nil {
		return err
	}

	client := s3.NewFromConfig(cfg)

	paginator := s3.NewListObjectsV2Paginator(client, &s3.ListObjectsV2Input{
		Bucket: &bucketName,
	})

	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return err
		}

		if len(page.Contents) == 0 {
			continue
		}

		objects := make([]s3types.ObjectIdentifier, 0, len(page.Contents))
		for _, obj := range page.Contents {
			objects = append(objects, s3types.ObjectIdentifier{Key: obj.Key})
		}

		quiet := true
		out, err := client.DeleteObjects(ctx, &s3.DeleteObjectsInput{
			Bucket: &bucketName,
			Delete: &s3types.Delete{
				Objects: objects,
				Quiet:   &quiet,
			},
		})
		if err != nil {
			return err
		}

		if len(out.Errors) > 0 {
			return fmt.Errorf("failed to delete one or more S3 objects from bucket %s", bucketName)
		}
	}

	_, err = client.DeleteBucket(ctx, &s3.DeleteBucketInput{
		Bucket: &bucketName,
	})
	if err != nil {
		return err
	}

	waiter := s3.NewBucketNotExistsWaiter(client)
	return waiter.Wait(ctx, &s3.HeadBucketInput{Bucket: &bucketName}, 2*time.Minute)
}

// CheckS3SnapshotLocation is a helper function that checks if a snapshot is stored in S3.
func CheckS3SnapshotLocation(snapshot steveV1.SteveAPIObject) bool {
	if snapshotFile, ok := nestedMap(snapshot.JSONResp, "snapshotFile"); ok {
		if location, ok := nestedString(snapshotFile, "location"); ok {
			return isS3Location(location)
		}

		if _, hasS3Config := snapshotFile[s3StorageType]; hasS3Config {
			return true
		}
	}

	if spec, ok := nestedMap(snapshot.JSONResp, "spec"); ok {
		if location, ok := nestedString(spec, "location"); ok {
			return isS3Location(location)
		}

		if _, hasS3Config := spec[s3StorageType]; hasS3Config {
			return true
		}
	}

	store, ok := snapshot.Annotations[storageAnnotation]
	if ok && strings.EqualFold(strings.TrimSpace(store), s3StorageType) {
		return hasS3Token(snapshot.ID) || hasS3Token(snapshot.Name)
	}

	return false
}

func nestedMap(source map[string]any, key string) (map[string]any, bool) {
	if source == nil {
		return nil, false
	}

	v, ok := source[key]
	if !ok || v == nil {
		return nil, false
	}

	obj, ok := v.(map[string]any)
	if !ok {
		return nil, false
	}

	return obj, true
}

func nestedString(source map[string]any, key string) (string, bool) {
	if source == nil {
		return "", false
	}

	v, ok := source[key]
	if !ok || v == nil {
		return "", false
	}

	value, ok := v.(string)
	if !ok {
		return "", false
	}

	return value, true
}

func hasS3Token(value string) bool {
	trimmed := strings.TrimSpace(strings.ToLower(value))
	if trimmed == "" {
		return false
	}

	return strings.Contains(trimmed, "-"+s3StorageType+"-") || strings.HasPrefix(trimmed, s3StorageType+"-")
}

func isS3Location(location string) bool {
	trimmed := strings.TrimSpace(strings.ToLower(location))
	if trimmed == "" {
		return false
	}

	return strings.HasPrefix(trimmed, s3SchemePrefix)
}
