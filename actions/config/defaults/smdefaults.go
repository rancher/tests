package defaults

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	secrettypes "github.com/aws/aws-sdk-go-v2/service/secretsmanager/types"
	"github.com/rancher/shepherd/extensions/cloudcredentials"
	"github.com/rancher/shepherd/extensions/defaults/providers"
	"github.com/rancher/shepherd/pkg/config/operations"
	"github.com/sirupsen/logrus"
)

var awsSecretPlaceholderRegex = regexp.MustCompile(`<([^<>]+)>`)

func LoadSecretsManagerDefaults(cattleConfig map[string]any) (map[string]any, error) {
	credentialSpec := cloudcredentials.LoadCloudCredential(providers.AWS)
	if credentialSpec.AmazonEC2CredentialConfig == nil ||
		credentialSpec.AmazonEC2CredentialConfig.AccessKey == "" ||
		credentialSpec.AmazonEC2CredentialConfig.SecretKey == "" ||
		credentialSpec.AmazonEC2CredentialConfig.DefaultRegion == "" {
		logrus.Warning("Unable to load Secrets Manager defaults: AWS credentials are incomplete in cattle config")
		return cattleConfig, nil
	}

	awsCredentials := *credentialSpec.AmazonEC2CredentialConfig

	output, err := operations.DeepCopyMap(cattleConfig)
	if err != nil {
		return nil, err
	}

	ctx := context.Background()
	creds := credentials.NewStaticCredentialsProvider(
		awsCredentials.AccessKey,
		awsCredentials.SecretKey,
		"",
	)

	awsCfg, err := awsconfig.LoadDefaultConfig(
		ctx,
		awsconfig.WithRegion(awsCredentials.DefaultRegion),
		awsconfig.WithCredentialsProvider(creds),
	)
	if err != nil {
		return nil, err
	}

	secretsClient := secretsmanager.NewFromConfig(awsCfg)
	secretCache := map[string]any{}

	resolvedValue, err := resolveAWSSecretsInValue(ctx, secretsClient, output, secretCache)
	if err != nil {
		return nil, err
	}

	resolvedConfig, ok := resolvedValue.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("resolved cattle config is not a map")
	}

	return resolvedConfig, nil
}

func resolveAWSSecretsInValue(ctx context.Context, client *secretsmanager.Client, value any, cache map[string]any) (any, error) {
	switch typedValue := value.(type) {
	case map[string]any:
		for key, nestedValue := range typedValue {
			resolvedNestedValue, err := resolveAWSSecretsInValue(ctx, client, nestedValue, cache)
			if err != nil {
				return nil, err
			}

			typedValue[key] = resolvedNestedValue
		}

		return typedValue, nil
	case []any:
		for i, listValue := range typedValue {
			resolvedListValue, err := resolveAWSSecretsInValue(ctx, client, listValue, cache)
			if err != nil {
				return nil, err
			}

			typedValue[i] = resolvedListValue
		}

		return typedValue, nil
	case string:
		return resolveSecretPlaceholdersInString(ctx, client, typedValue, cache)
	default:
		return value, nil
	}
}

func resolveSecretPlaceholdersInString(ctx context.Context, client *secretsmanager.Client, value string, cache map[string]any) (any, error) {
	matches := awsSecretPlaceholderRegex.FindAllStringSubmatch(value, -1)
	if len(matches) == 0 {
		return value, nil
	}

	// If the entire string is exactly one placeholder, return the typed value directly to preserve its type.
	if len(matches) == 1 && value == matches[0][0] {
		secretName := matches[0][1]
		secretValue, exists, err := getSecretValueByName(ctx, client, secretName, cache)
		if err != nil {
			return nil, err
		}
		if !exists {
			return value, nil
		}
		return secretValue, nil
	}

	// Multiple or embedded placeholders: perform string substitution.
	resolvedValue := value
	for _, match := range matches {
		if len(match) < 2 {
			continue
		}

		secretName := match[1]
		placeholder := match[0]

		secretValue, exists, err := getSecretValueByName(ctx, client, secretName, cache)
		if err != nil {
			return "", err
		}

		if !exists {
			continue
		}

		resolvedValue = strings.ReplaceAll(resolvedValue, placeholder, fmt.Sprintf("%v", secretValue))
	}

	return resolvedValue, nil
}

func getSecretValueByName(ctx context.Context, client *secretsmanager.Client, secretName string, cache map[string]any) (any, bool, error) {
	if cachedValue, ok := cache[secretName]; ok {
		return cachedValue, true, nil
	}

	output, err := client.GetSecretValue(ctx, &secretsmanager.GetSecretValueInput{SecretId: aws.String(secretName)})
	if err != nil {
		var notFoundErr *secrettypes.ResourceNotFoundException
		if errors.As(err, &notFoundErr) {
			return nil, false, nil
		}

		return nil, false, err
	}

	rawSecret := ""
	if output.SecretString != nil {
		rawSecret = *output.SecretString
	} else if output.SecretBinary != nil {
		rawSecret = string(output.SecretBinary)
	}

	// Attempt to parse as JSON and extract the "value" key to preserve the secret's type.
	var jsonValue map[string]any
	if err := json.Unmarshal([]byte(rawSecret), &jsonValue); err == nil {
		if v, ok := jsonValue["value"]; ok {
			cache[secretName] = v
			return v, true, nil
		}
	}

	// Not JSON or no "value" key — use plaintext as-is.
	cache[secretName] = rawSecret
	return rawSecret, true, nil
}
