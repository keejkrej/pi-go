// Ported from packages/mcp/src/oauth/types.ts (pi v1.5.2).

package oauth

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/keejkrej/pi-go/ai"
	aioauth "github.com/keejkrej/pi-go/ai/oauth"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/mcp"
)

// OAuthProtectedResourceMetadata is a protected-resource metadata document.
// Field order is the TS interface order. Extra is not a JSON key of its own.
type OAuthProtectedResourceMetadata struct {
	Resource             string   `json:"resource"`
	AuthorizationServers []string `json:"authorization_servers,omitzero"`
	ScopesSupported      []string `json:"scopes_supported,omitzero"`
	// Extra is the TS index signature, excluding the known fields.
	Extra *jsonx.Object `json:"-"`
}

// AuthorizationServerMetadata is an authorization-server metadata document.
type AuthorizationServerMetadata struct {
	Issuer                            string   `json:"issuer"`
	AuthorizationEndpoint             string   `json:"authorization_endpoint"`
	TokenEndpoint                     string   `json:"token_endpoint"`
	RegistrationEndpoint              *string  `json:"registration_endpoint,omitzero"`
	ScopesSupported                   []string `json:"scopes_supported,omitzero"`
	ResponseTypesSupported            []string `json:"response_types_supported"`
	GrantTypesSupported               []string `json:"grant_types_supported,omitzero"`
	TokenEndpointAuthMethodsSupported []string `json:"token_endpoint_auth_methods_supported,omitzero"`
	CodeChallengeMethodsSupported     []string `json:"code_challenge_methods_supported,omitzero"`
	ClientIdMetadataDocumentSupported *bool    `json:"client_id_metadata_document_supported,omitzero"`
	// AuthorizationResponseIssParameterSupported reports whether authorization responses carry an iss parameter (RFC 9207).
	AuthorizationResponseIssParameterSupported *bool `json:"authorization_response_iss_parameter_supported,omitzero"`
	// Extra is the TS index signature, excluding the known fields.
	Extra *jsonx.Object `json:"-"`
}

// OAuthTokens is a token-endpoint response.
type OAuthTokens struct {
	AccessToken  string   `json:"access_token"`
	TokenType    string   `json:"token_type"`
	ExpiresIn    *float64 `json:"expires_in,omitzero"`
	Scope        *string  `json:"scope,omitzero"`
	RefreshToken *string  `json:"refresh_token,omitzero"`
	IdToken      *string  `json:"id_token,omitzero"`
}

// OAuthClientMetadata is client metadata sent to dynamic registration.
type OAuthClientMetadata struct {
	RedirectUris            []string `json:"redirect_uris"`
	TokenEndpointAuthMethod *string  `json:"token_endpoint_auth_method,omitzero"`
	GrantTypes              []string `json:"grant_types,omitzero"`
	ResponseTypes           []string `json:"response_types,omitzero"`
	ApplicationType         *string  `json:"application_type,omitzero"`
	ClientName              *string  `json:"client_name,omitzero"`
	ClientUri               *string  `json:"client_uri,omitzero"`
	LogoUri                 *string  `json:"logo_uri,omitzero"`
	Scope                   *string  `json:"scope,omitzero"`
	Contacts                []string `json:"contacts,omitzero"`
	TosUri                  *string  `json:"tos_uri,omitzero"`
	PolicyUri               *string  `json:"policy_uri,omitzero"`
	JwksUri                 *string  `json:"jwks_uri,omitzero"`
	Jwks                    any      `json:"jwks,omitzero"`
	SoftwareId              *string  `json:"software_id,omitzero"`
	SoftwareVersion         *string  `json:"software_version,omitzero"`
	SoftwareStatement       *string  `json:"software_statement,omitzero"`
}

