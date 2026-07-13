package zztgo

import (
	"context"
	"crypto"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	defaultGoogleAuthEndpoint  = "https://accounts.google.com/o/oauth2/v2/auth"
	defaultGoogleTokenEndpoint = "https://oauth2.googleapis.com/token"
	defaultGoogleJWKSURL       = "https://www.googleapis.com/oauth2/v3/certs"

	authSessionCookie = "zzt_auth"
	authStateCookie   = "zzt_oauth_state"
	authSessionMaxAge = 30 * 24 * time.Hour
	authStateMaxAge   = 10 * time.Minute
)

var (
	ErrAuthDisabled = errors.New("google auth is not configured")
	ErrInvalidAuth  = errors.New("invalid auth token")
)

type AuthenticatedAccount struct {
	ID    string `json:"id"`
	Email string `json:"email,omitempty"`
	Name  string `json:"name,omitempty"`
}

func (a AuthenticatedAccount) DisplayName() string {
	if strings.TrimSpace(a.Name) != "" {
		return strings.TrimSpace(a.Name)
	}
	if strings.TrimSpace(a.Email) != "" {
		return strings.TrimSpace(a.Email)
	}
	return a.ID
}

type IDTokenVerifier interface {
	VerifyIDToken(ctx context.Context, token, clientID string) (AuthenticatedAccount, error)
}

type AuthService struct {
	ClientID     string
	ClientSecret string
	RedirectURL  string

	AuthEndpoint  string
	TokenEndpoint string

	HTTPClient *http.Client
	Verifier   IDTokenVerifier
	Signer     *CookieSigner
	Now        func() time.Time
}

func NewAuthServiceFromEnv() (*AuthService, error) {
	clientID := os.Getenv("ZZT_GOOGLE_CLIENT_ID")
	if clientID == "" {
		clientID = os.Getenv("GOOGLE_CLIENT_ID")
	}
	if clientID == "" {
		return nil, nil
	}
	clientSecret := os.Getenv("ZZT_GOOGLE_CLIENT_SECRET")
	if clientSecret == "" {
		clientSecret = os.Getenv("GOOGLE_CLIENT_SECRET")
	}
	secret := []byte(os.Getenv("ZZT_AUTH_COOKIE_SECRET"))
	if len(secret) == 0 {
		secret = make([]byte, 32)
		if _, err := rand.Read(secret); err != nil {
			return nil, err
		}
	}
	return NewAuthService(clientID, clientSecret, os.Getenv("ZZT_GOOGLE_REDIRECT_URL"), secret), nil
}

func NewAuthService(clientID, clientSecret, redirectURL string, cookieSecret []byte) *AuthService {
	return &AuthService{
		ClientID:      clientID,
		ClientSecret:  clientSecret,
		RedirectURL:   redirectURL,
		AuthEndpoint:  defaultGoogleAuthEndpoint,
		TokenEndpoint: defaultGoogleTokenEndpoint,
		HTTPClient:    http.DefaultClient,
		Verifier:      NewGoogleIDTokenVerifier(),
		Signer:        NewCookieSigner(cookieSecret),
		Now:           time.Now,
	}
}

func (a *AuthService) Enabled() bool {
	return a != nil && a.ClientID != "" && a.Signer != nil
}

func (a *AuthService) HandleMe(w http.ResponseWriter, r *http.Request) {
	account, ok := a.AccountFromRequest(r)
	writeJSON(w, struct {
		Enabled       bool   `json:"enabled"`
		Authenticated bool   `json:"authenticated"`
		ID            string `json:"id,omitempty"`
		Name          string `json:"name,omitempty"`
		Email         string `json:"email,omitempty"`
	}{
		Enabled:       a.Enabled(),
		Authenticated: ok,
		ID:            account.ID,
		Name:          account.DisplayName(),
		Email:         account.Email,
	})
}

