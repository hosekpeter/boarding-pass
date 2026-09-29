package api

import "boarding-pass/internal/bcbp"

type ParsedPass struct {
	DecodedBCBP bcbp.BoardingPass `json:"decoded_bcbp"`
}

type ErrorResponse struct {
	Error APIError `json:"error"`
}

type APIError struct {
	Code    string `json:"code" example:"barcode_not_found"`
	Message string `json:"message" example:"No readable PDF417 barcode found"`
}

type HealthResponse struct {
	Status string `json:"status" enums:"ok" example:"ok"`
}
