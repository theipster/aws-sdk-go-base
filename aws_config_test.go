package awsbase

import (
	"context"
	"errors"
	"fmt"
	"io/ioutil"
	"net"
	"os"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/retry"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/aws/smithy-go/middleware"
	smithyhttp "github.com/aws/smithy-go/transport/http"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/hashicorp/aws-sdk-go-base/v2/internal/constants"
	"github.com/hashicorp/aws-sdk-go-base/v2/mockdata"
	"github.com/hashicorp/aws-sdk-go-base/v2/servicemocks"
)

const (
	// Shockingly, this is not defined in the SDK
	sharedConfigCredentialsProvider = "SharedConfigCredentials"
)

func TestGetAwsConfig(t *testing.T) {
	testCases := []struct {
		Config                     *Config
		Description                string
		EnableEc2MetadataServer    bool
		EnableEcsCredentialsServer bool
		EnableWebIdentityToken     bool
		EnvironmentVariables       map[string]string
		ExpectedCredentialsValue   aws.Credentials
		ExpectedRegion             string
		ExpectedUserAgent          string
		ExpectedError              func(err error) bool
		MockStsEndpoints           []*servicemocks.MockEndpoint
		SharedConfigurationFile    string
		SharedCredentialsFile      string
	}{
		{
			Config:      &Config{},
			Description: "no configuration or credentials",
			ExpectedError: func(err error) bool {
				return IsNoValidCredentialSourcesError(err)
			},
		},
		{
			Config: &Config{
				AccessKey: servicemocks.MockStaticAccessKey,
				Region:    "us-east-1",
				SecretKey: servicemocks.MockStaticSecretKey,
			},
			Description:              "config AccessKey",
			ExpectedCredentialsValue: mockdata.MockStaticCredentials,
			ExpectedRegion:           "us-east-1",
			MockStsEndpoints: []*servicemocks.MockEndpoint{
				servicemocks.MockStsGetCallerIdentityValidEndpoint,
			},
		},
		{
			Config: &Config{
				AccessKey: servicemocks.MockStaticAccessKey,
				AssumeRole: &AssumeRole{
					RoleARN:     servicemocks.MockStsAssumeRoleArn,
					SessionName: servicemocks.MockStsAssumeRoleSessionName,
				},
				Region:    "us-east-1",
				SecretKey: servicemocks.MockStaticSecretKey,
			},
			Description:              "config AccessKey config AssumeRoleARN access key",
			ExpectedCredentialsValue: mockdata.MockStsAssumeRoleCredentials,
			ExpectedRegion:           "us-east-1",
			MockStsEndpoints: []*servicemocks.MockEndpoint{
				servicemocks.MockStsAssumeRoleValidEndpoint,
				servicemocks.MockStsGetCallerIdentityValidEndpoint,
			},
		},
		{
			Config: &Config{
				AccessKey: servicemocks.MockStaticAccessKey,
				AssumeRole: &AssumeRole{
					RoleARN:         servicemocks.MockStsAssumeRoleArn,
					DurationSeconds: 3600,
					SessionName:     servicemocks.MockStsAssumeRoleSessionName,
				},
				Region:    "us-east-1",
				SecretKey: servicemocks.MockStaticSecretKey,
			},
			Description:              "config AssumeRoleDurationSeconds",
			ExpectedCredentialsValue: mockdata.MockStsAssumeRoleCredentials,
			ExpectedRegion:           "us-east-1",
			MockStsEndpoints: []*servicemocks.MockEndpoint{
				servicemocks.MockStsAssumeRoleValidEndpointWithOptions(map[string]string{"DurationSeconds": "3600"}),
				servicemocks.MockStsGetCallerIdentityValidEndpoint,
			},
		},
		{
			Config: &Config{
				AccessKey: servicemocks.MockStaticAccessKey,
				AssumeRole: &AssumeRole{
					RoleARN:     servicemocks.MockStsAssumeRoleArn,
					ExternalID:  servicemocks.MockStsAssumeRoleExternalId,
					SessionName: servicemocks.MockStsAssumeRoleSessionName,
				},
				Region:    "us-east-1",
				SecretKey: servicemocks.MockStaticSecretKey,
			},
			Description:              "config AssumeRoleExternalID",
			ExpectedCredentialsValue: mockdata.MockStsAssumeRoleCredentials,
			ExpectedRegion:           "us-east-1",
			MockStsEndpoints: []*servicemocks.MockEndpoint{
				servicemocks.MockStsAssumeRoleValidEndpointWithOptions(map[string]string{"ExternalId": servicemocks.MockStsAssumeRoleExternalId}),
				servicemocks.MockStsGetCallerIdentityValidEndpoint,
			},
		},
		{
			Config: &Config{
				AccessKey: servicemocks.MockStaticAccessKey,
				AssumeRole: &AssumeRole{
					RoleARN:     servicemocks.MockStsAssumeRoleArn,
					Policy:      servicemocks.MockStsAssumeRolePolicy,
					SessionName: servicemocks.MockStsAssumeRoleSessionName,
				},
				Region:    "us-east-1",
				SecretKey: servicemocks.MockStaticSecretKey,
			},
			Description:              "config AssumeRolePolicy",
			ExpectedCredentialsValue: mockdata.MockStsAssumeRoleCredentials,
			ExpectedRegion:           "us-east-1",
			MockStsEndpoints: []*servicemocks.MockEndpoint{
				servicemocks.MockStsAssumeRoleValidEndpointWithOptions(map[string]string{"Policy": servicemocks.MockStsAssumeRolePolicy}),
				servicemocks.MockStsGetCallerIdentityValidEndpoint,
			},
		},
		{
			Config: &Config{
				AccessKey: servicemocks.MockStaticAccessKey,
				AssumeRole: &AssumeRole{
					RoleARN:     servicemocks.MockStsAssumeRoleArn,
					PolicyARNs:  []string{servicemocks.MockStsAssumeRolePolicyArn},
					SessionName: servicemocks.MockStsAssumeRoleSessionName,
				},
				Region:    "us-east-1",
				SecretKey: servicemocks.MockStaticSecretKey,
			},
			Description:              "config AssumeRolePolicyARNs",
			ExpectedCredentialsValue: mockdata.MockStsAssumeRoleCredentials,
			ExpectedRegion:           "us-east-1",
			MockStsEndpoints: []*servicemocks.MockEndpoint{
				servicemocks.MockStsAssumeRoleValidEndpointWithOptions(map[string]string{"PolicyArns.member.1.arn": servicemocks.MockStsAssumeRolePolicyArn}),
				servicemocks.MockStsGetCallerIdentityValidEndpoint,
			},
		},
		{
			Config: &Config{
				AccessKey: servicemocks.MockStaticAccessKey,
				AssumeRole: &AssumeRole{
					RoleARN:     servicemocks.MockStsAssumeRoleArn,
					SessionName: servicemocks.MockStsAssumeRoleSessionName,
					Tags: map[string]string{
						servicemocks.MockStsAssumeRoleTagKey: servicemocks.MockStsAssumeRoleTagValue,
					},
				},
				Region:    "us-east-1",
				SecretKey: servicemocks.MockStaticSecretKey,
			},
			Description:              "config AssumeRoleTags",
			ExpectedCredentialsValue: mockdata.MockStsAssumeRoleCredentials,
			ExpectedRegion:           "us-east-1",
			MockStsEndpoints: []*servicemocks.MockEndpoint{
				servicemocks.MockStsAssumeRoleValidEndpointWithOptions(map[string]string{"Tags.member.1.Key": servicemocks.MockStsAssumeRoleTagKey, "Tags.member.1.Value": servicemocks.MockStsAssumeRoleTagValue}),
				servicemocks.MockStsGetCallerIdentityValidEndpoint,
			},
		},
		{
			Config: &Config{
				AccessKey: servicemocks.MockStaticAccessKey,
				AssumeRole: &AssumeRole{
					RoleARN:     servicemocks.MockStsAssumeRoleArn,
					SessionName: servicemocks.MockStsAssumeRoleSessionName,
					Tags: map[string]string{
						servicemocks.MockStsAssumeRoleTagKey: servicemocks.MockStsAssumeRoleTagValue,
					},
					TransitiveTagKeys: []string{servicemocks.MockStsAssumeRoleTagKey},
				},
				Region:    "us-east-1",
				SecretKey: servicemocks.MockStaticSecretKey,
			},
			Description:              "config AssumeRoleTransitiveTagKeys",
			ExpectedCredentialsValue: mockdata.MockStsAssumeRoleCredentials,
			ExpectedRegion:           "us-east-1",
			MockStsEndpoints: []*servicemocks.MockEndpoint{
				servicemocks.MockStsAssumeRoleValidEndpointWithOptions(map[string]string{"Tags.member.1.Key": servicemocks.MockStsAssumeRoleTagKey, "Tags.member.1.Value": servicemocks.MockStsAssumeRoleTagValue, "TransitiveTagKeys.member.1": servicemocks.MockStsAssumeRoleTagKey}),
				servicemocks.MockStsGetCallerIdentityValidEndpoint,
			},
		},
		{
			Config: &Config{
				Profile: "SharedCredentialsProfile",
				Region:  "us-east-1",
			},
			Description: "config Profile shared credentials profile aws_access_key_id",
			ExpectedCredentialsValue: aws.Credentials{
				AccessKeyID:     "ProfileSharedCredentialsAccessKey",
				SecretAccessKey: "ProfileSharedCredentialsSecretKey",
				Source:          sharedConfigCredentialsProvider,
			},
			ExpectedRegion: "us-east-1",
			MockStsEndpoints: []*servicemocks.MockEndpoint{
				servicemocks.MockStsGetCallerIdentityValidEndpoint,
			},
			SharedCredentialsFile: `
[default]
aws_access_key_id = DefaultSharedCredentialsAccessKey
aws_secret_access_key = DefaultSharedCredentialsSecretKey

[SharedCredentialsProfile]
aws_access_key_id = ProfileSharedCredentialsAccessKey
aws_secret_access_key = ProfileSharedCredentialsSecretKey
`,
		},
		{
			Config: &Config{
				Profile: "SharedConfigurationProfile",
				Region:  "us-east-1",
			},
			Description:              "config Profile shared configuration credential_source Ec2InstanceMetadata",
			EnableEc2MetadataServer:  true,
			ExpectedCredentialsValue: mockdata.MockStsAssumeRoleCredentials,
			ExpectedRegion:           "us-east-1",
			MockStsEndpoints: []*servicemocks.MockEndpoint{
				servicemocks.MockStsAssumeRoleValidEndpoint,
				servicemocks.MockStsGetCallerIdentityValidEndpoint,
			},
			SharedConfigurationFile: fmt.Sprintf(`
[profile SharedConfigurationProfile]
credential_source = Ec2InstanceMetadata
role_arn = %[1]s
role_session_name = %[2]s
`, servicemocks.MockStsAssumeRoleArn, servicemocks.MockStsAssumeRoleSessionName),
		},
		// 		{
		// 			Config: &Config{
		// 				Profile: "SharedConfigurationProfile",
		// 				Region:  "us-east-1",
		// 			},
		// 			Description: "config Profile shared configuration credential_source EcsContainer",
		// 			EnvironmentVariables: map[string]string{
		// 				"AWS_CONTAINER_CREDENTIALS_RELATIVE_URI": "/creds",
		// 			},
		// 			EnableEc2MetadataServer:    true,
		// 			EnableEcsCredentialsServer: true,
		// 			ExpectedCredentialsValue:   mockdata.MockStsAssumeRoleCredentialsV2,
		// 			ExpectedRegion:             "us-east-1",
		// 			MockStsEndpoints: []*servicemocks.MockEndpoint{
		// 				servicemocks.MockStsAssumeRoleValidEndpoint,
		// 				servicemocks.MockStsGetCallerIdentityValidEndpoint,
		// 			},
		// 			SharedConfigurationFile: fmt.Sprintf(`
		// [profile SharedConfigurationProfile]
		// credential_source = EcsContainer
		// role_arn = %[1]s
		// role_session_name = %[2]s
		// `, servicemocks.MockStsAssumeRoleArn, servicemocks.MockStsAssumeRoleSessionName),
		// 		},
		{
			Config: &Config{
				Profile: "SharedConfigurationProfile",
				Region:  "us-east-1",
			},
			Description:              "config Profile shared configuration source_profile",
			ExpectedCredentialsValue: mockdata.MockStsAssumeRoleCredentials,
			ExpectedRegion:           "us-east-1",
			MockStsEndpoints: []*servicemocks.MockEndpoint{
				servicemocks.MockStsAssumeRoleValidEndpoint,
				servicemocks.MockStsGetCallerIdentityValidEndpoint,
			},
			SharedConfigurationFile: fmt.Sprintf(`
[profile SharedConfigurationProfile]
role_arn = %[1]s
role_session_name = %[2]s
source_profile = SharedConfigurationSourceProfile

[profile SharedConfigurationSourceProfile]
aws_access_key_id = SharedConfigurationSourceAccessKey
aws_secret_access_key = SharedConfigurationSourceSecretKey
`, servicemocks.MockStsAssumeRoleArn, servicemocks.MockStsAssumeRoleSessionName),
		},
		{
			Config: &Config{
				Region: "us-east-1",
			},
			Description: "environment AWS_ACCESS_KEY_ID",
			EnvironmentVariables: map[string]string{
				"AWS_ACCESS_KEY_ID":     servicemocks.MockEnvAccessKey,
				"AWS_SECRET_ACCESS_KEY": servicemocks.MockEnvSecretKey,
			},
			ExpectedCredentialsValue: mockdata.MockEnvCredentials,
			ExpectedRegion:           "us-east-1",
			MockStsEndpoints: []*servicemocks.MockEndpoint{
				servicemocks.MockStsGetCallerIdentityValidEndpoint,
			},
		},
		{
			Config: &Config{
				AssumeRole: &AssumeRole{
					RoleARN:     servicemocks.MockStsAssumeRoleArn,
					SessionName: servicemocks.MockStsAssumeRoleSessionName,
				},
				Region: "us-east-1",
			},
			Description: "environment AWS_ACCESS_KEY_ID config AssumeRoleARN access key",
			EnvironmentVariables: map[string]string{
				"AWS_ACCESS_KEY_ID":     servicemocks.MockEnvAccessKey,
				"AWS_SECRET_ACCESS_KEY": servicemocks.MockEnvSecretKey,
			},
			ExpectedCredentialsValue: mockdata.MockStsAssumeRoleCredentials,
			ExpectedRegion:           "us-east-1",
			MockStsEndpoints: []*servicemocks.MockEndpoint{
				servicemocks.MockStsAssumeRoleValidEndpoint,
				servicemocks.MockStsGetCallerIdentityValidEndpoint,
			},
		},
		{
			Config: &Config{
				Region: "us-east-1",
			},
			Description: "environment AWS_PROFILE shared credentials profile aws_access_key_id",
			EnvironmentVariables: map[string]string{
				"AWS_PROFILE": "SharedCredentialsProfile",
			},
			ExpectedCredentialsValue: aws.Credentials{
				AccessKeyID:     "ProfileSharedCredentialsAccessKey",
				SecretAccessKey: "ProfileSharedCredentialsSecretKey",
				Source:          sharedConfigCredentialsProvider,
			},
			ExpectedRegion: "us-east-1",
			MockStsEndpoints: []*servicemocks.MockEndpoint{
				servicemocks.MockStsGetCallerIdentityValidEndpoint,
			},
			SharedCredentialsFile: `
[default]
aws_access_key_id = DefaultSharedCredentialsAccessKey
aws_secret_access_key = DefaultSharedCredentialsSecretKey

[SharedCredentialsProfile]
aws_access_key_id = ProfileSharedCredentialsAccessKey
aws_secret_access_key = ProfileSharedCredentialsSecretKey
`,
		},
		{
			Config: &Config{
				Region: "us-east-1",
			},
			Description:             "environment AWS_PROFILE shared configuration credential_source Ec2InstanceMetadata",
			EnableEc2MetadataServer: true,
			EnvironmentVariables: map[string]string{
				"AWS_PROFILE": "SharedConfigurationProfile",
			},
			ExpectedCredentialsValue: mockdata.MockStsAssumeRoleCredentials,
			ExpectedRegion:           "us-east-1",
			MockStsEndpoints: []*servicemocks.MockEndpoint{
				servicemocks.MockStsAssumeRoleValidEndpoint,
				servicemocks.MockStsGetCallerIdentityValidEndpoint,
			},
			SharedConfigurationFile: fmt.Sprintf(`
[profile SharedConfigurationProfile]
credential_source = Ec2InstanceMetadata
role_arn = %[1]s
role_session_name = %[2]s
`, servicemocks.MockStsAssumeRoleArn, servicemocks.MockStsAssumeRoleSessionName),
		},
		// 		{
		// 			Config: &Config{
		// 				Region: "us-east-1",
		// 			},
		// 			Description:                "environment AWS_PROFILE shared configuration credential_source EcsContainer",
		// 			EnableEc2MetadataServer:    true,
		// 			EnableEcsCredentialsServer: true,
		// 			EnvironmentVariables: map[string]string{
		// 				"AWS_CONTAINER_CREDENTIALS_RELATIVE_URI": "/creds",
		// 				"AWS_PROFILE":                            "SharedConfigurationProfile",
		// 			},
		// 			ExpectedCredentialsValue: mockdata.MockStsAssumeRoleCredentialsV2,
		// 			ExpectedRegion:           "us-east-1",
		// 			MockStsEndpoints: []*servicemocks.MockEndpoint{
		// 				servicemocks.MockStsAssumeRoleValidEndpoint,
		// 				servicemocks.MockStsGetCallerIdentityValidEndpoint,
		// 			},
		// 			SharedConfigurationFile: fmt.Sprintf(`
		// [profile SharedConfigurationProfile]
		// credential_source = EcsContainer
		// role_arn = %[1]s
		// role_session_name = %[2]s
		// `, servicemocks.MockStsAssumeRoleArn, servicemocks.MockStsAssumeRoleSessionName),
		// 		},
		{
			Config: &Config{
				Region: "us-east-1",
			},
			Description: "environment AWS_PROFILE shared configuration source_profile",
			EnvironmentVariables: map[string]string{
				"AWS_PROFILE": "SharedConfigurationProfile",
			},
			ExpectedCredentialsValue: mockdata.MockStsAssumeRoleCredentials,
			ExpectedRegion:           "us-east-1",
			MockStsEndpoints: []*servicemocks.MockEndpoint{
				servicemocks.MockStsAssumeRoleValidEndpoint,
				servicemocks.MockStsGetCallerIdentityValidEndpoint,
			},
			SharedConfigurationFile: fmt.Sprintf(`
[profile SharedConfigurationProfile]
role_arn = %[1]s
role_session_name = %[2]s
source_profile = SharedConfigurationSourceProfile

[profile SharedConfigurationSourceProfile]
aws_access_key_id = SharedConfigurationSourceAccessKey
aws_secret_access_key = SharedConfigurationSourceSecretKey
`, servicemocks.MockStsAssumeRoleArn, servicemocks.MockStsAssumeRoleSessionName),
		},
		{
			Config: &Config{
				Region: "us-east-1",
			},
			Description: "environment AWS_SESSION_TOKEN",
			EnvironmentVariables: map[string]string{
				"AWS_ACCESS_KEY_ID":     servicemocks.MockEnvAccessKey,
				"AWS_SECRET_ACCESS_KEY": servicemocks.MockEnvSecretKey,
				"AWS_SESSION_TOKEN":     servicemocks.MockEnvSessionToken,
			},
			ExpectedCredentialsValue: mockdata.MockEnvCredentialsWithSessionToken,
			ExpectedRegion:           "us-east-1",
			MockStsEndpoints: []*servicemocks.MockEndpoint{
				servicemocks.MockStsGetCallerIdentityValidEndpoint,
			},
		},
		{
			Config: &Config{
				Region: "us-east-1",
			},
			Description: "shared credentials default aws_access_key_id",
			ExpectedCredentialsValue: aws.Credentials{
				AccessKeyID:     "DefaultSharedCredentialsAccessKey",
				SecretAccessKey: "DefaultSharedCredentialsSecretKey",
				Source:          sharedConfigCredentialsProvider,
			},
			ExpectedRegion: "us-east-1",
			MockStsEndpoints: []*servicemocks.MockEndpoint{
				servicemocks.MockStsGetCallerIdentityValidEndpoint,
			},
			SharedCredentialsFile: `
[default]
aws_access_key_id = DefaultSharedCredentialsAccessKey
aws_secret_access_key = DefaultSharedCredentialsSecretKey
`,
		},
		{
			Config: &Config{
				AssumeRole: &AssumeRole{
					RoleARN:     servicemocks.MockStsAssumeRoleArn,
					SessionName: servicemocks.MockStsAssumeRoleSessionName,
				},
				Region: "us-east-1",
			},
			Description:              "shared credentials default aws_access_key_id config AssumeRoleARN access key",
			ExpectedCredentialsValue: mockdata.MockStsAssumeRoleCredentials,
			ExpectedRegion:           "us-east-1",
			MockStsEndpoints: []*servicemocks.MockEndpoint{
				servicemocks.MockStsAssumeRoleValidEndpoint,
				servicemocks.MockStsGetCallerIdentityValidEndpoint,
			},
			SharedCredentialsFile: `
[default]
aws_access_key_id = DefaultSharedCredentialsAccessKey
aws_secret_access_key = DefaultSharedCredentialsSecretKey
`,
		},
		{
			Config: &Config{
				Region: "us-east-1",
			},
			Description:              "web identity token access key",
			EnableEc2MetadataServer:  true,
			EnableWebIdentityToken:   true,
			ExpectedCredentialsValue: mockdata.MockStsAssumeRoleWithWebIdentityCredentials,
			ExpectedRegion:           "us-east-1",
			MockStsEndpoints: []*servicemocks.MockEndpoint{
				servicemocks.MockStsAssumeRoleWithWebIdentityValidEndpoint,
				servicemocks.MockStsGetCallerIdentityValidEndpoint,
			},
		},
		{
			Config: &Config{
				Region: "us-east-1",
			},
			Description:              "EC2 metadata access key",
			EnableEc2MetadataServer:  true,
			ExpectedCredentialsValue: mockdata.MockEc2MetadataCredentials,
			ExpectedRegion:           "us-east-1",
			MockStsEndpoints: []*servicemocks.MockEndpoint{
				servicemocks.MockStsGetCallerIdentityValidEndpoint,
			},
		},
		{
			Config: &Config{
				AssumeRole: &AssumeRole{
					RoleARN:     servicemocks.MockStsAssumeRoleArn,
					SessionName: servicemocks.MockStsAssumeRoleSessionName,
				},
				Region: "us-east-1",
			},
			Description:              "EC2 metadata access key config AssumeRoleARN access key",
			EnableEc2MetadataServer:  true,
			ExpectedCredentialsValue: mockdata.MockStsAssumeRoleCredentials,
			ExpectedRegion:           "us-east-1",
			MockStsEndpoints: []*servicemocks.MockEndpoint{
				servicemocks.MockStsAssumeRoleValidEndpoint,
				servicemocks.MockStsGetCallerIdentityValidEndpoint,
			},
		},
		{
			Config: &Config{
				Region: "us-east-1",
			},
			Description:                "ECS credentials access key",
			EnableEc2MetadataServer:    true,
			EnableEcsCredentialsServer: true,
			ExpectedCredentialsValue:   mockdata.MockEcsCredentialsCredentials,
			ExpectedRegion:             "us-east-1",
			MockStsEndpoints: []*servicemocks.MockEndpoint{
				servicemocks.MockStsGetCallerIdentityValidEndpoint,
			},
		},
		{
			Config: &Config{
				AssumeRole: &AssumeRole{
					RoleARN:     servicemocks.MockStsAssumeRoleArn,
					SessionName: servicemocks.MockStsAssumeRoleSessionName,
				},
				Region: "us-east-1",
			},
			Description:                "ECS credentials access key config AssumeRoleARN access key",
			EnableEc2MetadataServer:    true,
			EnableEcsCredentialsServer: true,
			ExpectedCredentialsValue:   mockdata.MockStsAssumeRoleCredentials,
			ExpectedRegion:             "us-east-1",
			MockStsEndpoints: []*servicemocks.MockEndpoint{
				servicemocks.MockStsAssumeRoleValidEndpoint,
				servicemocks.MockStsGetCallerIdentityValidEndpoint,
			},
		},
		{
			Config: &Config{
				AccessKey: servicemocks.MockStaticAccessKey,
				Region:    "us-east-1",
				SecretKey: servicemocks.MockStaticSecretKey,
			},
			Description: "config AccessKey over environment AWS_ACCESS_KEY_ID",
			EnvironmentVariables: map[string]string{
				"AWS_ACCESS_KEY_ID":     servicemocks.MockEnvAccessKey,
				"AWS_SECRET_ACCESS_KEY": servicemocks.MockEnvSecretKey,
			},
			ExpectedCredentialsValue: mockdata.MockStaticCredentials,
			ExpectedRegion:           "us-east-1",
			MockStsEndpoints: []*servicemocks.MockEndpoint{
				servicemocks.MockStsGetCallerIdentityValidEndpoint,
			},
		},
		{
			Config: &Config{
				AccessKey: servicemocks.MockStaticAccessKey,
				Region:    "us-east-1",
				SecretKey: servicemocks.MockStaticSecretKey,
			},
			Description:              "config AccessKey over shared credentials default aws_access_key_id",
			ExpectedCredentialsValue: mockdata.MockStaticCredentials,
			ExpectedRegion:           "us-east-1",
			MockStsEndpoints: []*servicemocks.MockEndpoint{
				servicemocks.MockStsGetCallerIdentityValidEndpoint,
			},
			SharedCredentialsFile: `
[default]
aws_access_key_id = DefaultSharedCredentialsAccessKey
aws_secret_access_key = DefaultSharedCredentialsSecretKey
`,
		},
		{
			Config: &Config{
				AccessKey: servicemocks.MockStaticAccessKey,
				Region:    "us-east-1",
				SecretKey: servicemocks.MockStaticSecretKey,
			},
			Description:              "config AccessKey over EC2 metadata access key",
			EnableEc2MetadataServer:  true,
			ExpectedCredentialsValue: mockdata.MockStaticCredentials,
			ExpectedRegion:           "us-east-1",
			MockStsEndpoints: []*servicemocks.MockEndpoint{
				servicemocks.MockStsGetCallerIdentityValidEndpoint,
			},
		},
		{
			Config: &Config{
				AccessKey: servicemocks.MockStaticAccessKey,
				Region:    "us-east-1",
				SecretKey: servicemocks.MockStaticSecretKey,
			},
			Description:                "config AccessKey over ECS credentials access key",
			EnableEc2MetadataServer:    true,
			EnableEcsCredentialsServer: true,
			ExpectedCredentialsValue:   mockdata.MockStaticCredentials,
			ExpectedRegion:             "us-east-1",
			MockStsEndpoints: []*servicemocks.MockEndpoint{
				servicemocks.MockStsGetCallerIdentityValidEndpoint,
			},
		},
		{
			Config: &Config{
				Region: "us-east-1",
			},
			Description: "environment AWS_ACCESS_KEY_ID over shared credentials default aws_access_key_id",
			EnvironmentVariables: map[string]string{
				"AWS_ACCESS_KEY_ID":     servicemocks.MockEnvAccessKey,
				"AWS_SECRET_ACCESS_KEY": servicemocks.MockEnvSecretKey,
			},
			ExpectedCredentialsValue: mockdata.MockEnvCredentials,
			ExpectedRegion:           "us-east-1",
			MockStsEndpoints: []*servicemocks.MockEndpoint{
				servicemocks.MockStsGetCallerIdentityValidEndpoint,
			},
			SharedCredentialsFile: `
[default]
aws_access_key_id = DefaultSharedCredentialsAccessKey
aws_secret_access_key = DefaultSharedCredentialsSecretKey
`,
		},
		{
			Config: &Config{
				Region: "us-east-1",
			},
			Description: "environment AWS_ACCESS_KEY_ID over EC2 metadata access key",
			EnvironmentVariables: map[string]string{
				"AWS_ACCESS_KEY_ID":     servicemocks.MockEnvAccessKey,
				"AWS_SECRET_ACCESS_KEY": servicemocks.MockEnvSecretKey,
			},
			EnableEc2MetadataServer:  true,
			ExpectedCredentialsValue: mockdata.MockEnvCredentials,
			ExpectedRegion:           "us-east-1",
			MockStsEndpoints: []*servicemocks.MockEndpoint{
				servicemocks.MockStsGetCallerIdentityValidEndpoint,
			},
		},
		{
			Config: &Config{
				Region: "us-east-1",
			},
			Description: "environment AWS_ACCESS_KEY_ID over ECS credentials access key",
			EnvironmentVariables: map[string]string{
				"AWS_ACCESS_KEY_ID":     servicemocks.MockEnvAccessKey,
				"AWS_SECRET_ACCESS_KEY": servicemocks.MockEnvSecretKey,
			},
			EnableEc2MetadataServer:    true,
			EnableEcsCredentialsServer: true,
			ExpectedCredentialsValue:   mockdata.MockEnvCredentials,
			ExpectedRegion:             "us-east-1",
			MockStsEndpoints: []*servicemocks.MockEndpoint{
				servicemocks.MockStsGetCallerIdentityValidEndpoint,
			},
		},
		{
			Config: &Config{
				Region: "us-east-1",
			},
			Description:             "shared credentials default aws_access_key_id over EC2 metadata access key",
			EnableEc2MetadataServer: true,
			ExpectedCredentialsValue: aws.Credentials{
				AccessKeyID:     "DefaultSharedCredentialsAccessKey",
				SecretAccessKey: "DefaultSharedCredentialsSecretKey",
				Source:          sharedConfigCredentialsProvider,
			},
			ExpectedRegion: "us-east-1",
			MockStsEndpoints: []*servicemocks.MockEndpoint{
				servicemocks.MockStsGetCallerIdentityValidEndpoint,
			},
			SharedCredentialsFile: `
[default]
aws_access_key_id = DefaultSharedCredentialsAccessKey
aws_secret_access_key = DefaultSharedCredentialsSecretKey
`,
		},
		{
			Config: &Config{
				Region: "us-east-1",
			},
			Description:                "shared credentials default aws_access_key_id over ECS credentials access key",
			EnableEc2MetadataServer:    true,
			EnableEcsCredentialsServer: true,
			ExpectedCredentialsValue: aws.Credentials{
				AccessKeyID:     "DefaultSharedCredentialsAccessKey",
				SecretAccessKey: "DefaultSharedCredentialsSecretKey",
				Source:          sharedConfigCredentialsProvider,
			},
			ExpectedRegion: "us-east-1",
			MockStsEndpoints: []*servicemocks.MockEndpoint{
				servicemocks.MockStsGetCallerIdentityValidEndpoint,
			},
			SharedCredentialsFile: `
[default]
aws_access_key_id = DefaultSharedCredentialsAccessKey
aws_secret_access_key = DefaultSharedCredentialsSecretKey
`,
		},
		{
			Config: &Config{
				Region: "us-east-1",
			},
			Description:                "ECS credentials access key over EC2 metadata access key",
			EnableEc2MetadataServer:    true,
			EnableEcsCredentialsServer: true,
			ExpectedCredentialsValue:   mockdata.MockEcsCredentialsCredentials,
			ExpectedRegion:             "us-east-1",
			MockStsEndpoints: []*servicemocks.MockEndpoint{
				servicemocks.MockStsGetCallerIdentityValidEndpoint,
			},
		},
		{
			Config: &Config{
				AccessKey: servicemocks.MockStaticAccessKey,
				SecretKey: servicemocks.MockStaticSecretKey,
			},
			Description:              "retrieve region from shared configuration file",
			ExpectedCredentialsValue: mockdata.MockStaticCredentials,
			ExpectedRegion:           "us-east-1",
			MockStsEndpoints: []*servicemocks.MockEndpoint{
				servicemocks.MockStsGetCallerIdentityValidEndpoint,
			},
			SharedConfigurationFile: `
[default]
region = us-east-1
`,
		},
		{
			Config: &Config{
				AccessKey: servicemocks.MockStaticAccessKey,
				AssumeRole: &AssumeRole{
					RoleARN:     servicemocks.MockStsAssumeRoleArn,
					SessionName: servicemocks.MockStsAssumeRoleSessionName,
				},
				DebugLogging: true,
				Region:       "us-east-1",
				SecretKey:    servicemocks.MockStaticSecretKey,
			},
			Description: "assume role error",
			ExpectedError: func(err error) bool {
				return IsCannotAssumeRoleError(err)
			},
			ExpectedRegion: "us-east-1",
			MockStsEndpoints: []*servicemocks.MockEndpoint{
				servicemocks.MockStsAssumeRoleInvalidEndpointInvalidClientTokenId,
				servicemocks.MockStsGetCallerIdentityValidEndpoint,
			},
		},
		// {
		// 	Config: &Config{
		// 		AccessKey: servicemocks.MockStaticAccessKey,
		// 		Region:    "us-east-1",
		// 		SecretKey: servicemocks.MockStaticSecretKey,
		// 	},
		// 	Description: "credential validation error",
		// 	ExpectedError: func(err error) bool {
		// 		return tfawserr.ErrCodeEquals(err, "AccessDenied")
		// 	},
		// 	MockStsEndpoints: []*servicemocks.MockEndpoint{
		// 		servicemocks.MockStsGetCallerIdentityInvalidEndpointAccessDenied,
		// 	},
		// },
		{
			Config: &Config{
				Profile: "SharedConfigurationProfile",
				Region:  "us-east-1",
			},
			Description: "session creation error",
			ExpectedError: func(err error) bool {
				var e config.CredentialRequiresARNError
				return errors.As(err, &e)
			},
			SharedConfigurationFile: `
[profile SharedConfigurationProfile]
source_profile = SourceSharedCredentials
`,
		},
		{
			Config: &Config{
				AccessKey:           servicemocks.MockStaticAccessKey,
				Region:              "us-east-1",
				SecretKey:           servicemocks.MockStaticSecretKey,
				SkipCredsValidation: true,
			},
			Description:              "skip credentials validation",
			ExpectedCredentialsValue: mockdata.MockStaticCredentials,
			ExpectedRegion:           "us-east-1",
		},
		{
			Config: &Config{
				Region:               "us-east-1",
				SkipMetadataApiCheck: true,
			},
			Description:             "skip EC2 metadata API check",
			EnableEc2MetadataServer: true,
			ExpectedError: func(err error) bool {
				return IsNoValidCredentialSourcesError(err)
			},
			ExpectedRegion: "us-east-1",
		},
	}

	for _, testCase := range testCases {
		testCase := testCase

		t.Run(testCase.Description, func(t *testing.T) {
			oldEnv := servicemocks.InitSessionTestEnv()
			defer servicemocks.PopEnv(oldEnv)

			if testCase.EnableEc2MetadataServer {
				closeEc2Metadata := servicemocks.AwsMetadataApiMock(append(servicemocks.Ec2metadata_securityCredentialsEndpoints, servicemocks.Ec2metadata_instanceIdEndpoint, servicemocks.Ec2metadata_iamInfoEndpoint))
				defer closeEc2Metadata()
			}

			if testCase.EnableEcsCredentialsServer {
				closeEcsCredentials := servicemocks.EcsCredentialsApiMock()
				defer closeEcsCredentials()
			}

			if testCase.EnableWebIdentityToken {
				file, err := ioutil.TempFile("", "aws-sdk-go-base-web-identity-token-file")

				if err != nil {
					t.Fatalf("unexpected error creating temporary shared configuration file: %s", err)
				}

				defer os.Remove(file.Name())

				err = ioutil.WriteFile(file.Name(), []byte(servicemocks.MockWebIdentityToken), 0600)

				if err != nil {
					t.Fatalf("unexpected error writing shared configuration file: %s", err)
				}

				os.Setenv("AWS_ROLE_ARN", servicemocks.MockStsAssumeRoleWithWebIdentityArn)
				os.Setenv("AWS_ROLE_SESSION_NAME", servicemocks.MockStsAssumeRoleWithWebIdentitySessionName)
				os.Setenv("AWS_WEB_IDENTITY_TOKEN_FILE", file.Name())
			}

			closeSts, _, stsEndpoint := mockdata.GetMockedAwsApiSession("STS", testCase.MockStsEndpoints)
			defer closeSts()

			testCase.Config.StsEndpoint = stsEndpoint

			if testCase.SharedConfigurationFile != "" {
				file, err := ioutil.TempFile("", "aws-sdk-go-base-shared-configuration-file")

				if err != nil {
					t.Fatalf("unexpected error creating temporary shared configuration file: %s", err)
				}

				defer os.Remove(file.Name())

				err = ioutil.WriteFile(file.Name(), []byte(testCase.SharedConfigurationFile), 0600)

				if err != nil {
					t.Fatalf("unexpected error writing shared configuration file: %s", err)
				}

				testCase.Config.SharedConfigFiles = []string{file.Name()}
			}

			if testCase.SharedCredentialsFile != "" {
				file, err := ioutil.TempFile("", "aws-sdk-go-base-shared-credentials-file")

				if err != nil {
					t.Fatalf("unexpected error creating temporary shared credentials file: %s", err)
				}

				defer os.Remove(file.Name())

				err = ioutil.WriteFile(file.Name(), []byte(testCase.SharedCredentialsFile), 0600)

				if err != nil {
					t.Fatalf("unexpected error writing shared credentials file: %s", err)
				}

				testCase.Config.SharedCredentialsFiles = []string{file.Name()}
				if testCase.ExpectedCredentialsValue.Source == sharedConfigCredentialsProvider {
					testCase.ExpectedCredentialsValue.Source = sharedConfigCredentialsSource(file.Name())
				}
			}

			for k, v := range testCase.EnvironmentVariables {
				os.Setenv(k, v)
			}

			awsConfig, err := GetAwsConfig(context.Background(), testCase.Config)

			if err != nil {
				if testCase.ExpectedError == nil {
					t.Fatalf("expected no error, got '%[1]T' error: %[1]s", err)
				}

				if !testCase.ExpectedError(err) {
					t.Fatalf("unexpected GetAwsConfig() '%[1]T' error: %[1]s", err)
				}

				t.Logf("received expected '%[1]T' error: %[1]s", err)
				return
			}

			if err == nil && testCase.ExpectedError != nil {
				t.Fatalf("expected error, got no error")
			}

			credentialsValue, err := awsConfig.Credentials.Retrieve(context.Background())

			if err != nil {
				t.Fatalf("unexpected credentials Retrieve() error: %s", err)
			}

			if diff := cmp.Diff(credentialsValue, testCase.ExpectedCredentialsValue, cmpopts.IgnoreFields(aws.Credentials{}, "Expires")); diff != "" {
				t.Fatalf("unexpected credentials: (- got, + expected)\n%s", diff)
			}
			// TODO: test credentials.Expires

			if expected, actual := testCase.ExpectedRegion, awsConfig.Region; expected != actual {
				t.Fatalf("expected region (%s), got: %s", expected, actual)
			}

			// if testCase.ExpectedUserAgent != "" {
			// 	clientInfo := metadata.ClientInfo{
			// 		Endpoint:    "http://endpoint",
			// 		SigningName: "",
			// 	}
			// 	conn := client.New(*actualSession.Config, clientInfo, actualSession.Handlers)

			// 	req := conn.NewRequest(&request.Operation{Name: "Operation"}, nil, nil)

			// 	if err := req.Build(); err != nil {
			// 		t.Fatalf("expect no Request.Build() error, got %s", err)
			// 	}

			// 	if e, a := testCase.ExpectedUserAgent, req.HTTPRequest.Header.Get("User-Agent"); e != a {
			// 		t.Errorf("expected User-Agent (%s), got: %s", e, a)
			// 	}
			// }
		})
	}
}