// OAuthClientInformation is a client id, with an optional secret.
type OAuthClientInformation struct {
	ClientId              string   `json:"client_id"`
	ClientSecret          *string  `json:"client_secret,omitzero"`
	ClientIdIssuedAt      *float64 `json:"client_id_issued_at,omitzero"`
	ClientSecretExpiresAt *float64 `json:"client_secret_expires_at,omitzero"`
}

// OAuthClientInformationFull is OAuthClientInformation plus OAuthClientMetadata.
type OAuthClientInformationFull struct {
	OAuthClientInformation
	OAuthClientMetadata
}

// OAuthClientInformationMixed is OAuthClientInformation or OAuthClientInformationFull.
// There is no discriminator; narrow with a type switch. "token_endpoint_auth_method" in
// the object is *OAuthClientInformationFull with TokenEndpointAuthMethod set.
type OAuthClientInformationMixed interface {
	isOAuthClientInformationMixed()
}

func (*OAuthClientInformation) isOAuthClientInformationMixed()     {}
func (*OAuthClientInformationFull) isOAuthClientInformationMixed() {}

var (
	_ OAuthClientInformationMixed = (*OAuthClientInformation)(nil)
	_ OAuthClientInformationMixed = (*OAuthClientInformationFull)(nil)
)

// OAuthDiscoveryState is the cached discovery result for one authorization server.
type OAuthDiscoveryState struct {
	AuthorizationServerUrl      string                          `json:"authorizationServerUrl"`
	AuthorizationServerMetadata *AuthorizationServerMetadata    `json:"authorizationServerMetadata,omitzero"`
	ResourceMetadata            *OAuthProtectedResourceMetadata `json:"resourceMetadata,omitzero"`
	ResourceMetadataUrl         *string                         `json:"resourceMetadataUrl,omitzero"`
}

// OAuthServerInfo is the discovered authorization server and optional resource metadata.
type OAuthServerInfo struct {
	AuthorizationServerUrl      string                          `json:"authorizationServerUrl"`
	AuthorizationServerMetadata *AuthorizationServerMetadata    `json:"authorizationServerMetadata,omitzero"`
	ResourceMetadata            *OAuthProtectedResourceMetadata `json:"resourceMetadata,omitzero"`
}

// OAuthChallenge is a parsed WWW-Authenticate bearer or DPoP challenge.
// ResourceMetadataUrl is nil when the parameter is absent or not a URL.
type OAuthChallenge struct {
	ResourceMetadataUrl *url.URL
	Scope               *string
	Error               *string
	ErrorDescription    *string
}

// ParseProtectedResourceMetadata validates a protected-resource metadata document.
func ParseProtectedResourceMetadata(value any) (*OAuthProtectedResourceMetadata, error) {
	input, err := typesAsObject(value, "OAuth protected resource metadata")
	if err != nil {
		return nil, err
	}
	resVal, _ := input.Get("resource")
	resource, err := typesSafeUrl(resVal, "OAuth protected resource metadata resource")
	if err != nil {
		return nil, err
	}
	asVal, hasAS := input.Get("authorization_servers")
	var authServers []string
	if hasAS {
		authServers, err = typesOptionalStrings(asVal, "authorization_servers")
		if err != nil {
			return nil, err
		}
		for i, raw := range authServers {
			checked, err := typesSafeUrl(raw, "authorization server URL")
			if err != nil {
				return nil, err
			}
			authServers[i] = checked
		}
	}
	ssVal, hasSS := input.Get("scopes_supported")
	var scopes []string
	if hasSS {
		scopes, err = typesOptionalStrings(ssVal, "scopes_supported")
		if err != nil {
			return nil, err
		}
	}
	extra := typesCollectExtra(input, "resource", "authorization_servers", "scopes_supported")
	return &OAuthProtectedResourceMetadata{
		Resource:             resource,
		AuthorizationServers: authServers,
		ScopesSupported:      scopes,
		Extra:                extra,
	}, nil
}

