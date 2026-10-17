// Package auth renews a portal session through the CAS login form.
package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/emmansun/gmsm/sm2"
)

const (
	loginURL   = "https://ca.muc.edu.cn/zfca/login"
	serviceURL = "http://my.muc.edu.cn/user/simpleSSOLogin"
	userAgent  = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/145.0.0.0 Safari/537.36 Edg/145.0.0.0"
)

var (
	flowIDPattern    = regexp.MustCompile(`<input[^>]*\bname=["']flowId["'][^>]*\bvalue=["']([^"']+)["']`)
	publicKeyPattern = regexp.MustCompile(`"publicKey"\s*:\s*"([^"]+)"`)
)

// LoginPage contains the fields required to submit the CAS login form.
type LoginPage struct {
	FlowID    string
	PublicKey string
}

// ParseLoginPage extracts the login form fields from CAS HTML.
func ParseLoginPage(html []byte) (LoginPage, error) {
	flowID := flowIDPattern.FindSubmatch(html)
	if len(flowID) != 2 {
		return LoginPage{}, errors.New("CAS login page is missing flowId")
	}
	publicKey := publicKeyPattern.FindSubmatch(html)
	if len(publicKey) != 2 {
		return LoginPage{}, errors.New("CAS login page is missing publicKey")
	}
	var decodedKey string
	if err := json.Unmarshal(append(append([]byte{'"'}, publicKey[1]...), '"'), &decodedKey); err != nil {
		return LoginPage{}, fmt.Errorf("CAS login page has invalid publicKey: %w", err)
	}
	return LoginPage{FlowID: string(flowID[1]), PublicKey: decodedKey}, nil
}

// EncryptPassword returns a base64-encoded SM2 C1C3C2 ciphertext.
func EncryptPassword(password, encodedPublicKey string) (string, error) {
	if password == "" {
		return "", errors.New("password is required")
	}
	key, err := base64.StdEncoding.DecodeString(encodedPublicKey)
	if err != nil {
		return "", fmt.Errorf("decode SM2 public key: %w", err)
	}
	if len(key) != 64 {
		if len(key) != 65 || key[0] != 4 {
			return "", errors.New("SM2 public key must contain 64 bytes or an uncompressed point")
		}
	} else {
		key = append([]byte{4}, key...)
	}
	publicKey, err := sm2.NewPublicKey(key)
	if err != nil {
		return "", fmt.Errorf("parse SM2 public key: %w", err)
	}
	ciphertext, err := sm2.Encrypt(rand.Reader, publicKey, []byte(password), sm2.NewPlainEncrypterOpts(sm2.MarshalUncompressed, sm2.C1C3C2))
	if err != nil {
		return "", fmt.Errorf("encrypt SM2 password: %w", err)
	}
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

// Login authenticates with CAS and returns the portal cookies, including JSESSIONID.
func Login(ctx context.Context, client *http.Client, username, password string) ([]*http.Cookie, error) {
	if client == nil {
		return nil, errors.New("HTTP client is required")
	}
	if username == "" || password == "" {
		return nil, errors.New("username and password are required")
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, fmt.Errorf("create cookie jar: %w", err)
	}
	session := *client
	session.Jar = jar
	session.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	loginPageResponse, err := session.Do(newRequest(ctx, http.MethodGet, loginURL+"?service="+url.QueryEscape(serviceURL), nil))
	if err != nil {
		return nil, fmt.Errorf("get CAS login page: %w", err)
	}
	body, readErr := io.ReadAll(loginPageResponse.Body)
	loginPageResponse.Body.Close()
	if readErr != nil {
		return nil, fmt.Errorf("read CAS login page: %w", readErr)
	}
	if loginPageResponse.StatusCode < 200 || loginPageResponse.StatusCode >= 300 {
		return nil, fmt.Errorf("CAS login page returned HTTP %d", loginPageResponse.StatusCode)
	}
	page, err := ParseLoginPage(body)
	if err != nil {
		return nil, err
	}
	encryptedPassword, err := EncryptPassword(password, page.PublicKey)
	if err != nil {
		return nil, err
	}
	form := url.Values{
		"username":  {username},
		"password":  {encryptedPassword},
		"loginType": {"username_password"},
		"flowId":    {page.FlowID},
		"submit":    {"登录"},
	}
	request := newRequest(ctx, http.MethodPost, loginURL, strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := session.Do(request)
	if err != nil {
		return nil, fmt.Errorf("submit CAS login form: %w", err)
	}
	for redirects := 0; isRedirect(response.StatusCode); redirects++ {
		// The HTTPS ticket callback has already established the portal session.
		if isHTTPSServiceCallback(response.Request.URL) {
			break
		}
		if redirects == 10 {
			response.Body.Close()
			return nil, errors.New("CAS redirect limit exceeded")
		}
		location := response.Header.Get("Location")
		if location == "" {
			response.Body.Close()
			return nil, errors.New("CAS redirect has no Location header")
		}
		next, err := response.Request.URL.Parse(location)
		if err != nil {
			response.Body.Close()
			return nil, fmt.Errorf("parse CAS redirect: %w", err)
		}
		response.Body.Close()
		response, err = session.Do(newRequest(ctx, http.MethodGet, next.String(), nil))
		if err != nil {
			return nil, fmt.Errorf("follow CAS redirect: %w", err)
		}
	}
	callbackURL := response.Request.URL
	response.Body.Close()
	if !isHTTPSServiceCallback(callbackURL) {
		return nil, errors.New("CAS login did not reach the portal callback")
	}
	return requiredPortalCookies(jar.Cookies(callbackURL))
}

func isHTTPSServiceCallback(target *url.URL) bool {
	service, err := url.Parse(serviceURL)
	if err != nil || target == nil {
		return false
	}
	return target.Scheme == "https" && strings.EqualFold(target.Host, service.Host) && target.Path == service.Path
}

func requiredPortalCookies(cookies []*http.Cookie) ([]*http.Cookie, error) {
	byName := make(map[string]*http.Cookie, len(cookies))
	for _, cookie := range cookies {
		if cookie != nil && cookie.Value != "" {
			byName[cookie.Name] = cookie
		}
	}
	selected := make([]*http.Cookie, 0, 2)
	for _, name := range []string{"JSESSIONID", "nginx"} {
		cookie := byName[name]
		if cookie == nil {
			return nil, fmt.Errorf("portal login did not establish %s", name)
		}
		selected = append(selected, cookie)
	}
	return selected, nil
}

func newRequest(ctx context.Context, method, rawURL string, body io.Reader) *http.Request {
	request, _ := http.NewRequestWithContext(ctx, method, rawURL, body)
	request.Header.Set("User-Agent", userAgent)
	return request
}

func isRedirect(status int) bool {
	return status == http.StatusMovedPermanently || status == http.StatusFound || status == http.StatusSeeOther || status == http.StatusTemporaryRedirect || status == http.StatusPermanentRedirect
}