func (a *AuthService) HandleStart(w http.ResponseWriter, r *http.Request) {
	if !a.Enabled() {
		http.Error(w, ErrAuthDisabled.Error(), http.StatusServiceUnavailable)
		return
	}
	state, err := randomToken(24)
	if err != nil {
		http.Error(w, "could not start auth", http.StatusInternalServerError)
		return
	}
	verifier, err := randomToken(48)
	if err != nil {
		http.Error(w, "could not start auth", http.StatusInternalServerError)
		return
	}
	returnTo := r.URL.Query().Get("return")
	if !strings.HasPrefix(returnTo, "/") || strings.HasPrefix(returnTo, "//") {
		returnTo = "/"
	}
	redirectURL := a.redirectURL(r)
	stateValue, err := a.Signer.Encode(oauthState{
		State:        state,
		CodeVerifier: verifier,
		ReturnTo:     returnTo,
		ExpiresAt:    a.now().Add(authStateMaxAge).Unix(),
	})
	if err != nil {
		http.Error(w, "could not start auth", http.StatusInternalServerError)
		return
	}
	http.SetCookie(w, a.cookie(r, authStateCookie, stateValue, int(authStateMaxAge/time.Second)))

	challengeBytes := sha256.Sum256([]byte(verifier))
	q := url.Values{}
	q.Set("client_id", a.ClientID)
	q.Set("redirect_uri", redirectURL)
	q.Set("response_type", "code")
	q.Set("scope", "openid email profile")
	q.Set("state", state)
	q.Set("code_challenge", base64.RawURLEncoding.EncodeToString(challengeBytes[:]))
	q.Set("code_challenge_method", "S256")
	http.Redirect(w, r, a.AuthEndpoint+"?"+q.Encode(), http.StatusFound)
}

func (a *AuthService) HandleCallback(w http.ResponseWriter, r *http.Request) {
	if !a.Enabled() {
		http.Error(w, ErrAuthDisabled.Error(), http.StatusServiceUnavailable)
		return
	}
	stateCookie, err := r.Cookie(authStateCookie)
	if err != nil {
		http.Error(w, "missing auth state", http.StatusBadRequest)
		return
	}
	var state oauthState
	if err := a.Signer.Decode(stateCookie.Value, &state); err != nil || state.ExpiresAt < a.now().Unix() {
		http.Error(w, "invalid auth state", http.StatusBadRequest)
		return
	}
	if subtle.ConstantTimeCompare([]byte(state.State), []byte(r.URL.Query().Get("state"))) != 1 {
		http.Error(w, "state mismatch", http.StatusBadRequest)
		return
	}
	if authErr := r.URL.Query().Get("error"); authErr != "" {
		http.Error(w, authErr, http.StatusBadRequest)
		return
	}
	code := r.URL.Query().Get("code")
	if code == "" {
		http.Error(w, "missing code", http.StatusBadRequest)
		return
	}
	idToken, err := a.exchangeCode(r.Context(), code, state.CodeVerifier, a.redirectURL(r))
	if err != nil {
		http.Error(w, "token exchange failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	account, err := a.Verifier.VerifyIDToken(r.Context(), idToken, a.ClientID)
	if err != nil {
		http.Error(w, "id token verification failed", http.StatusUnauthorized)
		return
	}
	a.SetSessionCookie(w, r, account)
	a.clearCookie(w, r, authStateCookie)
	http.Redirect(w, r, state.ReturnTo, http.StatusFound)
}

func (a *AuthService) HandleLogout(w http.ResponseWriter, r *http.Request) {
	a.clearCookie(w, r, authSessionCookie)
	writeJSON(w, struct {
		OK bool `json:"ok"`
	}{OK: true})
}

func (a *AuthService) SetSessionCookie(w http.ResponseWriter, r *http.Request, account AuthenticatedAccount) {
	if !a.Enabled() {
		return
	}
	value, err := a.Signer.Encode(authSession{
		Account:   account,
		ExpiresAt: a.now().Add(authSessionMaxAge).Unix(),
	})
	if err != nil {
		return
	}
	http.SetCookie(w, a.cookie(r, authSessionCookie, value, int(authSessionMaxAge/time.Second)))
}

func (a *AuthService) AccountFromRequest(r *http.Request) (AuthenticatedAccount, bool) {
	if !a.Enabled() {
		return AuthenticatedAccount{}, false
	}
	cookie, err := r.Cookie(authSessionCookie)
	if err != nil {
		return AuthenticatedAccount{}, false
	}
	var session authSession
	if err := a.Signer.Decode(cookie.Value, &session); err != nil {
		return AuthenticatedAccount{}, false
	}
	if session.ExpiresAt < a.now().Unix() || session.Account.ID == "" {
		return AuthenticatedAccount{}, false
	}
	return session.Account, true
}

func (a *AuthService) exchangeCode(ctx context.Context, code, verifier, redirectURL string) (string, error) {
	form := url.Values{}
	form.Set("code", code)
	form.Set("client_id", a.ClientID)
	form.Set("redirect_uri", redirectURL)
	form.Set("grant_type", "authorization_code")
	form.Set("code_verifier", verifier)
	if a.ClientSecret != "" {
		form.Set("client_secret", a.ClientSecret)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.TokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	client := a.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var tokenResp struct {
		IDToken          string `json:"id_token"`
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
	}
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || tokenResp.Error != "" {
		if tokenResp.ErrorDescription != "" {
			return "", errors.New(tokenResp.ErrorDescription)
		}
		if tokenResp.Error != "" {
			return "", errors.New(tokenResp.Error)
		}
		return "", fmt.Errorf("google token endpoint returned %d", resp.StatusCode)
	}
	if tokenResp.IDToken == "" {
		return "", errors.New("missing id_token")
	}
	return tokenResp.IDToken, nil
}

func (a *AuthService) redirectURL(r *http.Request) string {
	if a.RedirectURL != "" {
		return a.RedirectURL
	}
	scheme := "http"
	if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	host := r.Host
	if forwardedHost := r.Header.Get("X-Forwarded-Host"); forwardedHost != "" {
		host = forwardedHost
	}
	return scheme + "://" + host + "/api/auth/google/callback"
}

func (a *AuthService) cookie(r *http.Request, name, value string, maxAge int) *http.Cookie {
	return &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https"),
	}
}

func (a *AuthService) clearCookie(w http.ResponseWriter, r *http.Request, name string) {
	cookie := a.cookie(r, name, "", -1)
	http.SetCookie(w, cookie)
}

func (a *AuthService) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
}

type authSession struct {
	Account   AuthenticatedAccount `json:"account"`
	ExpiresAt int64                `json:"exp"`
}

type oauthState struct {
	State        string `json:"state"`
	CodeVerifier string `json:"codeVerifier"`
	ReturnTo     string `json:"returnTo"`
	ExpiresAt    int64  `json:"exp"`
}

type CookieSigner struct {
	secret []byte
}

func NewCookieSigner(secret []byte) *CookieSigner {
	cp := make([]byte, len(secret))
	copy(cp, secret)
	return &CookieSigner{secret: cp}
}

func (s *CookieSigner) Encode(v interface{}) (string, error) {
	if s == nil || len(s.secret) == 0 {
		return "", ErrInvalidAuth
	}
	payload, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	payloadText := base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, s.secret)
	mac.Write([]byte(payloadText))
	signature := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return payloadText + "." + signature, nil
}

