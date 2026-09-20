package api

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/MicahParks/keyfunc/v3"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"

	"wiminder/internal/config"
	"wiminder/internal/store"
)

type UserProvisioner interface {
	GetOrCreateUser(ctx context.Context, email, name string, adminEmails map[string]bool) (store.User, error)
}

// NewCFAccessFromKeyfunc verifies the Cf-Access-Jwt-Assertion header and provisions the user.
// The keyfunc is injected so it can be tested with a local JWKS.
func NewCFAccessFromKeyfunc(kf jwt.Keyfunc, aud string, provision UserProvisioner, adminEmails map[string]bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		raw := strings.TrimSpace(c.GetHeader("Cf-Access-Jwt-Assertion"))
		if raw == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing Cloudflare Access token"})
			return
		}
		parsed, err := jwt.Parse(raw, kf,
			jwt.WithValidMethods([]string{"RS256"}),
			jwt.WithAudience(aud),
			jwt.WithExpirationRequired(),
		)
		if err != nil || !parsed.Valid {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": fmt.Sprintf("invalid token: %v", err)})
			return
		}
		claims, ok := parsed.Claims.(jwt.MapClaims)
		if !ok {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unreadable claims"})
			return
		}
		email, _ := claims["email"].(string)
		name, _ := claims["name"].(string)
		if email == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "empty email claim — make sure the Access policy includes an email"})
			return
		}
		u, err := provision.GetOrCreateUser(c.Request.Context(), email, name, adminEmails)
		if err != nil {
			c.AbortWithStatusJSON(500, gin.H{"error": "failed to provision user"})
			return
		}
		c.Set("user", u)
		c.Next()
	}
}

// NewCFAccess builds the production JWKS keyfunc from the Access team domain.
func NewCFAccess(ctx context.Context, cfg config.Config, provision UserProvisioner) (gin.HandlerFunc, error) {
	jwksURL := fmt.Sprintf("https://%s/cdn-cgi/access/certs", cfg.CFTeamDomain)
	// keyfunc v3.8.2: NewRemote/NewRemoteConfig was removed — the equivalents are
	// NewDefaultOverrideCtx (single URL) + KeyfuncCtx for jwt.Keyfunc.
	noErrFirst := false // initial JWKS fetch failure = error (fail fast at init)
	kfi, err := keyfunc.NewDefaultOverrideCtx(ctx, []string{jwksURL}, keyfunc.Override{
		Client:                    &http.Client{Timeout: 10 * time.Second},
		RefreshInterval:           time.Hour,
		NoErrorReturnFirstHTTPReq: &noErrFirst,
	})
	if err != nil {
		return nil, err
	}
	return NewCFAccessFromKeyfunc(kfi.KeyfuncCtx(ctx), cfg.CFAud, provision, cfg.AdminEmails), nil
}

// devAuthMiddleware is ONLY for AUTH_MODE=dev.
func devAuthMiddleware(provision UserProvisioner, adminEmails map[string]bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		email := strings.ToLower(strings.TrimSpace(c.GetHeader("X-Dev-Email")))
		if email == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "dev mode: X-Dev-Email header required"})
			return
		}
		u, err := provision.GetOrCreateUser(c.Request.Context(), email, email, adminEmails)
		if err != nil {
			c.AbortWithStatusJSON(500, gin.H{"error": "failed to provision user"})
			return
		}
		c.Set("user", u)
		c.Next()
	}
}
