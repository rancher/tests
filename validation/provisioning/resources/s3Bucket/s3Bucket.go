package s3

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/sirupsen/logrus"
)

// CreateBucket creates an S3 bucket in the given region.
func CreateBucket(bucketName, region string) error {
	logrus.Infof("Creating S3 bucket: %s in region: %s", bucketName, region)

	cfg, err := config.LoadDefaultConfig(context.TODO(),
		config.WithRegion(region),
	)
	if err != nil {
		return fmt.Errorf("failed to load AWS config: %w", err)
	}

	client := awss3.NewFromConfig(cfg)

	_, err = client.CreateBucket(context.TODO(), &awss3.CreateBucketInput{
		Bucket: aws.String(bucketName),
	})
	if err != nil {
		return fmt.Errorf("failed to create bucket %s: %w", bucketName, err)
	}

	logrus.Infof("Successfully created S3 bucket: %s", bucketName)
	return nil
}

// DeleteBucket deletes all objects in a bucket and then deletes the bucket itself.
func DeleteBucket(bucketName, region string) error {
	logrus.Infof("Deleting S3 bucket: %s", bucketName)

	cfg, err := config.LoadDefaultConfig(context.TODO(),
		config.WithRegion(region),
	)
	if err != nil {
		return fmt.Errorf("failed to load AWS config: %w", err)
	}

	client := awss3.NewFromConfig(cfg)

	// Step 1: List all objects in the bucket
	listResp, err := client.ListObjectsV2(context.TODO(), &awss3.ListObjectsV2Input{
		Bucket: aws.String(bucketName),
	})
	if err != nil {
		return fmt.Errorf("failed to list objects in bucket %s: %w", bucketName, err)
	}

	// Step 2: Delete all objects
	for _, obj := range listResp.Contents {
		logrus.Debugf("Deleting object: %s", *obj.Key)

		_, err := client.DeleteObject(context.TODO(), &awss3.DeleteObjectInput{
			Bucket: aws.String(bucketName),
			Key:    obj.Key,
		})
		if err != nil {
			return fmt.Errorf("failed to delete object %s: %w", *obj.Key, err)
		}
	}

	// Step 3: Delete the bucket
	_, err = client.DeleteBucket(context.TODO(), &awss3.DeleteBucketInput{
		Bucket: aws.String(bucketName),
	})
	if err != nil {
		return fmt.Errorf("failed to delete bucket %s: %w", bucketName, err)
	}

	logrus.Infof("Successfully deleted S3 bucket: %s", bucketName)
	return nil
}