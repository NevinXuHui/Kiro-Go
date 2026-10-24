package proxy

import (
	"context"
	"net/http"
)

type httpClientContextKey struct{}

// WithHTTPClient lets an embedding service supply its outbound transport without
// changing global proxy settings. The override also applies to retries and fallbacks.
func WithHTTPClient(ctx context.Context, client *http.Client) context.Context {
	return context.WithValue(ctx, httpClientContextKey{}, client)
}

func streamingHTTPClient(ctx context.Context, proxyURL string) *http.Client {
	if client, ok := ctx.Value(httpClientContextKey{}).(*http.Client); ok && client != nil {
		return client
	}
	return GetClientForProxy(proxyURL)
}