func (s *CookieSigner) Decode(value string, v interface{}) error {
	if s == nil || len(s.secret) == 0 {
		return ErrInvalidAuth
	}
	parts := strings.Split(value, ".")
	if len(parts) != 2 {
		return ErrInvalidAuth
	}
	mac := hmac.New(sha256.New, s.secret)
	mac.Write([]byte(parts[0]))
	want := mac.Sum(nil)
	got, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || !hmac.Equal(got, want) {
		return ErrInvalidAuth
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return ErrInvalidAuth
	}
	if err := json.Unmarshal(payload, v); err != nil {
		return ErrInvalidAuth
	}
	return nil
}

type GoogleIDTokenVerifier struct {
	JWKSURL    string
	HTTPClient *http.Client
	Now        func() time.Time

	mu       sync.Mutex
	keys     map[string]*rsa.PublicKey
	cacheExp time.Time
}

func NewGoogleIDTokenVerifier() *GoogleIDTokenVerifier {
	return &GoogleIDTokenVerifier{JWKSURL: defaultGoogleJWKSURL, HTTPClient: http.DefaultClient, Now: time.Now}
}

func (v *GoogleIDTokenVerifier) VerifyIDToken(ctx context.Context, token, clientID string) (AuthenticatedAccount, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return AuthenticatedAccount{}, ErrInvalidAuth
	}
	var header struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
	}
	if err := decodeJWTPart(parts[0], &header); err != nil {
		return AuthenticatedAccount{}, err
	}
	if header.Alg != "RS256" || header.Kid == "" {
		return AuthenticatedAccount{}, ErrInvalidAuth
	}
	key, err := v.key(ctx, header.Kid)
	if err != nil {
		return AuthenticatedAccount{}, err
	}
	signed := []byte(parts[0] + "." + parts[1])
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return AuthenticatedAccount{}, ErrInvalidAuth
	}
	digest := sha256.Sum256(signed)
	if err := rsa.VerifyPKCS1v15(key, crypto.SHA256, digest[:], sig); err != nil {
		return AuthenticatedAccount{}, ErrInvalidAuth
	}

	var claims googleIDClaims
	if err := decodeJWTPart(parts[1], &claims); err != nil {
		return AuthenticatedAccount{}, err
	}
	if claims.Sub == "" || !claims.audienceContains(clientID) {
		return AuthenticatedAccount{}, ErrInvalidAuth
	}
	if claims.Iss != "accounts.google.com" && claims.Iss != "https://accounts.google.com" {
		return AuthenticatedAccount{}, ErrInvalidAuth
	}
	now := v.now().Unix()
	if claims.Exp < now || claims.Iat > now+300 {
		return AuthenticatedAccount{}, ErrInvalidAuth
	}
	return AuthenticatedAccount{
		ID:    "google:" + claims.Sub,
		Email: claims.Email,
		Name:  claims.Name,
	}, nil
}

