package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
)

func TestDocumentation(t *testing.T) {
	t.Parallel()

	handler := newTestAPI(&parserStub{}, &repoStub{}, logger())
	for _, path := range []string{"/documentation", "/documentation/"} {
		t.Run(path, func(t *testing.T) {
			t.Parallel()

			resp := httptest.NewRecorder()
			handler.ServeHTTP(resp, httptest.NewRequest(http.MethodGet, path, nil))
			require.Equal(t, http.StatusOK, resp.Code)
			assert.Equal(t, "text/html; charset=utf-8", resp.Header().Get("Content-Type"))
			assert.Contains(t, resp.Body.String(), `url: "/documentation/swagger.json"`)
			assert.Contains(t, resp.Body.String(), `src="/documentation/swagger-ui-bundle.js"`)
			assert.Contains(t, resp.Body.String(), `href="/documentation/swagger-ui.css"`)
			assert.Contains(t, resp.Body.String(), "validatorUrl: null")
		})
	}
	for _, asset := range []struct{ path, contentType string }{
		{"/documentation/swagger-ui-bundle.js", "javascript"},
		{"/documentation/swagger-ui.css", "text/css"},
	} {
		t.Run(asset.path, func(t *testing.T) {
			t.Parallel()

			resp := httptest.NewRecorder()
			handler.ServeHTTP(resp, httptest.NewRequest(http.MethodGet, asset.path, nil))
			require.Equal(t, http.StatusOK, resp.Code)
			assert.Contains(t, resp.Header().Get("Content-Type"), asset.contentType)
			assert.NotEmpty(t, resp.Body.Bytes())
		})
	}
	for _, path := range []string{"/documentation", "/documentation/", "/documentation/swagger-ui.css", "/documentation/swagger.json", "/documentation/swagger.yaml", "/openapi.yaml"} {
		resp := httptest.NewRecorder()
		handler.ServeHTTP(resp, httptest.NewRequest(http.MethodPost, path, nil))
		assert.Equal(t, http.StatusMethodNotAllowed, resp.Code, path)
		assert.Equal(t, http.MethodGet, resp.Header().Get("Allow"), path)
		assertError(t, resp, "method_not_allowed")
	}
}

func TestOpenAPIContract(t *testing.T) {
	t.Parallel()

	handler := newTestAPI(&parserStub{}, &repoStub{}, logger())
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, httptest.NewRequest(http.MethodGet, "/documentation/swagger.yaml", nil))
	require.Equal(t, http.StatusOK, resp.Code)
	assert.Equal(t, "application/yaml", resp.Header().Get("Content-Type"))
	var document map[string]any
	require.NoError(t, yaml.Unmarshal(resp.Body.Bytes(), &document))
	assert.Equal(t, "3.1.0", document["openapi"])
	assert.Equal(t, []any{map[string]any{"url": "/", "description": "Current server"}}, document["servers"])

	// These details are essential for Swagger UI's file picker and Try it out.
	parse := documentAt(t, document, "paths", "/boarding-pass/parse-from-file", "post")
	assert.Equal(t, true, documentAt(t, parse, "requestBody", "required"))
	upload := documentAt(t, parse, "requestBody", "content", "multipart/form-data", "schema")
	assert.Equal(t, "object", documentAt(t, upload, "type"))
	assert.Equal(t, []any{"file"}, documentAt(t, upload, "required"))
	assert.Equal(t, "string", documentAt(t, upload, "properties", "file", "type"))
	assert.Equal(t, "binary", documentAt(t, upload, "properties", "file", "format"))
	assert.Equal(t, "#/components/schemas/api.ParsedPass", documentAt(t, parse, "responses", "200", "content", "application/json", "schema", "$ref"))
	assert.Equal(t, "#/components/schemas/api.ErrorResponse", documentAt(t, parse, "responses", "422", "content", "application/json", "schema", "$ref"))

	legs := documentAt(t, document, "components", "schemas", "bcbp.BoardingPass", "properties", "legs")
	assert.Equal(t, 1, documentAt(t, legs, "minItems"))
	assert.Equal(t, 4, documentAt(t, legs, "maxItems"))
	assert.Equal(t, "#/components/schemas/bcbp.Leg", documentAt(t, legs, "items", "$ref"))

	for _, path := range []string{"/boarding-passes", "/healthz", "/documentation", "/openapi.yaml"} {
		assert.NotEmpty(t, documentAt(t, document, "paths", path, "get", "operationId"))
	}
	list := documentAt(t, document, "paths", "/boarding-passes", "get")
	assert.Len(t, documentAt(t, list, "parameters"), 4)
	assert.Equal(t, "#/components/schemas/store.Page", documentAt(t, list, "responses", "200", "content", "application/json", "schema", "$ref"))

	jsonResponse := httptest.NewRecorder()
	handler.ServeHTTP(jsonResponse, httptest.NewRequest(http.MethodGet, "/documentation/swagger.json", nil))
	require.Equal(t, http.StatusOK, jsonResponse.Code)
	assert.Equal(t, "application/json", jsonResponse.Header().Get("Content-Type"))
	expectedJSON, err := json.Marshal(document)
	require.NoError(t, err)
	assert.JSONEq(t, string(expectedJSON), jsonResponse.Body.String(), "JSON and YAML must describe the same API")

	alias := httptest.NewRecorder()
	handler.ServeHTTP(alias, httptest.NewRequest(http.MethodGet, "/openapi.yaml", nil))
	require.Equal(t, http.StatusOK, alias.Code)
	assert.Equal(t, resp.Body.String(), alias.Body.String())
}

func documentAt(t *testing.T, node any, keys ...string) any {
	t.Helper()
	for _, key := range keys {
		mapping, ok := node.(map[string]any)
		require.True(t, ok, "expected an object containing %q, got %T", key, node)
		require.Contains(t, mapping, key)
		node = mapping[key]
	}
	return node
}
