package identity

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"time"

	"connectrpc.com/connect"
	domainv1 "github.com/syss-io/executor/gen/proto/go/domain/v1"
	identityv1 "github.com/syss-io/executor/gen/proto/go/identity/v1"
	"github.com/syss-io/executor/gen/proto/go/identity/v1/identityv1connect"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

type Service struct {
	DB *gorm.DB
}

var _ identityv1connect.IdentityHandler = (*Service)(nil)

func (s *Service) CreateAPIKey(ctx context.Context, req *connect.Request[identityv1.CreateAPIKeyRequest]) (*connect.Response[identityv1.CreateAPIKeyResponse], error) {
	plainKey, err := generateAPIKey(32)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(plainKey), bcrypt.DefaultCost)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	apiKey := &domainv1.APIKeyORM{
		Name:      req.Msg.Name,
		KeyHash:   string(hash),
		CreatedAt: time.Now().Unix(),
	}

	if err := s.DB.Create(apiKey).Error; err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&identityv1.CreateAPIKeyResponse{
		ApiKey: &domainv1.APIKey{
			KeyId:     apiKey.KeyId,
			Name:      apiKey.Name,
			CreatedAt: apiKey.CreatedAt,
		},
		PlainKey: plainKey,
	}), nil
}

func (s *Service) ListAPIKeys(ctx context.Context, req *connect.Request[identityv1.ListAPIKeysRequest]) (*connect.Response[identityv1.ListAPIKeysResponse], error) {
	var keys []domainv1.APIKeyORM
	if err := s.DB.Where("deleted_at IS NULL").Find(&keys).Error; err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	apiKeys := make([]*domainv1.APIKey, len(keys))
	for i, k := range keys {
		apiKeys[i] = &domainv1.APIKey{
			KeyId:     k.KeyId,
			Name:      k.Name,
			CreatedAt: k.CreatedAt,
		}
	}

	return connect.NewResponse(&identityv1.ListAPIKeysResponse{
		ApiKeys: apiKeys,
	}), nil
}

func (s *Service) RemoveAPIKey(ctx context.Context, req *connect.Request[identityv1.RemoveAPIKeyRequest]) (*connect.Response[identityv1.RemoveAPIKeyResponse], error) {
	now := time.Now().Unix()
	result := s.DB.Model(&domainv1.APIKeyORM{}).
		Where("key_id = ? AND deleted_at IS NULL", req.Msg.KeyId).
		Update("deleted_at", now)

	if result.Error != nil {
		return nil, connect.NewError(connect.CodeInternal, result.Error)
	}

	if result.RowsAffected == 0 {
		return nil, connect.NewError(connect.CodeNotFound, nil)
	}

	return connect.NewResponse(&identityv1.RemoveAPIKeyResponse{}), nil
}

func generateAPIKey(length int) (string, error) {
	bytes := make([]byte, length)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return base64.URLEncoding.EncodeToString(bytes), nil
}
