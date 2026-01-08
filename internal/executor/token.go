package executor

// TokenValidator validates session tokens and returns their claims.
type TokenValidator interface {
	ValidateToken(token string) (*TokenValidationResult, error)
}

// TokenValidationResult contains the validated token claims.
type TokenValidationResult struct {
	Valid           bool
	MaxUSDCents     uint32
	AllowedModels   []string
	AllowedAssetIDs []string
}