func TestUserAgentProducts(t *testing.T) {
	testCases := []struct {
		Config               *Config
		Description          string
		EnvironmentVariables map[string]string
		ExpectedUserAgent    string
	}{
		{
			Config: &Config{
				AccessKey: servicemocks.MockStaticAccessKey,
				Region:    "us-east-1",
				SecretKey: servicemocks.MockStaticSecretKey,
			},
			Description:       "standard User-Agent",
			ExpectedUserAgent: awsSdkGoUserAgent(),
		},
		{
			Config: &Config{
				AccessKey: servicemocks.MockStaticAccessKey,
				Region:    "us-east-1",
				SecretKey: servicemocks.MockStaticSecretKey,
			},
			Description: "customized User-Agent TF_APPEND_USER_AGENT",
			EnvironmentVariables: map[string]string{
				constants.AppendUserAgentEnvVar: "Last",
			},
			ExpectedUserAgent: awsSdkGoUserAgent() + " Last",
		},
		{
			Config: &Config{
				AccessKey: servicemocks.MockStaticAccessKey,
				Region:    "us-east-1",
				SecretKey: servicemocks.MockStaticSecretKey,
				UserAgentProducts: []*UserAgentProduct{
					{
						Name:    "first",
						Version: "1.0",
					},
					{
						Name:    "second",
						Version: "1.2.3",
						Extra:   []string{"+https://www.example.com/"},
					},
				},
			},
			Description:       "customized User-Agent Products",
			ExpectedUserAgent: "first/1.0 second/1.2.3 (+https://www.example.com/) " + awsSdkGoUserAgent(),
		},
		{
			Config: &Config{
				AccessKey: servicemocks.MockStaticAccessKey,
				Region:    "us-east-1",
				SecretKey: servicemocks.MockStaticSecretKey,
				UserAgentProducts: []*UserAgentProduct{
					{
						Name:    "first",
						Version: "1.0",
					},
					{
						Name:    "second",
						Version: "1.2.3",
						Extra:   []string{"+https://www.example.com/"},
					},
				},
			},
			Description: "customized User-Agent Products and TF_APPEND_USER_AGENT",
			EnvironmentVariables: map[string]string{
				constants.AppendUserAgentEnvVar: "Last",
			},
			ExpectedUserAgent: "first/1.0 second/1.2.3 (+https://www.example.com/) " + awsSdkGoUserAgent() + " Last",
		},
	}

	var (
		httpUserAgent string
		httpSdkAgent  string
	)

	errCancelOperation := fmt.Errorf("Cancelling request")

	readUserAgent := middleware.FinalizeMiddlewareFunc("ReadUserAgent", func(_ context.Context, in middleware.FinalizeInput, next middleware.FinalizeHandler) (middleware.FinalizeOutput, middleware.Metadata, error) {
		request, ok := in.Request.(*smithyhttp.Request)
		if !ok {
			t.Fatalf("Expected *github.com/aws/smithy-go/transport/http.Request, got %s", fullTypeName(in.Request))
		}
		httpUserAgent = request.UserAgent()
		httpSdkAgent = request.Header.Get("X-Amz-User-Agent")

		return middleware.FinalizeOutput{}, middleware.Metadata{}, errCancelOperation
	})

	for _, testCase := range testCases {
		testCase := testCase

		t.Run(testCase.Description, func(t *testing.T) {
			oldEnv := servicemocks.InitSessionTestEnv()
			defer servicemocks.PopEnv(oldEnv)

			for k, v := range testCase.EnvironmentVariables {
				os.Setenv(k, v)
			}

			testCase.Config.SkipCredsValidation = true

			awsConfig, err := GetAwsConfig(context.Background(), testCase.Config)
			if err != nil {
				t.Fatalf("error in GetAwsConfig() '%[1]T': %[1]s", err)
			}

			client := sts.NewFromConfig(awsConfig)

			_, err = client.GetCallerIdentity(context.Background(), &sts.GetCallerIdentityInput{},
				func(opts *sts.Options) {
					opts.APIOptions = append(opts.APIOptions, func(stack *middleware.Stack) error {
						return stack.Finalize.Add(readUserAgent, middleware.Before)
					})
				},
			)
			if err == nil {
				t.Fatal("Expected an error, got none")
			} else if !errors.Is(err, errCancelOperation) {
				t.Fatalf("Unexpected error: %s", err)
			}

			var userAgentParts []string
			for _, v := range strings.Split(httpUserAgent, " ") {
				if !strings.HasPrefix(v, "api/") {
					userAgentParts = append(userAgentParts, v)
				}
			}
			cleanedUserAgent := strings.Join(userAgentParts, " ")

			if testCase.ExpectedUserAgent != cleanedUserAgent {
				t.Errorf("expected User-Agent %q, got %q", testCase.ExpectedUserAgent, cleanedUserAgent)
			}

			// The header X-Amz-User-Agent was disabled but not removed in v1.3.0 (2021-03-18)
			if httpSdkAgent != "" {
				t.Errorf("expected header X-Amz-User-Agent to not be set, got %q", httpSdkAgent)
			}
		})
	}
}

