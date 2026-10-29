package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/dihemoe/alisten/internal/app/port"
)

const (
	// AWS 的默认区域，在这个区域创建 bucket 时不能指定 LocationConstraint。
	defaultRegion   = "us-east-1"
	rollbackTimeout = 10 * time.Second
)

type Config struct {
	Endpoint        string
	Region          string
	Bucket          string
	AccessKeyID     string
	SecretAccessKey string
}

type Storage struct {
	client *s3.Client
	bucket string
	region string
}

var _ port.MediaStore = (*Storage)(nil)

func New(cfg Config) *Storage {
	client := s3.New(s3.Options{
		BaseEndpoint: aws.String(cfg.Endpoint),
		Region:       cfg.Region,
		// 为兼容不支持虚拟主机寻址的 S3 服务，统一使用路径寻址。
		UsePathStyle: true,
		Credentials:  credentials.NewStaticCredentialsProvider(cfg.AccessKeyID, cfg.SecretAccessKey, ""),
		// 仅在操作要求时计算或验证校验和：部分 S3 兼容服务不支持 SDK 默认添加的灵活校验和。
		RequestChecksumCalculation: aws.RequestChecksumCalculationWhenRequired,
		ResponseChecksumValidation: aws.ResponseChecksumValidationWhenRequired,
	})
	return &Storage{client: client, bucket: cfg.Bucket, region: cfg.Region}
}

// 只为新建的 bucket 设置访问策略：匿名请求只能读取 link/ 下的对象，不能列出对象。
// 设置访问策略失败时删除刚创建的 bucket，否则下一次检查会把这个没有策略的 bucket 当作已经配置好的。
func (s *Storage) EnsureBucket(ctx context.Context) (created bool, err error) {
	_, err = s.client.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String(s.bucket)})
	if err == nil {
		return false, nil
	}
	if !notFound(err) {
		return false, fmt.Errorf("检查 bucket %s：%w", s.bucket, err)
	}
	input := &s3.CreateBucketInput{Bucket: aws.String(s.bucket)}
	if s.region != defaultRegion {
		input.CreateBucketConfiguration = &types.CreateBucketConfiguration{LocationConstraint: types.BucketLocationConstraint(s.region)}
	}
	if _, err := s.client.CreateBucket(ctx, input); err != nil {
		return false, fmt.Errorf("创建 bucket %s：%w", s.bucket, err)
	}
	_, err = s.client.PutBucketPolicy(ctx, &s3.PutBucketPolicyInput{Bucket: aws.String(s.bucket), Policy: aws.String(s.policy())})
	if err == nil {
		return true, nil
	}
	err = fmt.Errorf("设置 bucket %s 的访问策略：%w", s.bucket, err)
	// 迁移被取消或超时往往正是设置访问策略失败的原因，回滚不能随 ctx 一起失败。
	rollback, cancel := context.WithTimeout(context.WithoutCancel(ctx), rollbackTimeout)
	defer cancel()
	if _, deleteErr := s.client.DeleteBucket(rollback, &s3.DeleteBucketInput{Bucket: aws.String(s.bucket)}); deleteErr != nil {
		err = errors.Join(err, fmt.Errorf("删除没有访问策略的 bucket %s：%w", s.bucket, deleteErr))
	}
	return false, err
}

type (
	policy struct {
		Version   string
		Statement []statement
	}
	statement struct {
		Effect    string
		Principal principal
		Action    []string
		Resource  []string
	}
	principal struct {
		AWS []string
	}
)

func (s *Storage) policy() string {
	b, _ := json.Marshal(policy{
		Version: "2012-10-17",
		Statement: []statement{{
			Effect:    "Allow",
			Principal: principal{AWS: []string{"*"}},
			Action:    []string{"s3:GetObject"},
			Resource:  []string{"arn:aws:s3:::" + s.bucket + "/link/*"},
		}},
	})
	return string(b)
}

func (s *Storage) Put(ctx context.Context, key string, body io.ReadSeeker, contentType string) error {
	_, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(s.bucket),
		Key:         aws.String(key),
		Body:        body,
		ContentType: aws.String(contentType),
	})
	if err != nil {
		return fmt.Errorf("上传 %s：%w", key, err)
	}
	return nil
}

func (s *Storage) Exists(ctx context.Context, key string) (bool, error) {
	_, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key)})
	switch {
	case err == nil:
		return true, nil
	case notFound(err):
		return false, nil
	}
	return false, fmt.Errorf("检查 %s 是否存在：%w", key, err)
}

func (s *Storage) Delete(ctx context.Context, key string) error {
	if _, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key)}); err != nil {
		return fmt.Errorf("删除 %s：%w", key, err)
	}
	return nil
}

// HEAD 请求的 404 响应没有响应体，SDK 只能从状态码判断。
func notFound(err error) bool {
	var resp *awshttp.ResponseError
	return errors.As(err, &resp) && resp.HTTPStatusCode() == http.StatusNotFound
}