// ParseAuthorizationServerMetadata validates an authorization-server metadata document.
func ParseAuthorizationServerMetadata(value any) (*AuthorizationServerMetadata, error) {
	input, err := typesAsObject(value, "authorization server metadata")
	if err != nil {
		return nil, err
	}
	rtVal, _ := input.Get("response_types_supported")
	responseTypes, err := typesOptionalStrings(rtVal, "response_types_supported")
	if err != nil {
		return nil, err
	}
	if responseTypes == nil {
		return nil, fmt.Errorf("Invalid response_types_supported")
	}
	issuerVal, _ := input.Get("issuer")
	issuer, err := typesSafeUrl(issuerVal, "authorization server issuer")
	if err != nil {
		return nil, err
	}
	aeVal, _ := input.Get("authorization_endpoint")
	authEndpoint, err := typesSafeUrl(aeVal, "authorization endpoint")
	if err != nil {
		return nil, err
	}
	teVal, _ := input.Get("token_endpoint")
	tokenEndpoint, err := typesSafeUrl(teVal, "token endpoint")
	if err != nil {
		return nil, err
	}
	reVal, _ := input.Get("registration_endpoint")
	regEndpoint, err := typesOptionalUrl(reVal, "registration endpoint")
	if err != nil {
		return nil, err
	}
	ssVal, _ := input.Get("scopes_supported")
	scopesSupported, err := typesOptionalStrings(ssVal, "scopes_supported")
	if err != nil {
		return nil, err
	}
	gtVal, _ := input.Get("grant_types_supported")
	grantTypesSupported, err := typesOptionalStrings(gtVal, "grant_types_supported")
	if err != nil {
		return nil, err
	}
	amVal, _ := input.Get("token_endpoint_auth_methods_supported")
	authMethodsSupported, err := typesOptionalStrings(amVal, "token_endpoint_auth_methods_supported")
	if err != nil {
		return nil, err
	}
	ccVal, _ := input.Get("code_challenge_methods_supported")
	codeChallengeMethodsSupported, err := typesOptionalStrings(ccVal, "code_challenge_methods_supported")
	if err != nil {
		return nil, err
	}
	var cimdSupported *bool
	if v, ok := input.Get("client_id_metadata_document_supported"); ok {
		if b, isBool := v.(bool); isBool {
			cimdSupported = &b
		}
	}
	var issSupported *bool
	if v, ok := input.Get("authorization_response_iss_parameter_supported"); ok {
		if b, isBool := v.(bool); isBool {
			issSupported = &b
		}
	}
	extra := typesCollectExtra(
		input,
		"issuer",
		"authorization_endpoint",
		"token_endpoint",
		"registration_endpoint",
		"scopes_supported",
		"response_types_supported",
		"grant_types_supported",
		"token_endpoint_auth_methods_supported",
		"code_challenge_methods_supported",
		"client_id_metadata_document_supported",
		"authorization_response_iss_parameter_supported",
	)
	return &AuthorizationServerMetadata{
		Issuer:                                     issuer,
		AuthorizationEndpoint:                      authEndpoint,
		TokenEndpoint:                              tokenEndpoint,
		RegistrationEndpoint:                       regEndpoint,
		ScopesSupported:                            scopesSupported,
		ResponseTypesSupported:                     responseTypes,
		GrantTypesSupported:                        grantTypesSupported,
		TokenEndpointAuthMethodsSupported:          authMethodsSupported,
		CodeChallengeMethodsSupported:              codeChallengeMethodsSupported,
		ClientIdMetadataDocumentSupported:          cimdSupported,
		AuthorizationResponseIssParameterSupported: issSupported,
		Extra: extra,
	}, nil
}