func awsSdkGoUserAgent() string {
	// See https://github.com/aws/aws-sdk-go-v2/blob/994cb2c7c1c822dc628949e7ae2941b9c856ccb3/aws/middleware/user_agent_test.go#L18
	return fmt.Sprintf("%s/%s os/%s lang/go/%s md/GOOS/%s md/GOARCH/%s", aws.SDKName, aws.SDKVersion, getNormalizedOSName(), strings.TrimPrefix(runtime.Version(), "go"), runtime.GOOS, runtime.GOARCH)
}

// Copied from https://github.com/aws/aws-sdk-go-v2/blob/main/aws/middleware/osname.go
func getNormalizedOSName() (os string) {
	switch runtime.GOOS {
	case "android":
		os = "android"
	case "linux":
		os = "linux"
	case "windows":
		os = "windows"
	case "darwin":
		os = "macos"
	case "ios":
		os = "ios"
	default:
		os = "other"
	}
	return os
}

func fullTypeName(i interface{}) string {
	return fullValueTypeName(reflect.ValueOf(i))
}

func fullValueTypeName(v reflect.Value) string {
	if v.Kind() == reflect.Ptr {
		return "*" + fullValueTypeName(reflect.Indirect(v))
	}

	requestType := v.Type()
	return fmt.Sprintf("%s.%s", requestType.PkgPath(), requestType.Name())
}

