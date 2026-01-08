package sessiontoken

import (
	"context"
	"errors"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/o1egl/paseto/v2"
	domainv1 "github.com/syss-io/executor/gen/proto/go/domain/v1"
	sessiontokenv1 "github.com/syss-io/executor/gen/proto/go/sessiontoken/v1"
	"github.com/syss-io/executor/gen/proto/go/sessiontoken/v1/sessiontokenv1connect"
	"github.com/syss-io/executor/internal/executor"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

type Service struct {
	DB        *gorm.DB
	PasetoKey []byte
}

var _ sessiontokenv1connect.SessionTokenServiceHandler = (*Service)(nil)
var _ executor.TokenValidator = (*Service)(nil)

type TokenClaims struct {
	TokenID         string   `json:"token_id"`
	APIKeyID        int64    `json:"api_key_id"`
	MaxUSDCents     uint32   `json:"max_usd_cents"`
	AllowedModels   []string `json:"allowed_models"`
	AllowedAssetIDs []string `json:"allowed_asset_ids"`
}

const tokenTTL = 5 * time.Minute

func (s *Service) MintSessionToken(ctx context.Context, req *connect.Request[sessiontokenv1.MintSessionTokenRequest]) (*connect.Response[sessiontokenv1.MintSessionTokenResponse], error) {
	apiKeyID, err := s.validateAPIKey(req.Header().Get("Authorization"))
	if err != nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, err)
	}

	tokenID := uuid.New().String()
	expiresAt := time.Now().Add(tokenTTL)

	claims := TokenClaims{
		TokenID:         tokenID,
		APIKeyID:        apiKeyID,
		MaxUSDCents:     req.Msg.MaxUsdCents,
		AllowedModels:   req.Msg.AllowedModels,
		AllowedAssetIDs: req.Msg.AllowedAssetIds,
	}

	jsonToken := paseto.JSONToken{
		Expiration: expiresAt,
	}
	jsonToken.Set("claims", claims)

	token, err := paseto.Encrypt(s.PasetoKey, jsonToken, nil)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	sessionToken := &domainv1.SessionTokenORM{
		TokenId:   tokenID,
		ApiKeyId:  apiKeyID,
		ExpiresAt: expiresAt.Unix(),
	}

	if err := s.DB.Create(sessionToken).Error; err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&sessiontokenv1.MintSessionTokenResponse{
		SessionToken: token,
		ExpiresAt:    expiresAt.Unix(),
	}), nil
}

func (s *Service) RevokeSessionToken(ctx context.Context, req *connect.Request[sessiontokenv1.RevokeSessionTokenRequest]) (*connect.Response[sessiontokenv1.RevokeSessionTokenResponse], error) {
	claims, err := s.decryptToken(req.Msg.SessionToken)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	now := time.Now().Unix()
	result := s.DB.Model(&domainv1.SessionTokenORM{}).
		Where("token_id = ? AND revoked_at IS NULL", claims.TokenID).
		Update("revoked_at", now)

	if result.Error != nil {
		return nil, connect.NewError(connect.CodeInternal, result.Error)
	}

	if result.RowsAffected == 0 {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("token not found or already revoked"))
	}

	return connect.NewResponse(&sessiontokenv1.RevokeSessionTokenResponse{}), nil
}

func (s *Service) ValidateSessionToken(ctx context.Context, req *connect.Request[sessiontokenv1.ValidateSessionTokenRequest]) (*connect.Response[sessiontokenv1.ValidateSessionTokenResponse], error) {
	claims, err := s.decryptToken(req.Msg.SessionToken)
	if err != nil {
		return connect.NewResponse(&sessiontokenv1.ValidateSessionTokenResponse{
			Valid: false,
		}), nil
	}

	var token domainv1.SessionTokenORM
	if err := s.DB.Where("token_id = ?", claims.TokenID).First(&token).Error; err != nil {
		return connect.NewResponse(&sessiontokenv1.ValidateSessionTokenResponse{
			Valid: false,
		}), nil
	}

	if token.RevokedAt != nil {
		return connect.NewResponse(&sessiontokenv1.ValidateSessionTokenResponse{
			Valid: false,
		}), nil
	}

	if time.Now().Unix() > token.ExpiresAt {
		return connect.NewResponse(&sessiontokenv1.ValidateSessionTokenResponse{
			Valid: false,
		}), nil
	}

	return connect.NewResponse(&sessiontokenv1.ValidateSessionTokenResponse{
		Valid:           true,
		ExpiresAt:       token.ExpiresAt,
		MaxUsdCents:     claims.MaxUSDCents,
		AllowedModels:   claims.AllowedModels,
		AllowedAssetIds: claims.AllowedAssetIDs,
	}), nil
}

// ValidateToken validates a session token and returns its claims.
// This implements the executor.TokenValidator interface for local validation.
func (s *Service) ValidateToken(token string) (*executor.TokenValidationResult, error) {
	claims, err := s.decryptToken(token)
	if err != nil {
		return &executor.TokenValidationResult{Valid: false}, errors.New("invalid session token")
	}

	var tokenRecord domainv1.SessionTokenORM
	if err := s.DB.Where("token_id = ?", claims.TokenID).First(&tokenRecord).Error; err != nil {
		return &executor.TokenValidationResult{Valid: false}, errors.New("session token not found")
	}

	if tokenRecord.RevokedAt != nil {
		return &executor.TokenValidationResult{Valid: false}, errors.New("session token revoked")
	}

	if time.Now().Unix() > tokenRecord.ExpiresAt {
		return &executor.TokenValidationResult{Valid: false}, errors.New("session token expired")
	}

	return &executor.TokenValidationResult{
		Valid:           true,
		MaxUSDCents:     claims.MaxUSDCents,
		AllowedModels:   claims.AllowedModels,
		AllowedAssetIDs: claims.AllowedAssetIDs,
	}, nil
}

func (s *Service) validateAPIKey(authHeader string) (int64, error) {
	if authHeader == "" {
		return 0, errors.New("missing authorization header")
	}

	parts := strings.SplitN(authHeader, " ", 2)
	if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
		return 0, errors.New("invalid authorization header format")
	}

	plainKey := parts[1]

	var keys []domainv1.APIKeyORM
	if err := s.DB.Where("deleted_at IS NULL").Find(&keys).Error; err != nil {
		return 0, err
	}

	for _, key := range keys {
		if err := bcrypt.CompareHashAndPassword([]byte(key.KeyHash), []byte(plainKey)); err == nil {
			return key.KeyId, nil
		}
	}

	return 0, errors.New("invalid api key")
}

func (s *Service) decryptToken(token string) (*TokenClaims, error) {
	var jsonToken paseto.JSONToken
	if err := paseto.Decrypt(token, s.PasetoKey, &jsonToken, nil); err != nil {
		return nil, err
	}

	var claims TokenClaims
	if err := jsonToken.Get("claims", &claims); err != nil {
		return nil, err
	}

	return &claims, nil
}