func (v *GoogleIDTokenVerifier) key(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	v.mu.Lock()
	if v.keys != nil && v.now().Before(v.cacheExp) {
		key := v.keys[kid]
		v.mu.Unlock()
		if key != nil {
			return key, nil
		}
		return nil, ErrInvalidAuth
	}
	v.mu.Unlock()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, v.JWKSURL, nil)
	if err != nil {
		return nil, err
	}
	client := v.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("jwks endpoint returned %d", resp.StatusCode)
	}
	var jwks struct {
		Keys []struct {
			Kty string `json:"kty"`
			Kid string `json:"kid"`
			Alg string `json:"alg"`
			Use string `json:"use"`
			N   string `json:"n"`
			E   string `json:"e"`
		} `json:"keys"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&jwks); err != nil {
		return nil, err
	}
	keys := make(map[string]*rsa.PublicKey)
	for _, jwk := range jwks.Keys {
		if jwk.Kty != "RSA" || jwk.Kid == "" {
			continue
		}
		key, err := rsaKeyFromJWK(jwk.N, jwk.E)
		if err == nil {
			keys[jwk.Kid] = key
		}
	}
	cacheExp := v.now().Add(time.Hour)
	if maxAge := cacheMaxAge(resp.Header.Get("Cache-Control")); maxAge > 0 {
		cacheExp = v.now().Add(maxAge)
	}

	v.mu.Lock()
	v.keys = keys
	v.cacheExp = cacheExp
	key := v.keys[kid]
	v.mu.Unlock()
	if key == nil {
		return nil, ErrInvalidAuth
	}
	return key, nil
}

func (v *GoogleIDTokenVerifier) now() time.Time {
	if v.Now != nil {
		return v.Now()
	}
	return time.Now()
}

type googleIDClaims struct {
	Iss   string          `json:"iss"`
	Sub   string          `json:"sub"`
	Aud   json.RawMessage `json:"aud"`
	Exp   int64           `json:"exp"`
	Iat   int64           `json:"iat"`
	Email string          `json:"email"`
	Name  string          `json:"name"`
}

func (c googleIDClaims) audienceContains(clientID string) bool {
	var one string
	if json.Unmarshal(c.Aud, &one) == nil {
		return one == clientID
	}
	var many []string
	if json.Unmarshal(c.Aud, &many) == nil {
		for _, aud := range many {
			if aud == clientID {
				return true
			}
		}
	}
	return false
}

func decodeJWTPart(part string, v interface{}) error {
	data, err := base64.RawURLEncoding.DecodeString(part)
	if err != nil {
		return ErrInvalidAuth
	}
	if err := json.Unmarshal(data, v); err != nil {
		return ErrInvalidAuth
	}
	return nil
}

func rsaKeyFromJWK(nText, eText string) (*rsa.PublicKey, error) {
	nBytes, err := base64.RawURLEncoding.DecodeString(nText)
	if err != nil {
		return nil, err
	}
	eBytes, err := base64.RawURLEncoding.DecodeString(eText)
	if err != nil {
		return nil, err
	}
	if len(eBytes) < 8 {
		padded := make([]byte, 8)
		copy(padded[8-len(eBytes):], eBytes)
		eBytes = padded
	}
	e := binary.BigEndian.Uint64(eBytes)
	return &rsa.PublicKey{N: new(big.Int).SetBytes(nBytes), E: int(e)}, nil
}

func randomToken(n int) (string, error) {
	data := make([]byte, n)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func cacheMaxAge(header string) time.Duration {
	for _, part := range strings.Split(header, ",") {
		part = strings.TrimSpace(part)
		if !strings.HasPrefix(part, "max-age=") {
			continue
		}
		var seconds int64
		if _, err := fmt.Sscanf(part, "max-age=%d", &seconds); err == nil && seconds > 0 {
			return time.Duration(seconds) * time.Second
		}
	}
	return 0
}
