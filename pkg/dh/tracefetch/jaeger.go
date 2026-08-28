package tracefetch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/ccfos/nightingale/v6/models"
)

// ErrTraceNotFound 表示上游确认这条 trace 不存在（api_v3 用 404 表达空结果）。
var ErrTraceNotFound = errors.New("trace not found")

// ErrResponseTooLarge 表示上游响应超过 maxTraceBodyBytes，已经放弃读取。
var ErrResponseTooLarge = errors.New("trace response is too large")

// maxTraceBodyBytes 限制单次响应体大小，避免超大 trace（或一次查太多 trace）把 center 的内存
// 打满。FindTraces 的响应是一批 trace 的全量 span，所以这个上限对它比对 GetTrace 更要紧。
const maxTraceBodyBytes = 64 << 20

// JaegerClient 按 traceID 取整条 trace。地址与凭据完全取自数据源配置，口径与官方
// center/router/router_proxy.go 的 dsProxy 一致。
type JaegerClient struct {
	target  *url.URL
	headers map[string]string
	auth    models.Auth
	http    *http.Client
}

// NewJaegerClient 从数据源构造客户端。timeout 同时作为整体请求超时。
func NewJaegerClient(ds *models.Datasource, timeout time.Duration) (*JaegerClient, error) {
	if ds == nil {
		return nil, errors.New("nil datasource")
	}

	target, err := ds.HTTPJson.ParseUrl()
	if err != nil {
		return nil, err
	}

	tlsConfig, err := ds.HTTPJson.TLS.TLSConfig()
	if err != nil {
		return nil, err
	}

	dialTimeout := time.Duration(ds.HTTPJson.DialTimeout) * time.Millisecond
	if dialTimeout <= 0 {
		dialTimeout = 10 * time.Second
	}

	return &JaegerClient{
		target:  target,
		headers: ds.HTTPJson.Headers,
		auth:    ds.AuthJson,
		http: &http.Client{
			Timeout: timeout,
			Transport: &http.Transport{
				TLSClientConfig: tlsConfig,
				Proxy:           http.ProxyFromEnvironment,
				DialContext:     (&net.Dialer{Timeout: dialTimeout}).DialContext,
			},
		},
	}, nil
}

// GetTrace 返回上游 /api/v3/traces/{traceID} 的原始响应体。
//
// 返回的 error 只描述上游的失败方式，不携带响应体：调用方会把它写进接口错误信息，而响应体
// 里就是尚未判权的 trace 内容。
func (c *JaegerClient) GetTrace(ctx context.Context, traceID string) ([]byte, error) {
	u := *c.target
	u.Path = strings.TrimRight(u.Path, "/") + "/api/v3/traces/" + url.PathEscape(traceID)
	return c.getJSON(ctx, u.String())
}

// getJSON 是 GetTrace / FindTraces 共用的取数：数据源凭据、TLS、超时与响应体大小上限都在这里，
// 两条路径的口径必须一致。
func (c *JaegerClient) getJSON(ctx context.Context, target string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	for key, value := range c.headers {
		req.Header.Set(key, value)
		if key == "Host" {
			req.Host = value
		}
	}
	if c.auth.BasicAuthUser != "" {
		req.SetBasicAuth(c.auth.BasicAuthUser, c.auth.BasicAuthPassword)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, ErrTraceNotFound
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("upstream responded %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxTraceBodyBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxTraceBodyBytes {
		return nil, ErrResponseTooLarge
	}
	return body, nil
}

// IsValidTraceID 只接受十六进制 trace id（OTLP/Jaeger 的编码），顺带挡掉路径穿越。
func IsValidTraceID(traceID string) bool {
	if traceID == "" || len(traceID) > 32 {
		return false
	}
	for _, r := range traceID {
		switch {
		case r >= '0' && r <= '9':
		case r >= 'a' && r <= 'f':
		case r >= 'A' && r <= 'F':
		default:
			return false
		}
	}
	return true
}
