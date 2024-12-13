package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/oklog/ulid/v2"
	"net/http"
)

var erroredUpstream = errors.New("errored upstream")

type paymentGatewayPostPaymentRequest struct {
	Amount int `json:"amount"`
}

type paymentGatewayGetPaymentsResponseOne struct {
	Amount int    `json:"amount"`
	Status string `json:"status"`
}

func requestPaymentGatewayPostPayment(ctx context.Context, paymentGatewayURL string, token string, param *paymentGatewayPostPaymentRequest) error {
	b, err := json.Marshal(param)
	if err != nil {
		return fmt.Errorf("failed to marshal request body: %w", err)
	}

	idempotencyKey := ulid.Make().String()
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, paymentGatewayURL+"/payments", bytes.NewBuffer(b))
		if err != nil {
			return fmt.Errorf("failed to create request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Idempotency-Key", idempotencyKey)

		res, err := http.DefaultClient.Do(req)
		if err != nil {
			return fmt.Errorf("failed to request payment gateway: %w", err)
		}
		if err = res.Body.Close(); err != nil {
			return fmt.Errorf("failed to close response body: %w", err)
		}

		if res.StatusCode == http.StatusNoContent {
			break
		}
	}

	return nil
}