// ParseOAuthTokens validates a token-endpoint JSON body.
func ParseOAuthTokens(value any) (*OAuthTokens, error) {
	input, err := typesAsObject(value, "OAuth token response")
	if err != nil {
		return nil, err
	}
	var expiresIn *float64
	if expVal, ok := input.Get("expires_in"); ok && !typesAbsent(expVal) {
		f, err := typesToJSNumber(expVal)
		if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
			return nil, fmt.Errorf("Invalid expires_in")
		}
		expiresIn = &f
	}
	atVal, _ := input.Get("access_token")
	accessToken, err := typesRequiredString(atVal, "access_token")
	if err != nil {
		return nil, err
	}
	ttVal, _ := input.Get("token_type")
	tokenType, err := typesRequiredString(ttVal, "token_type")
	if err != nil {
		return nil, err
	}
	scopeVal, _ := input.Get("scope")
	scope, err := typesOptionalString(scopeVal, "scope")
	if err != nil {
		return nil, err
	}
	rtVal, _ := input.Get("refresh_token")
	refreshToken, err := typesOptionalString(rtVal, "refresh_token")
	if err != nil {
		return nil, err
	}
	idVal, _ := input.Get("id_token")
	idToken, err := typesOptionalString(idVal, "id_token")
	if err != nil {
		return nil, err
	}
	return &OAuthTokens{
		AccessToken:  accessToken,
		TokenType:    tokenType,
		ExpiresIn:    expiresIn,
		Scope:        scope,
		RefreshToken: refreshToken,
		IdToken:      idToken,
	}, nil
}

// ParseClientInformation validates a dynamic-client-registration response.
func ParseClientInformation(value any) (*OAuthClientInformationFull, error) {
	input, err := typesAsObject(value, "OAuth client registration response")
	if err != nil {
		return nil, err
	}
	cidVal, _ := input.Get("client_id")
	clientId, err := typesRequiredString(cidVal, "client_id")
	if err != nil {
		return nil, err
	}
	csVal, _ := input.Get("client_secret")
	clientSecret, err := typesOptionalString(csVal, "client_secret")
	if err != nil {
		return nil, err
	}
	var clientIdIssuedAt *float64
	if v, ok := input.Get("client_id_issued_at"); ok {
		if f, ok := typesAsFiniteFloat(v); ok {
			clientIdIssuedAt = &f
		}
	}
	var clientSecretExpiresAt *float64
	if v, ok := input.Get("client_secret_expires_at"); ok {
		if f, ok := typesAsFiniteFloat(v); ok {
			clientSecretExpiresAt = &f
		}
	}
	ruVal, _ := input.Get("redirect_uris")
	redirectUris, err := typesOptionalStrings(ruVal, "redirect_uris")
	if err != nil {
		return nil, err
	}
	if redirectUris == nil {
		redirectUris = []string{}
	}

	var meta OAuthClientMetadata
	metaObj := input.Clone()
	metaObj.Delete("client_id")
	metaObj.Delete("client_secret")
	metaObj.Delete("client_id_issued_at")
	metaObj.Delete("client_secret_expires_at")
	metaObj.Delete("redirect_uris")
	if err := jsonx.Decode(metaObj, &meta); err != nil {
		return nil, err
	}
	meta.RedirectUris = redirectUris

	return &OAuthClientInformationFull{
		OAuthClientInformation: OAuthClientInformation{
			ClientId:              clientId,
			ClientSecret:          clientSecret,
			ClientIdIssuedAt:      clientIdIssuedAt,
			ClientSecretExpiresAt: clientSecretExpiresAt,
		},
		OAuthClientMetadata: meta,
	}, nil
}

func typesAsObject(value any, name string) (*jsonx.Object, error) {
	switch v := value.(type) {
	case *jsonx.Object:
		if v == nil {
			return nil, fmt.Errorf("Invalid %s", name)
		}
		return v, nil
	case map[string]any:
		if v == nil {
			return nil, fmt.Errorf("Invalid %s", name)
		}
		obj := jsonx.NewObject()
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		for _, k := range keys {
			obj.Set(k, v[k])
		}
		return obj, nil
	default:
		return nil, fmt.Errorf("Invalid %s", name)
	}
}

func typesAbsent(v any) bool {
	if v == nil {
		return true
	}
	s, ok := v.(string)
	return ok && s == ""
}

