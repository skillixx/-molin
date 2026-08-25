package image

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/netip"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

const maxMinIOReadBytes = int64(64 << 20)

type MinIOObjectStoreConfig struct {
	Endpoint               string
	PublicDownloadEndpoint string
	AccessKey              string
	SecretKey              string
	UseSSL                 bool
	Region                 string
	Buckets                []string
}

type MinIOObjectStore struct {
	client               *minio.Client
	downloadSigner       *minio.Client
	publicDownloadHost   string
	publicDownloadSecure bool
	buckets              map[string]struct{}
}

// NewMinIOObjectStore 使用静态受限凭据构造私有对象存储；构造阶段不创建bucket也不输出凭据。
func NewMinIOObjectStore(config MinIOObjectStoreConfig) (*MinIOObjectStore, error) {
	endpoint := strings.TrimSpace(config.Endpoint)
	accessKey := strings.TrimSpace(config.AccessKey)
	secretKey := strings.TrimSpace(config.SecretKey)
	if endpoint == "" || accessKey == "" || secretKey == "" || endpoint != config.Endpoint || accessKey != config.AccessKey || secretKey != config.SecretKey ||
		strings.Contains(endpoint, "://") || strings.ContainsAny(endpoint, "@/\\ \t\r\n") || len(config.Buckets) == 0 {
		return nil, ErrObjectInvalid
	}
	publicHost, publicSecure, err := parsePublicDownloadEndpoint(config.PublicDownloadEndpoint)
	if err != nil {
		return nil, err
	}
	// 显式地区避免签名客户端为查询bucket地区而访问浏览器入口；MinIO未配置地区时使用S3默认值。
	region := strings.TrimSpace(config.Region)
	if region == "" {
		region = "us-east-1"
	}
	client, err := minio.New(endpoint, &minio.Options{Creds: credentials.NewStaticV4(accessKey, secretKey, ""), Secure: config.UseSSL, Region: region})
	if err != nil {
		return nil, ErrObjectInvalid
	}
	downloadSigner, err := minio.New(publicHost, &minio.Options{Creds: credentials.NewStaticV4(accessKey, secretKey, ""), Secure: publicSecure, Region: region})
	if err != nil {
		return nil, ErrObjectInvalid
	}
	allowed := make(map[string]struct{}, len(config.Buckets))
	for _, bucket := range config.Buckets {
		bucket = strings.TrimSpace(bucket)
		if bucket == "" || strings.ContainsAny(bucket, "/\\") {
			return nil, ErrObjectInvalid
		}
		if _, duplicate := allowed[bucket]; duplicate {
			return nil, ErrObjectInvalid
		}
		allowed[bucket] = struct{}{}
	}
	return &MinIOObjectStore{
		client: client, downloadSigner: downloadSigner,
		publicDownloadHost: publicHost, publicDownloadSecure: publicSecure, buckets: allowed,
	}, nil
}