func TestGetAwsConfigWithAccountIDAndPartition(t *testing.T) {
	oldEnv := servicemocks.InitSessionTestEnv()
	defer servicemocks.PopEnv(oldEnv)

	testCases := []struct {
		desc                    string
		config                  *Config
		skipRequestingAccountId bool
		expectedAcctID          string
		expectedPartition       string
		expectError             bool
		mockStsEndpoints        []*servicemocks.MockEndpoint
	}{
		{
			"StandardProvider_Config",
			&Config{
				AccessKey: "MockAccessKey",
				SecretKey: "MockSecretKey",
				Region:    "us-west-2"},
			false,
			"222222222222", "aws", false,
			[]*servicemocks.MockEndpoint{
				servicemocks.MockStsGetCallerIdentityValidEndpoint,
			},
		},
		{
			"SkipCredsValidation_Config",
			&Config{
				AccessKey:           "MockAccessKey",
				SecretKey:           "MockSecretKey",
				Region:              "us-west-2",
				SkipCredsValidation: true},
			false,
			"222222222222", "aws", false,
			[]*servicemocks.MockEndpoint{
				servicemocks.MockStsGetCallerIdentityValidEndpoint,
			},
		},
		{
			"SkipRequestingAccountId_Config",
			&Config{
				AccessKey:           "MockAccessKey",
				SecretKey:           "MockSecretKey",
				Region:              "us-west-2",
				SkipCredsValidation: true},
			true,
			"", "aws", false, []*servicemocks.MockEndpoint{},
		},
		{
			"WithAssumeRole",
			&Config{
				AccessKey: "MockAccessKey",
				SecretKey: "MockSecretKey",
				Region:    "us-west-2",
				AssumeRole: &AssumeRole{
					RoleARN:     servicemocks.MockStsAssumeRoleArn,
					SessionName: servicemocks.MockStsAssumeRoleSessionName,
				},
			},
			false,
			"555555555555", "aws", false, []*servicemocks.MockEndpoint{
				servicemocks.MockStsAssumeRoleValidEndpoint,
				servicemocks.MockStsGetCallerIdentityValidAssumedRoleEndpoint,
			},
		},
	}

	for _, testCase := range testCases {
		tc := testCase

		t.Run(tc.desc, func(t *testing.T) {
			ts := servicemocks.MockAwsApiServer("STS", tc.mockStsEndpoints)
			defer ts.Close()
			tc.config.StsEndpoint = ts.URL

			awsConfig, err := GetAwsConfig(context.Background(), tc.config)
			if err != nil {
				t.Fatalf("expected no error from GetAwsConfig(), got: %s", err)
			}
			acctID, part, err := GetAwsAccountIDAndPartition(context.Background(), awsConfig, tc.config.SkipCredsValidation, tc.skipRequestingAccountId)
			if err != nil {
				if !tc.expectError {
					t.Fatalf("expected no error, got: %s", err)
				}

				if !IsNoValidCredentialSourcesError(err) {
					t.Fatalf("expected no valid credential sources error, got: %s", err)
				}

				t.Logf("received expected error: %s", err)
				return
			}

			if acctID != tc.expectedAcctID {
				t.Errorf("expected account ID (%s), got: %s", tc.expectedAcctID, acctID)
			}

			if part != tc.expectedPartition {
				t.Errorf("expected partition (%s), got: %s", tc.expectedPartition, part)
			}
		})
	}
}

