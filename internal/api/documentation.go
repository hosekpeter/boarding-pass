package api

import (
	"net/http"

	"boarding-pass/docs"
)

// @Summary Process liveness (does not check external services)
// @ID health
// @Tags System
// @Produce json
// @Success 200 {object} HealthResponse "Server is running"
// @Router /healthz [get]
func health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, HealthResponse{Status: "ok"})
}

// @Summary Download the generated OpenAPI specification
// @ID openAPI
// @Tags Documentation
// @Produce application/yaml
// @Success 200 {string} string "OpenAPI YAML"
// @Router /openapi.yaml [get]
func openAPI(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/yaml")
	_, _ = w.Write(docs.OpenAPIYAML)
}

// @Summary Download the generated OpenAPI specification as JSON
// @ID specJSON
// @Tags Documentation
// @Produce json
// @Success 200 {object} object "OpenAPI JSON"
// @Router /documentation/swagger.json [get]
func specJSON(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(docs.OpenAPIJSON)
}

// @Summary Download the generated OpenAPI specification as YAML
// @ID specYAML
// @Tags Documentation
// @Produce application/yaml
// @Success 200 {string} string "OpenAPI YAML"
// @Router /documentation/swagger.yaml [get]
func specYAML(w http.ResponseWriter, r *http.Request) {
	openAPI(w, r)
}

// @Summary Interactive API documentation
// @ID documentation
// @Tags Documentation
// @Produce html
// @Success 200 {string} string "Swagger UI"
// @Router /documentation [get]
func documentation(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(docs.Index)
}
