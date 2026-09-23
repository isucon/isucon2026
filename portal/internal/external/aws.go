package external

import (
	"context"
	"fmt"
	"time"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// AWS は S3 presign（FR-REG-7）・EC2 同期（FR-J）を束ねる。
// ジョブキューは SQS ではなく PostgreSQL（internal/job.DBQueue）。
type AWS struct {
	Region string

	s3Presigner *s3.PresignClient
	ec2Client   *ec2.Client
}

func NewAWS(ctx context.Context, region string) (*AWS, error) {
	cfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(region))
	if err != nil {
		return nil, err
	}
	return &AWS{
		Region:      region,
		s3Presigner: s3.NewPresignClient(s3.NewFromConfig(cfg)),
		ec2Client:   ec2.NewFromConfig(cfg),
	}, nil
}

// PresignAvatarUpload は Avatar.upload_url 相当（有効 600 秒の PUT）。
func (a *AWS) PresignAvatarUpload(ctx context.Context, bucket, key string) (string, error) {
	out, err := a.s3Presigner.PresignPutObject(ctx, &s3.PutObjectInput{
		Bucket: &bucket,
		Key:    &key,
	}, s3.WithPresignExpires(600*time.Second))
	if err != nil {
		return "", err
	}
	return out.URL, nil
}

// EC2Instance は SyncContestantInstancesJob が読むタグ付きインスタンス。
type EC2Instance struct {
	InstanceID       string
	TeamID           int64
	Number           int64
	PrivateIPAddress string
	PublicIPAddress  string
}

// ListContestantInstances はタグ Project / Role=contestant のインスタンスを列挙する。
func (a *AWS) ListContestantInstances(ctx context.Context, project string) ([]EC2Instance, error) {
	var out []EC2Instance
	paginator := ec2.NewDescribeInstancesPaginator(a.ec2Client, &ec2.DescribeInstancesInput{
		Filters: []ec2types.Filter{
			{Name: strPtr("tag:Project"), Values: []string{project}},
			{Name: strPtr("tag:Role"), Values: []string{"contestant"}},
		},
	})
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, res := range page.Reservations {
			for _, i := range res.Instances {
				if i.State != nil && i.State.Name == ec2types.InstanceStateNameTerminated {
					continue
				}
				inst := EC2Instance{}
				if i.InstanceId != nil {
					inst.InstanceID = *i.InstanceId
				}
				if i.PrivateIpAddress != nil {
					inst.PrivateIPAddress = *i.PrivateIpAddress
				}
				if i.PublicIpAddress != nil {
					inst.PublicIPAddress = *i.PublicIpAddress
				}
				for _, tag := range i.Tags {
					if tag.Key == nil || tag.Value == nil {
						continue
					}
					switch *tag.Key {
					case "IsuconTeamID":
						fmt.Sscan(*tag.Value, &inst.TeamID)
					case "IsuconInstanceNum":
						fmt.Sscan(*tag.Value, &inst.Number)
					}
				}
				if inst.TeamID <= 0 || inst.Number <= 0 {
					continue
				}
				out = append(out, inst)
			}
		}
	}
	return out, nil
}

func strPtr(s string) *string { return &s }