type mockRetryableError struct{ b bool }

func (m mockRetryableError) RetryableError() bool { return m.b }
func (m mockRetryableError) Error() string {
	return fmt.Sprintf("mock retryable %t", m.b)
}

func TestRetryHandlers(t *testing.T) {
	const maxRetries = 10

	testcases := map[string]struct {
		NextHandler   func() middleware.FinalizeHandler
		ExpectResults retry.AttemptResults
		Err           error
	}{
		"stops at maxRetries for retryable errors": {
			NextHandler: func() middleware.FinalizeHandler {
				num := 0
				reqsErrs := make([]error, maxRetries)
				for i := 0; i < maxRetries; i++ {
					reqsErrs[i] = mockRetryableError{b: true}
				}
				return middleware.FinalizeHandlerFunc(func(ctx context.Context, in middleware.FinalizeInput) (out middleware.FinalizeOutput, metadata middleware.Metadata, err error) {
					if num >= len(reqsErrs) {
						err = fmt.Errorf("more requests than expected")
					} else {
						err = reqsErrs[num]
						num++
					}
					return out, metadata, err
				})
			},
			Err: fmt.Errorf("exceeded maximum number of attempts"),
			ExpectResults: func() retry.AttemptResults {
				results := retry.AttemptResults{
					Results: make([]retry.AttemptResult, maxRetries),
				}
				for i := 0; i < maxRetries-1; i++ {
					results.Results[i] = retry.AttemptResult{
						Err:       mockRetryableError{b: true},
						Retryable: true,
						Retried:   true,
					}
				}
				results.Results[maxRetries-1] = retry.AttemptResult{
					Err:       &retry.MaxAttemptsError{Attempt: maxRetries, Err: mockRetryableError{b: true}},
					Retryable: true,
				}
				return results
			}(),
		},
		"stops at MaxNetworkRetryCount for 'no such host' errors": {
			NextHandler: func() middleware.FinalizeHandler {
				num := 0
				reqsErrs := make([]error, constants.MaxNetworkRetryCount)
				for i := 0; i < constants.MaxNetworkRetryCount; i++ {
					reqsErrs[i] = &net.OpError{Op: "dial", Err: errors.New("no such host")}
				}
				return middleware.FinalizeHandlerFunc(func(ctx context.Context, in middleware.FinalizeInput) (out middleware.FinalizeOutput, metadata middleware.Metadata, err error) {
					if num >= len(reqsErrs) {
						err = fmt.Errorf("more requests than expected")
					} else {
						err = reqsErrs[num]
						num++
					}
					return out, metadata, err
				})
			},
			Err: fmt.Errorf("exceeded maximum number of attempts"),
			ExpectResults: func() retry.AttemptResults {
				results := retry.AttemptResults{
					Results: make([]retry.AttemptResult, constants.MaxNetworkRetryCount),
				}
				for i := 0; i < constants.MaxNetworkRetryCount-1; i++ {
					results.Results[i] = retry.AttemptResult{
						Err:       &net.OpError{Op: "dial", Err: errors.New("no such host")},
						Retryable: true,
						Retried:   true,
					}
				}
				results.Results[constants.MaxNetworkRetryCount-1] = retry.AttemptResult{
					Err:       &retry.MaxAttemptsError{Attempt: constants.MaxNetworkRetryCount, Err: &net.OpError{Op: "dial", Err: errors.New("no such host")}},
					Retryable: true,
				}
				return results
			}(),
		},
		"stops at MaxNetworkRetryCount for 'connection refused' errors": {
			NextHandler: func() middleware.FinalizeHandler {
				num := 0
				reqsErrs := make([]error, constants.MaxNetworkRetryCount)
				for i := 0; i < constants.MaxNetworkRetryCount; i++ {
					reqsErrs[i] = &net.OpError{Op: "dial", Err: errors.New("connection refused")}
				}
				return middleware.FinalizeHandlerFunc(func(ctx context.Context, in middleware.FinalizeInput) (out middleware.FinalizeOutput, metadata middleware.Metadata, err error) {
					if num >= len(reqsErrs) {
						err = fmt.Errorf("more requests than expected")
					} else {
						err = reqsErrs[num]
						num++
					}
					return out, metadata, err
				})
			},
			Err: fmt.Errorf("exceeded maximum number of attempts"),
			ExpectResults: func() retry.AttemptResults {
				results := retry.AttemptResults{
					Results: make([]retry.AttemptResult, constants.MaxNetworkRetryCount),
				}
				for i := 0; i < constants.MaxNetworkRetryCount-1; i++ {
					results.Results[i] = retry.AttemptResult{
						Err:       &net.OpError{Op: "dial", Err: errors.New("connection refused")},
						Retryable: true,
						Retried:   true,
					}
				}
				results.Results[constants.MaxNetworkRetryCount-1] = retry.AttemptResult{
					Err:       &retry.MaxAttemptsError{Attempt: constants.MaxNetworkRetryCount, Err: &net.OpError{Op: "dial", Err: errors.New("connection refused")}},
					Retryable: true,
				}
				return results
			}(),
		},
		"stops at maxRetries for other network errors": {
			NextHandler: func() middleware.FinalizeHandler {
				num := 0
				reqsErrs := make([]error, maxRetries)
				for i := 0; i < maxRetries; i++ {
					reqsErrs[i] = &net.OpError{Op: "dial", Err: errors.New("other error")}
				}
				return middleware.FinalizeHandlerFunc(func(ctx context.Context, in middleware.FinalizeInput) (out middleware.FinalizeOutput, metadata middleware.Metadata, err error) {
					if num >= len(reqsErrs) {
						err = fmt.Errorf("more requests than expected")
					} else {
						err = reqsErrs[num]
						num++
					}
					return out, metadata, err
				})
			},
			Err: fmt.Errorf("exceeded maximum number of attempts"),
			ExpectResults: func() retry.AttemptResults {
				results := retry.AttemptResults{
					Results: make([]retry.AttemptResult, maxRetries),
				}
				for i := 0; i < maxRetries-1; i++ {
					results.Results[i] = retry.AttemptResult{
						Err:       &net.OpError{Op: "dial", Err: errors.New("other error")},
						Retryable: true,
						Retried:   true,
					}
				}
				results.Results[maxRetries-1] = retry.AttemptResult{
					Err:       &retry.MaxAttemptsError{Attempt: maxRetries, Err: &net.OpError{Op: "dial", Err: errors.New("other error")}},
					Retryable: true,
				}
				return results
			}(),
		},
	}

	for name, testcase := range testcases {
		testcase := testcase

		t.Run(name, func(t *testing.T) {
			oldEnv := servicemocks.InitSessionTestEnv()
			defer servicemocks.PopEnv(oldEnv)

			config := &Config{
				AccessKey:           servicemocks.MockStaticAccessKey,
				Region:              "us-east-1",
				MaxRetries:          maxRetries,
				SecretKey:           servicemocks.MockStaticSecretKey,
				SkipCredsValidation: true,
				DebugLogging:        true,
			}
			awsConfig, err := GetAwsConfig(context.Background(), config)
			if err != nil {
				t.Fatalf("unexpected error from GetAwsConfig(): %s", err)
			}
			if awsConfig.Retryer == nil {
				t.Fatal("No Retryer configured on awsConfig")
			}

			am := retry.NewAttemptMiddleware(&withNoDelay{
				Retryer: awsConfig.Retryer(),
			}, func(i interface{}) interface{} {
				return i
			})
			_, metadata, err := am.HandleFinalize(context.Background(), middleware.FinalizeInput{Request: nil}, testcase.NextHandler())
			if err != nil && testcase.Err == nil {
				t.Errorf("expect no error, got %v", err)
			} else if err == nil && testcase.Err != nil {
				t.Errorf("expect error, got none")
			} else if err != nil && testcase.Err != nil {
				if !strings.Contains(err.Error(), testcase.Err.Error()) {
					t.Errorf("expect %v, got %v", testcase.Err, err)
				}
			}

			attemptResults, ok := retry.GetAttemptResults(metadata)
			if !ok {
				t.Fatalf("expected metadata to contain attempt results, got none")
			}
			if e, a := testcase.ExpectResults, attemptResults; !reflect.DeepEqual(e, a) {
				t.Fatalf("expected %v, got %v", e, a)
			}

			for i, attempt := range attemptResults.Results {
				_, ok := retry.GetAttemptResults(attempt.ResponseMetadata)
				if ok {
					t.Errorf("expect no attempt to include AttemptResults metadata, %v does, %#v", i, attempt)
				}
			}
		})
	}
}

type withNoDelay struct {
	aws.Retryer
}

func (r *withNoDelay) RetryDelay(attempt int, err error) (time.Duration, error) {
	delay, delayErr := r.Retryer.RetryDelay(attempt, err)
	if delayErr != nil {
		return delay, delayErr
	}

	return 0 * time.Second, nil
}
