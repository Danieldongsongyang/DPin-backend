package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/larksuite/cli/extension/credential"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestApplicationCredentialRenewal(t *testing.T) {
	t.Setenv("LARKSUITE_CLI_APP_ID", "cli_test")
	t.Setenv("LARKSUITE_CLI_APP_SECRET", "test-secret")
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"code":0,"access_token":"synthetic_%d","expires_in":7200}`, n)
	}))
	defer server.Close()
	spec := credential.TokenSpec{Type: credential.TokenTypeTAT, AppID: "cli_test"}
	first := &dpinProvider{endpoint: server.URL, client: server.Client()}
	a, err := first.ResolveToken(context.Background(), spec)
	require.NoError(t, err)
	b, err := first.ResolveToken(context.Background(), spec)
	require.NoError(t, err)
	assert.Equal(t, a.Value, b.Value)
	assert.Equal(t, int32(1), calls.Load())
	second := &dpinProvider{endpoint: server.URL, client: server.Client()}
	renewed, err := second.ResolveToken(context.Background(), spec)
	require.NoError(t, err)
	assert.NotEqual(t, a.Value, renewed.Value)
	assert.Equal(t, int32(2), calls.Load())
	_, err = second.ResolveToken(context.Background(), credential.TokenSpec{Type: credential.TokenTypeUAT, AppID: "cli_test"})
	assert.Error(t, err)
	t.Setenv("LARKSUITE_CLI_APP_SECRET", "")
	_, err = second.ResolveAccount(context.Background())
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "test-secret")
}

func TestCredentialErrorsAndExpiredTokenDoNotLeakSecrets(t *testing.T) {
	t.Setenv("LARKSUITE_CLI_APP_ID", "cli_test")
	t.Setenv("LARKSUITE_CLI_APP_SECRET", "private-test-secret")
	for _, body := range []string{`{"code":10014,"msg":"private-test-secret"}`, `{"code":0,"access_token":"expired-test-token","expires_in":1}`} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, body) }))
		provider := &dpinProvider{endpoint: server.URL, client: server.Client()}
		_, err := provider.ResolveToken(context.Background(), credential.TokenSpec{Type: credential.TokenTypeTAT, AppID: "cli_test"})
		require.Error(t, err)
		assert.NotContains(t, err.Error(), "private-test-secret")
		assert.NotContains(t, err.Error(), "expired-test-token")
		server.Close()
	}
}