// parsePublicDownloadEndpoint 将内部连接地址与浏览器下载入口彻底分离；明文HTTP只允许回环测试地址。
func parsePublicDownloadEndpoint(raw string) (string, bool, error) {
	if raw == "" || strings.TrimSpace(raw) != raw {
		return "", false, ErrObjectInvalid
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Opaque != "" || parsed.User != nil || parsed.Hostname() == "" || parsed.RawQuery != "" || parsed.Fragment != "" ||
		(parsed.EscapedPath() != "" && parsed.EscapedPath() != "/") {
		return "", false, ErrObjectInvalid
	}
	if port := parsed.Port(); port != "" {
		value, portErr := strconv.ParseUint(port, 10, 16)
		if portErr != nil || value == 0 {
			return "", false, ErrObjectInvalid
		}
	}
	hostname := strings.ToLower(parsed.Hostname())
	switch strings.ToLower(parsed.Scheme) {
	case "https":
		if !validPublicDownloadHost(hostname) {
			return "", false, ErrObjectInvalid
		}
		return parsed.Host, true, nil
	case "http":
		if !localTestDownloadHost(hostname) {
			return "", false, ErrObjectInvalid
		}
		return parsed.Host, false, nil
	default:
		return "", false, ErrObjectInvalid
	}
}

func validPublicDownloadHost(host string) bool {
	if address, err := netip.ParseAddr(host); err == nil {
		return allowedPublicIP(address)
	}
	// 单标签Docker服务名不是浏览器公开入口，必须在构造期失败关闭。
	return validAllowedDNSHost(host)
}

func localTestDownloadHost(host string) bool {
	if host == "localhost" {
		return true
	}
	address, err := netip.ParseAddr(host)
	return err == nil && address.IsLoopback()
}

// EnsureBuckets 幂等创建图片bucket并清除匿名策略，保证对象只能通过受控凭据或短效签名读取。
func (s *MinIOObjectStore) EnsureBuckets(ctx context.Context) error {
	if s == nil || s.client == nil {
		return ErrObjectInvalid
	}
	for bucket := range s.buckets {
		exists, err := s.client.BucketExists(ctx, bucket)
		if err != nil {
			return err
		}
		if !exists {
			if err := s.client.MakeBucket(ctx, bucket, minio.MakeBucketOptions{}); err != nil {
				return err
			}
		}
		if err := s.client.SetBucketPolicy(ctx, bucket, ""); err != nil {
			return err
		}
	}
	return nil
}

// VerifyBuckets 供运行时最小权限账号只读确认bucket存在，不要求创建bucket或修改policy的管理权限。
func (s *MinIOObjectStore) VerifyBuckets(ctx context.Context) error {
	if s == nil || s.client == nil {
		return ErrObjectInvalid
	}
	for bucket := range s.buckets {
		exists, err := s.client.BucketExists(ctx, bucket)
		if err != nil || !exists {
			return ErrObjectNotFound
		}
	}
	return nil
}

func (s *MinIOObjectStore) Put(ctx context.Context, ref ObjectRef, body io.Reader, maxBytes int64) (StoredObject, error) {
	if !s.allowedRef(ref) || body == nil || maxBytes <= 0 {
		return StoredObject{}, ErrObjectInvalid
	}
	raw, err := io.ReadAll(io.LimitReader(body, maxBytes+1))
	if err != nil {
		return StoredObject{}, err
	}
	if int64(len(raw)) > maxBytes {
		return StoredObject{}, ErrObjectTooLarge
	}
	sum := sha256.Sum256(raw)
	digest := hex.EncodeToString(sum[:])
	if existing, headErr := s.Head(ctx, ref); headErr == nil {
		if existing.SHA256 == digest && existing.SizeBytes == uint64(len(raw)) {
			return existing, nil
		}
		return StoredObject{}, ErrObjectConflict
	} else if !errors.Is(headErr, ErrObjectNotFound) {
		return StoredObject{}, headErr
	}
	contentType := "application/octet-stream"
	if strings.EqualFold(filepath.Ext(ref.Key), ".png") {
		contentType = "image/png"
	}
	info, err := s.client.PutObject(ctx, ref.Bucket, ref.Key, bytes.NewReader(raw), int64(len(raw)), minio.PutObjectOptions{
		ContentType: contentType, UserMetadata: map[string]string{"sha256": digest},
	})
	if err != nil {
		return StoredObject{}, err
	}
	return StoredObject{Ref: ref, SizeBytes: uint64(info.Size), SHA256: digest, CreatedAt: time.Now().UTC()}, nil
}

func (s *MinIOObjectStore) Get(ctx context.Context, ref ObjectRef) ([]byte, error) {
	if !s.allowedRef(ref) {
		return nil, ErrObjectInvalid
	}
	object, err := s.client.GetObject(ctx, ref.Bucket, ref.Key, minio.GetObjectOptions{})
	if err != nil {
		return nil, err
	}
	defer object.Close()
	raw, err := io.ReadAll(io.LimitReader(object, maxMinIOReadBytes+1))
	if err != nil {
		return nil, normalizeMinIOError(err)
	}
	if int64(len(raw)) > maxMinIOReadBytes {
		return nil, ErrObjectTooLarge
	}
	return raw, nil
}

func (s *MinIOObjectStore) Head(ctx context.Context, ref ObjectRef) (StoredObject, error) {
	if !s.allowedRef(ref) {
		return StoredObject{}, ErrObjectInvalid
	}
	info, err := s.client.StatObject(ctx, ref.Bucket, ref.Key, minio.StatObjectOptions{})
	if err != nil {
		return StoredObject{}, normalizeMinIOError(err)
	}
	digest := info.Metadata.Get("X-Amz-Meta-Sha256")
	if len(digest) != 64 {
		return StoredObject{}, ErrObjectConflict
	}
	return StoredObject{Ref: ref, SizeBytes: uint64(info.Size), SHA256: digest, CreatedAt: info.LastModified.UTC()}, nil
}

func (s *MinIOObjectStore) Delete(ctx context.Context, ref ObjectRef) error {
	if !s.allowedRef(ref) {
		return ErrObjectInvalid
	}
	return s.client.RemoveObject(ctx, ref.Bucket, ref.Key, minio.RemoveObjectOptions{})
}

func (s *MinIOObjectStore) SignDownload(ctx context.Context, ref ObjectRef, expiresIn time.Duration) (string, error) {
	if !s.allowedRef(ref) || expiresIn <= 0 || expiresIn > imageDownloadTTLInternal {
		return "", ErrObjectInvalid
	}
	if _, err := s.Head(ctx, ref); err != nil {
		return "", err
	}
	signed, err := s.downloadSigner.PresignedGetObject(ctx, ref.Bucket, ref.Key, expiresIn, url.Values{})
	if err != nil {
		return "", err
	}
	wantScheme := "http"
	if s.publicDownloadSecure {
		wantScheme = "https"
	}
	if signed.Scheme != wantScheme || !strings.EqualFold(signed.Host, s.publicDownloadHost) {
		return "", ErrObjectInvalid
	}
	return signed.String(), nil
}

const imageDownloadTTLInternal = 15 * time.Minute

func (s *MinIOObjectStore) allowedRef(ref ObjectRef) bool {
	if s == nil || s.client == nil || !validObjectRef(ref) {
		return false
	}
	_, ok := s.buckets[ref.Bucket]
	return ok
}

func normalizeMinIOError(err error) error {
	response := minio.ToErrorResponse(err)
	if response.Code == "NoSuchKey" || response.Code == "NoSuchObject" || response.Code == "NoSuchBucket" {
		return ErrObjectNotFound
	}
	return err
}

var _ ObjectStore = (*MinIOObjectStore)(nil)
