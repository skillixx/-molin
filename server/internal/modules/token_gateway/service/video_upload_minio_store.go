package service

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

type MinIOVideoUploadStoreConfig struct {
	Endpoint, PublicUploadEndpoint string
	AccessKey, SecretKey, Region   string
	UseSSL                         bool
	SourceBucket, NormalizedBucket string
}

// MinIOVideoUploadStore把短效PUT、封存、规范化副本和清理墓碑限制在服务端冻结目标。
type MinIOVideoUploadStore struct {
	client                         *minio.Client
	uploadSigner                   *minio.Client
	sourceBucket, normalizedBucket string
}

func NewMinIOVideoUploadStore(config MinIOVideoUploadStoreConfig) (*MinIOVideoUploadStore, error) {
	endpoint, access, secret := strings.TrimSpace(config.Endpoint), strings.TrimSpace(config.AccessKey), strings.TrimSpace(config.SecretKey)
	if endpoint == "" || endpoint != config.Endpoint || access == "" || access != config.AccessKey || secret == "" || secret != config.SecretKey || access == secret || strings.Contains(endpoint, "://") || strings.ContainsAny(endpoint, "@/\\ \t\r\n") || !videoBillingPublicID.MatchString(config.SourceBucket) || !videoBillingPublicID.MatchString(config.NormalizedBucket) || config.SourceBucket == config.NormalizedBucket {
		return nil, ErrVideoUploadUnavailable
	}
	publicHost, publicSecure, err := parseVideoUploadEndpoint(config.PublicUploadEndpoint)
	if err != nil {
		return nil, err
	}
	region := strings.TrimSpace(config.Region)
	if region == "" {
		region = "us-east-1"
	}
	client, err := minio.New(endpoint, &minio.Options{Creds: credentials.NewStaticV4(access, secret, ""), Secure: config.UseSSL, Region: region})
	if err != nil {
		return nil, ErrVideoUploadUnavailable
	}
	signer, err := minio.New(publicHost, &minio.Options{Creds: credentials.NewStaticV4(access, secret, ""), Secure: publicSecure, Region: region})
	if err != nil {
		return nil, ErrVideoUploadUnavailable
	}
	return &MinIOVideoUploadStore{client: client, uploadSigner: signer, sourceBucket: config.SourceBucket, normalizedBucket: config.NormalizedBucket}, nil
}

