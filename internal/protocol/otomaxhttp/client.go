package otomaxhttp

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const defaultTimeout = 20 * time.Second
const defaultMaxResponseLen int64 = 64 << 10

type Client struct {
	baseURL    *url.URL
	httpClient *http.Client
}

func NewClient(baseURL string, httpClient *http.Client) (*Client, error) {
	u, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("invalid OtoMax base URL: %w", err)
	}
	if !strings.EqualFold(u.Scheme, "https") || u.Host == "" {
		return nil, fmt.Errorf("OtoMax base URL must be an absolute HTTPS URL")
	}
	if httpClient == nil {
		httpClient = secureHTTPClient()
	}
	return &Client{baseURL: u, httpClient: httpClient}, nil
}

// secureHTTPClient checks every resolved connection target and redirect before
// dialing. Configuration is data, so it must not be able to reach loopback or
// private infrastructure even after DNS resolution.
func secureHTTPClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	dialer := &net.Dialer{Timeout: defaultTimeout}
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		if err := requirePublicHost(ctx, host); err != nil {
			return nil, err
		}
		return dialer.DialContext(ctx, network, address)
	}
	return &http.Client{
		Timeout:   defaultTimeout,
		Transport: transport,
		CheckRedirect: func(req *http.Request, _ []*http.Request) error {
			if !strings.EqualFold(req.URL.Scheme, "https") {
				return fmt.Errorf("OtoMax redirect must use HTTPS")
			}
			return requirePublicHost(req.Context(), req.URL.Hostname())
		},
	}
}

func requirePublicHost(ctx context.Context, host string) error {
	if host == "" || strings.EqualFold(host, "localhost") || strings.HasSuffix(strings.ToLower(host), ".local") || strings.HasSuffix(strings.ToLower(host), ".internal") {
		return fmt.Errorf("OtoMax request target is not public")
	}
	if ip := net.ParseIP(host); ip != nil {
		if privateIP(ip) {
			return fmt.Errorf("OtoMax request target is not public")
		}
		return nil
	}
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return fmt.Errorf("resolve OtoMax target: %w", err)
	}
	if len(ips) == 0 {
		return fmt.Errorf("OtoMax request target did not resolve")
	}
	for _, item := range ips {
		if privateIP(item.IP) {
			return fmt.Errorf("OtoMax request target is not public")
		}
	}
	return nil
}

func privateIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.Equal(net.ParseIP("169.254.169.254"))
}

func (c *Client) Execute(ctx context.Context, endpoint Endpoint, values TemplateValues) (*Response, error) {
	if c == nil || c.baseURL == nil {
		return nil, fmt.Errorf("OtoMax client is not configured")
	}
	method := strings.ToUpper(strings.TrimSpace(endpoint.Method))
	if method != http.MethodGet && method != http.MethodPost && method != http.MethodPut {
		return nil, fmt.Errorf("unsupported OtoMax HTTP method %q", endpoint.Method)
	}
	path, err := renderTemplate(endpoint.Path, values)
	if err != nil {
		return nil, err
	}
	if !strings.HasPrefix(path, "/") || strings.Contains(path, "://") {
		return nil, fmt.Errorf("OtoMax endpoint path must be an absolute path")
	}
	u := *c.baseURL
	u.Path = strings.TrimRight(c.baseURL.Path, "/") + path
	query := u.Query()
	for key, value := range endpoint.Query {
		key, err = renderTemplate(key, values)
		if err != nil {
			return nil, err
		}
		rendered, err := renderTemplate(value, values)
		if err != nil {
			return nil, err
		}
		// Game tanpa server: jangan kirim parameter kosong ke provider.
		if rendered == "" && strings.TrimSpace(value) == "{{server_id}}" {
			continue
		}
		query.Set(key, rendered)
	}
	u.RawQuery = query.Encode()

	body, err := renderTemplate(endpoint.Body, values)
	if err != nil {
		return nil, err
	}
	requestCtx := ctx
	if endpoint.Timeout > 0 {
		var cancel context.CancelFunc
		requestCtx, cancel = context.WithTimeout(ctx, endpoint.Timeout)
		defer cancel()
	}
	req, err := http.NewRequestWithContext(requestCtx, method, u.String(), bytes.NewBufferString(body))
	if err != nil {
		return nil, fmt.Errorf("build OtoMax request: %w", err)
	}
	for key, value := range endpoint.Headers {
		value, err = renderTemplate(value, values)
		if err != nil {
			return nil, err
		}
		req.Header.Set(key, value)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	maxLen := endpoint.MaxResponseLen
	if maxLen <= 0 {
		maxLen = defaultMaxResponseLen
	}
	bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, maxLen+1))
	if err != nil {
		return nil, fmt.Errorf("read OtoMax response: %w", err)
	}
	if int64(len(bodyBytes)) > maxLen {
		return nil, fmt.Errorf("OtoMax response exceeds %d bytes", maxLen)
	}
	return &Response{StatusCode: resp.StatusCode, Body: bodyBytes}, nil
}

func renderTemplate(template string, values TemplateValues) (string, error) {
	replacements := map[string]string{
		"product_code":      values.ProductCode,
		"customer_id":       values.CustomerID,
		"server_id":         values.ServerID,
		"ref_id":            values.RefID,
		"provider_order_id": values.ProviderOrderID,
		"callback_url":      values.CallbackURL,
	}
	result := template
	for {
		start := strings.Index(result, "{{")
		if start < 0 {
			return result, nil
		}
		endOffset := strings.Index(result[start+2:], "}}")
		if endOffset < 0 {
			return "", fmt.Errorf("unterminated OtoMax template placeholder")
		}
		end := start + 2 + endOffset
		key := strings.TrimSpace(result[start+2 : end])
		var value string
		var ok bool
		if strings.HasPrefix(key, "secret.") {
			value, ok = values.Secrets[strings.TrimPrefix(key, "secret.")]
		} else {
			value, ok = replacements[key]
		}
		if !ok || (value == "" && key != "server_id") {
			return "", fmt.Errorf("OtoMax template value %q is not configured", key)
		}
		result = result[:start] + value + result[end+2:]
	}
}