func typesRequiredString(v any, name string) (string, error) {
	s, ok := v.(string)
	if !ok || s == "" {
		return "", fmt.Errorf("Invalid %s", name)
	}
	return s, nil
}

func typesOptionalString(v any, name string) (*string, error) {
	if typesAbsent(v) {
		return nil, nil
	}
	s, err := typesRequiredString(v, name)
	if err != nil {
		return nil, err
	}
	return &s, nil
}

func typesOptionalStrings(v any, name string) ([]string, error) {
	if v == nil {
		return nil, nil
	}
	switch s := v.(type) {
	case []string:
		if s == nil {
			return nil, nil
		}
		out := make([]string, len(s))
		copy(out, s)
		return out, nil
	case []any:
		if s == nil {
			return nil, nil
		}
		out := make([]string, len(s))
		for i, item := range s {
			str, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("Invalid %s", name)
			}
			out[i] = str
		}
		return out, nil
	default:
		return nil, fmt.Errorf("Invalid %s", name)
	}
}

func typesSafeUrl(v any, name string) (string, error) {
	text, err := typesRequiredString(v, name)
	if err != nil {
		return "", err
	}
	if aioauth.TrustedHttpUrl(text) == nil {
		return "", fmt.Errorf("Invalid %s", name)
	}
	return text, nil
}

func typesOptionalUrl(v any, name string) (*string, error) {
	if typesAbsent(v) {
		return nil, nil
	}
	s, err := typesSafeUrl(v, name)
	if err != nil {
		return nil, err
	}
	return &s, nil
}

func typesAsFiniteFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, !math.IsNaN(n) && !math.IsInf(n, 0)
	case float32:
		f := float64(n)
		return f, !math.IsNaN(f) && !math.IsInf(f, 0)
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case int32:
		return float64(n), true
	default:
		return 0, false
	}
}

func typesToJSNumber(v any) (float64, error) {
	if f, ok := typesAsFiniteFloat(v); ok {
		return f, nil
	}
	switch x := v.(type) {
	case string:
		trimmed := strings.TrimSpace(x)
		if trimmed == "" {
			return 0, nil
		}
		f, err := strconv.ParseFloat(trimmed, 64)
		if err != nil {
			return 0, err
		}
		return f, nil
	case bool:
		if x {
			return 1, nil
		}
		return 0, nil
	default:
		return 0, fmt.Errorf("not a number")
	}
}

func typesCollectExtra(input *jsonx.Object, known ...string) *jsonx.Object {
	var extra *jsonx.Object
	for k, v := range input.All() {
		if slices.Contains(known, k) {
			continue
		}
		if extra == nil {
			extra = jsonx.NewObject()
		}
		extra.Set(k, v)
	}
	return extra
}

func typesHttpClientFetch(fetch mcp.McpFetch, client *http.Client) mcp.McpFetch {
	if fetch != nil {
		return fetch
	}
	hc := client
	if hc == nil {
		hc = ai.NodeHttpProxyDefaultClient()
	}
	return func(ctx context.Context, input string, init *mcp.McpRequestInit) (*http.Response, error) {
		if ctx == nil {
			ctx = context.Background()
		}
		method := http.MethodGet
		var body []byte
		var hdrs http.Header
		if init != nil {
			if init.Method != "" {
				method = init.Method
			}
			body = init.Body
			hdrs = init.Headers
		}
		var bodyReader *bytes.Reader
		if len(body) > 0 {
			bodyReader = bytes.NewReader(body)
		}
		var req *http.Request
		var err error
		if bodyReader != nil {
			req, err = http.NewRequestWithContext(ctx, method, input, bodyReader)
		} else {
			req, err = http.NewRequestWithContext(ctx, method, input, nil)
		}
		if err != nil {
			return nil, err
		}
		for k, vals := range hdrs {
			for _, v := range vals {
				req.Header.Add(k, v)
			}
		}
		return hc.Do(req)
	}
}