func (s *MinIOVideoUploadStore) EnsureBuckets(ctx context.Context) error {
	if s == nil || s.client == nil {
		return ErrVideoUploadUnavailable
	}
	for _, bucket := range []string{s.sourceBucket, s.normalizedBucket} {
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

func (s *MinIOVideoUploadStore) VerifyBuckets(ctx context.Context) error {
	if s == nil || s.client == nil {
		return ErrVideoUploadUnavailable
	}
	for _, bucket := range []string{s.sourceBucket, s.normalizedBucket} {
		exists, err := s.client.BucketExists(ctx, bucket)
		if err != nil || !exists {
			return ErrVideoUploadUnavailable
		}
		policy, err := s.client.GetBucketPolicy(ctx, bucket)
		if err != nil {
			response := minio.ToErrorResponse(err)
			if response.Code != "NoSuchBucketPolicy" && response.Code != "NoSuchPolicy" {
				return ErrVideoUploadUnavailable
			}
		} else if strings.TrimSpace(policy) != "" {
			return ErrVideoUploadUnavailable
		}
	}
	return nil
}

func (s *MinIOVideoUploadStore) Issue(ctx context.Context, target VideoUploadTarget) (*VideoUploadGrant, error) {
	if !s.validTarget(target) || target.SourceType != "platform_presigned" {
		return nil, ErrVideoUploadConflict
	}
	duration := time.Until(target.UploadExpiresAt)
	if duration < time.Second || duration > 15*time.Minute+time.Second {
		return nil, ErrVideoUploadConflict
	}
	headers := s.uploadHeaders(target)
	signed, err := s.uploadSigner.PresignHeader(ctx, http.MethodPut, target.SourceBucket, target.SourceKey, duration, nil, headers)
	if err != nil {
		return nil, ErrVideoUploadUnavailable
	}
	return &VideoUploadGrant{Method: http.MethodPut, URL: signed.String(), Headers: flattenVideoUploadHeaders(headers), ExpiresAt: target.UploadExpiresAt}, nil
}

func (s *MinIOVideoUploadStore) Seal(ctx context.Context, target VideoUploadTarget, maxBytes int64) (*VideoSealedUpload, error) {
	if !s.validTarget(target) || maxBytes <= 0 || target.SizeBytes > uint64(maxBytes) {
		return nil, ErrVideoUploadConflict
	}
	info, err := s.client.StatObject(ctx, target.SourceBucket, target.SourceKey, minio.StatObjectOptions{})
	if err != nil {
		return nil, ErrVideoUploadUnavailable
	}
	if info.Size != int64(target.SizeBytes) || info.ContentType != target.MIMEType || !videoUploadMetadataMatches(info, target) || info.Metadata.Get("X-Amz-Meta-Molin-Discarded") != "" {
		return nil, ErrVideoUploadConflict
	}
	object, err := s.client.GetObject(ctx, target.SourceBucket, target.SourceKey, minio.GetObjectOptions{})
	if err != nil {
		return nil, ErrVideoUploadUnavailable
	}
	defer object.Close()
	raw, err := io.ReadAll(io.LimitReader(object, maxBytes+1))
	if err != nil || len(raw) == 0 || int64(len(raw)) > maxBytes || uint64(len(raw)) != target.SizeBytes || videoPayloadSHA256(raw) != target.ExpectedSHA256 {
		return nil, ErrVideoUploadConflict
	}
	return &VideoSealedUpload{Bytes: raw, MIMEType: info.ContentType, ETag: strings.Trim(info.ETag, `"`), VersionID: info.VersionID}, nil
}

func (s *MinIOVideoUploadStore) PutOriginal(ctx context.Context, target VideoUploadTarget, body io.Reader, size uint64, digest string) error {
	if !s.validTarget(target) || target.SourceType != "openai_inline_multipart" || body == nil || size != target.SizeBytes || digest != target.ExpectedSHA256 {
		return ErrVideoUploadConflict
	}
	// 已存在对象也必须先验证本次正文，不能只凭调用方声明的hash把漂移内容当成幂等重放。
	raw, err := io.ReadAll(io.LimitReader(body, int64(size)+1))
	if err != nil || uint64(len(raw)) != size || videoPayloadSHA256(raw) != digest {
		return ErrVideoUploadConflict
	}
	return s.putImmutable(ctx, target.SourceBucket, target.SourceKey, bytes.NewReader(raw), size, digest, target.MIMEType, target.SessionID)
}

func (s *MinIOVideoUploadStore) PutNormalized(ctx context.Context, target VideoUploadTarget, raw []byte, digest string) error {
	if !s.validTarget(target) || len(raw) == 0 || int64(len(raw)) > videoUploadMaxBytes || videoPayloadSHA256(raw) != digest {
		return ErrVideoUploadConflict
	}
	return s.putImmutable(ctx, target.NormalizedBucket, target.NormalizedKey, bytes.NewReader(raw), uint64(len(raw)), digest, "image/png", target.SessionID)
}

func (s *MinIOVideoUploadStore) ReadNormalized(ctx context.Context, bucket, key string, maxBytes int64) ([]byte, error) {
	if s == nil || bucket != s.normalizedBucket || !validVideoNormalizedKey(key) || maxBytes <= 0 || maxBytes > videoUploadMaxBytes {
		return nil, ErrVideoUploadUnavailable
	}
	info, err := s.client.StatObject(ctx, bucket, key, minio.StatObjectOptions{})
	if err != nil || info.Size <= 0 || info.Size > maxBytes || info.ContentType != "image/png" || !lowerHex64.MatchString(strings.ToLower(info.Metadata.Get("X-Amz-Meta-Sha256"))) {
		return nil, ErrVideoUploadUnavailable
	}
	object, err := s.client.GetObject(ctx, bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, ErrVideoUploadUnavailable
	}
	defer object.Close()
	raw, err := io.ReadAll(io.LimitReader(object, maxBytes+1))
	digest := strings.ToLower(info.Metadata.Get("X-Amz-Meta-Sha256"))
	if err != nil || len(raw) == 0 || int64(len(raw)) != info.Size || videoPayloadSHA256(raw) != digest {
		return nil, ErrVideoUploadUnavailable
	}
	return raw, nil
}

func (s *MinIOVideoUploadStore) Discard(ctx context.Context, target VideoUploadTarget) error {
	if !s.validTarget(target) {
		return ErrVideoUploadConflict
	}
	// 先写原键墓碑再删规范化副本；旧URL带If-None-Match，墓碑存在后永远不能复活正文。
	if err := s.putDiscardTombstone(ctx, target); err != nil {
		return err
	}
	if err := s.client.RemoveObject(ctx, target.NormalizedBucket, target.NormalizedKey, minio.RemoveObjectOptions{}); err != nil {
		return ErrVideoUploadUnavailable
	}
	return nil
}

func (s *MinIOVideoUploadStore) SupportsSynchronousDeletion() bool { return true }

func (s *MinIOVideoUploadStore) VerifyDiscarded(ctx context.Context, target VideoUploadTarget) (bool, error) {
	if !s.validTarget(target) {
		return false, ErrVideoUploadConflict
	}
	info, err := s.client.StatObject(ctx, target.SourceBucket, target.SourceKey, minio.StatObjectOptions{})
	if err != nil || info.Size != 1 || info.Metadata.Get("X-Amz-Meta-Molin-Discarded") != "1" || info.Metadata.Get("X-Amz-Meta-Molin-Session") != target.SessionID {
		return false, ErrVideoUploadUnavailable
	}
	_, normalizedErr := s.client.StatObject(ctx, target.NormalizedBucket, target.NormalizedKey, minio.StatObjectOptions{})
	if normalizedErr == nil {
		return false, nil
	}
	response := minio.ToErrorResponse(normalizedErr)
	if response.Code != "NoSuchKey" && response.Code != "NoSuchObject" && response.Code != "NotFound" {
		return false, ErrVideoUploadUnavailable
	}
	return true, nil
}

func (s *MinIOVideoUploadStore) putDiscardTombstone(ctx context.Context, target VideoUploadTarget) error {
	for attempt := 0; attempt < 3; attempt++ {
		options := minio.PutObjectOptions{ContentType: "application/x-molin-discarded", UserMetadata: map[string]string{"molin-discarded": "1", "molin-session": target.SessionID}}
		info, err := s.client.StatObject(ctx, target.SourceBucket, target.SourceKey, minio.StatObjectOptions{})
		if err == nil {
			if info.Size == 1 && info.Metadata.Get("X-Amz-Meta-Molin-Discarded") == "1" && info.Metadata.Get("X-Amz-Meta-Molin-Session") == target.SessionID {
				return nil
			}
			options.SetMatchETag(strings.Trim(info.ETag, `"`))
		} else {
			response := minio.ToErrorResponse(err)
			if response.Code != "NoSuchKey" && response.Code != "NoSuchObject" && response.Code != "NotFound" {
				return ErrVideoUploadUnavailable
			}
			options.SetMatchETagExcept("*")
		}
		if _, err := s.client.PutObject(ctx, target.SourceBucket, target.SourceKey, bytes.NewReader([]byte{0}), 1, options); err == nil {
			return nil
		}
	}
	return ErrVideoUploadUnavailable
}

func (s *MinIOVideoUploadStore) putImmutable(ctx context.Context, bucket, key string, body io.Reader, size uint64, digest, mimeType, sessionID string) error {
	if size == 0 || size > uint64(videoUploadMaxBytes) || !lowerHex64.MatchString(digest) || body == nil {
		return ErrVideoUploadConflict
	}
	if info, err := s.client.StatObject(ctx, bucket, key, minio.StatObjectOptions{}); err == nil {
		if uint64(info.Size) == size && strings.ToLower(info.Metadata.Get("X-Amz-Meta-Sha256")) == digest && info.Metadata.Get("X-Amz-Meta-Molin-Session") == sessionID {
			return nil
		}
		return ErrVideoUploadConflict
	}
	metadata := map[string]string{"sha256": digest, "molin-session": sessionID, "molin-size": strconv.FormatUint(size, 10)}
	options := minio.PutObjectOptions{ContentType: mimeType, UserMetadata: metadata}
	options.SetMatchETagExcept("*")
	if _, err := s.client.PutObject(ctx, bucket, key, body, int64(size), options); err != nil {
		if info, headErr := s.client.StatObject(ctx, bucket, key, minio.StatObjectOptions{}); headErr == nil && uint64(info.Size) == size && strings.ToLower(info.Metadata.Get("X-Amz-Meta-Sha256")) == digest && info.Metadata.Get("X-Amz-Meta-Molin-Session") == sessionID {
			return nil
		}
		return ErrVideoUploadConflict
	}
	return nil
}

func (s *MinIOVideoUploadStore) validTarget(target VideoUploadTarget) bool {
	if s == nil || s.client == nil || target.SourceBucket != s.sourceBucket || target.NormalizedBucket != s.normalizedBucket || target.UserID == 0 || target.ProjectID == 0 || target.SizeBytes == 0 || target.SizeBytes > uint64(videoUploadMaxBytes) || !videoBillingPublicID.MatchString(target.SessionID) || !videoBillingPublicID.MatchString(target.InputAssetID) || !lowerHex64.MatchString(target.ExpectedSHA256) || (target.MIMEType != "image/png" && target.MIMEType != "image/jpeg") {
		return false
	}
	prefix := "original"
	if target.SourceType == "openai_inline_multipart" {
		prefix = "inline"
	} else if target.SourceType != "platform_presigned" {
		return false
	}
	wantSource := fmt.Sprintf("%s/%d/%d/%s", prefix, target.UserID, target.ProjectID, target.SessionID)
	wantNormalized := fmt.Sprintf("normalized/%d/%d/%s.png", target.UserID, target.ProjectID, target.InputAssetID)
	return target.SourceKey == wantSource && target.NormalizedKey == wantNormalized
}

func (s *MinIOVideoUploadStore) uploadHeaders(target VideoUploadTarget) http.Header {
	headers := http.Header{}
	headers.Set("Content-Type", target.MIMEType)
	headers.Set("If-None-Match", "*")
	headers.Set("X-Amz-Meta-Sha256", target.ExpectedSHA256)
	headers.Set("X-Amz-Meta-Molin-Session", target.SessionID)
	headers.Set("X-Amz-Meta-Molin-Size", strconv.FormatUint(target.SizeBytes, 10))
	return headers
}

func videoUploadMetadataMatches(info minio.ObjectInfo, target VideoUploadTarget) bool {
	return strings.ToLower(info.Metadata.Get("X-Amz-Meta-Sha256")) == target.ExpectedSHA256 && info.Metadata.Get("X-Amz-Meta-Molin-Session") == target.SessionID && info.Metadata.Get("X-Amz-Meta-Molin-Size") == strconv.FormatUint(target.SizeBytes, 10)
}

func flattenVideoUploadHeaders(headers http.Header) map[string]string {
	result := make(map[string]string, len(headers))
	for key, values := range headers {
		if len(values) == 1 {
			result[key] = values[0]
		}
	}
	return result
}

func validVideoNormalizedKey(key string) bool {
	parts := strings.Split(key, "/")
	if len(parts) != 4 || parts[0] != "normalized" || !videoBillingPublicID.MatchString(parts[3]) || !strings.HasSuffix(parts[3], ".png") {
		return false
	}
	_, userErr := strconv.ParseUint(parts[1], 10, 64)
	_, projectErr := strconv.ParseUint(parts[2], 10, 64)
	return userErr == nil && projectErr == nil
}

func parseVideoUploadEndpoint(raw string) (string, bool, error) {
	if raw == "" || raw != strings.TrimSpace(raw) {
		return "", false, ErrVideoUploadUnavailable
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.User != nil || parsed.Hostname() == "" || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.EscapedPath() != "" && parsed.EscapedPath() != "/") {
		return "", false, ErrVideoUploadUnavailable
	}
	host := strings.ToLower(parsed.Hostname())
	if parsed.Scheme == "http" {
		address, err := netip.ParseAddr(host)
		if host != "localhost" && (err != nil || !address.IsLoopback()) {
			return "", false, ErrVideoUploadUnavailable
		}
		return parsed.Host, false, nil
	}
	if parsed.Scheme != "https" || strings.ContainsAny(host, "_ ") || (!strings.Contains(host, ".") && net.ParseIP(host) == nil) {
		return "", false, ErrVideoUploadUnavailable
	}
	return parsed.Host, true, nil
}

var _ VideoUploadStore = (*MinIOVideoUploadStore)(nil)
var _ VideoInlineUploadStore = (*MinIOVideoUploadStore)(nil)
