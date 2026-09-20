package api

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/MicahParks/keyfunc/v3"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"wiminder/internal/config"
	"wiminder/internal/store"
)

type stubProvisioner struct{ users map[string]store.User }

func (s *stubProvisioner) GetOrCreateUser(_ context.Context, email, name string, admins map[string]bool) (store.User, error) {
	u, ok := s.users[email]
	if ok {
		return u, nil
	}
	role := "member"
	if admins[email] {
		role = "admin"
	}
	u = store.User{ID: uuid.NewString(), Email: email, Name: name, Role: role}
	s.users[email] = u
	return u, nil
}

func makeJWKS(t *testing.T, key *rsa.PrivateKey) (jwt.Keyfunc, *httptest.Server) {
	t.Helper()
	pub := key.PublicKey
	jwks := map[string]any{"keys": []map[string]any{{
		"kty": "RSA", "alg": "RS256", "use": "sig", "kid": "test-key",
		"n": base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
		"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes()),
	}}}
	b, _ := json.Marshal(jwks)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(b) }))
	t.Cleanup(srv.Close)
	// keyfunc v3.8.2: NewRemote/NewRemoteConfig was removed — the equivalents are
	// NewDefaultOverrideCtx (single URL) + KeyfuncCtx for jwt.Keyfunc.
	kfi, err := keyfunc.NewDefaultOverrideCtx(context.Background(), []string{srv.URL}, keyfunc.Override{Client: srv.Client()})
	if err != nil {
		t.Fatal(err)
	}
	return kfi.KeyfuncCtx(context.Background()), srv
}

func signToken(t *testing.T, key *rsa.PrivateKey, aud, email string, exp time.Time) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"aud": []string{aud}, "email": email, "name": email, "exp": exp.Unix(),
	})
	s, err := tok.SignedString(key)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestCFAccessMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	kf, _ := makeJWKS(t, key)
	prov := &stubProvisioner{users: map[string]store.User{}}
	mw := NewCFAccessFromKeyfunc(kf, "aud-1", prov, map[string]bool{"admin@x.id": true})

	r := gin.New()
	r.GET("/who", mw, func(c *gin.Context) {
		u := c.MustGet("user").(store.User)
		c.JSON(200, gin.H{"email": u.Email, "role": u.Role})
	})

	// no header → 401
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/who", nil))
	if w.Code != 401 {
		t.Errorf("no jwt: %d", w.Code)
	}

	// valid token → provisions admin
	tok := signToken(t, key, "aud-1", "admin@x.id", time.Now().Add(time.Hour))
	req := httptest.NewRequest("GET", "/who", nil)
	req.Header.Set("Cf-Access-Jwt-Assertion", tok)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"role":"admin"`) {
		t.Errorf("valid jwt: %d %s", w.Code, w.Body.String())
	}

	// wrong aud → 401
	tokBad := signToken(t, key, "aud-2", "admin@x.id", time.Now().Add(time.Hour))
	req = httptest.NewRequest("GET", "/who", nil)
	req.Header.Set("Cf-Access-Jwt-Assertion", tokBad)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 401 {
		t.Errorf("wrong aud: %d", w.Code)
	}

	// expired → 401
	tokExp := signToken(t, key, "aud-1", "admin@x.id", time.Now().Add(-time.Hour))
	req = httptest.NewRequest("GET", "/who", nil)
	req.Header.Set("Cf-Access-Jwt-Assertion", tokExp)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 401 {
		t.Errorf("expired: %d", w.Code)
	}
}

func TestDevAuth(t *testing.T) {
	gin.SetMode(gin.TestMode)
	prov := &stubProvisioner{users: map[string]store.User{}}
	mw := devAuthMiddleware(prov, map[string]bool{})
	r := gin.New()
	r.GET("/who", mw, func(c *gin.Context) {
		u := c.MustGet("user").(store.User)
		c.JSON(200, gin.H{"email": u.Email})
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/who", nil))
	if w.Code != 401 {
		t.Errorf("without header: %d", w.Code)
	}
	req := httptest.NewRequest("GET", "/who", nil)
	req.Header.Set("X-Dev-Email", "dev@x.id")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Errorf("with header: %d %s", w.Code, w.Body.String())
	}
}

func TestNewCFAccessInitFails(t *testing.T) {
	gin.SetMode(gin.TestMode)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	cfg := config.Config{
		CFTeamDomain: strings.TrimPrefix(srv.URL, "http://"),
		CFAud:        "aud-x",
		AdminEmails:  map[string]bool{},
	}
	if _, err := NewCFAccess(context.Background(), cfg, &stubProvisioner{users: map[string]store.User{}}); err == nil {
		t.Fatal("initial JWKS fetch fails, NewCFAccess must return an error")
	}
}
