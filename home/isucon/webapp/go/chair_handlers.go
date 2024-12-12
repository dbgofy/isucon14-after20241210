package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"
)

type chairPostChairsRequest struct {
	Name               string `json:"name"`
	Model              string `json:"model"`
	ChairRegisterToken string `json:"chair_register_token"`
}

type chairPostChairsResponse struct {
	ID      string `json:"id"`
	OwnerID string `json:"owner_id"`
}

func chairPostChairs(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	req := &chairPostChairsRequest{}
	if err := bindJSON(r, req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if req.Name == "" || req.Model == "" || req.ChairRegisterToken == "" {
		writeError(w, http.StatusBadRequest, errors.New("some of required fields(name, model, chair_register_token) are empty"))
		return
	}

	owner := &Owner{}
	if err := db.GetContext(ctx, owner, "SELECT * FROM owners WHERE chair_register_token = ?", req.ChairRegisterToken); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusUnauthorized, errors.New("invalid chair_register_token"))
			return
		}
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	chairID := ulid.Make().String()
	accessToken := secureRandomStr(32)

	now := time.Now()
	_, err := db.ExecContext(
		ctx,
		"INSERT INTO chairs (id, owner_id, name, model, is_active, access_token, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
		chairID, owner.ID, req.Name, req.Model, false, accessToken, now, now,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	UpdateChair(&Chair{
		ID:          chairID,
		OwnerID:     owner.ID,
		Name:        req.Name,
		Model:       req.Model,
		IsActive:    false,
		AccessToken: accessToken,
		CreatedAt:   now,
		UpdatedAt:   now,
	}, &now)

	http.SetCookie(w, &http.Cookie{
		Path:  "/",
		Name:  "chair_session",
		Value: accessToken,
	})

	writeJSON(w, http.StatusCreated, &chairPostChairsResponse{
		ID:      chairID,
		OwnerID: owner.ID,
	})
}

type postChairActivityRequest struct {
	IsActive bool `json:"is_active"`
}

func chairPostActivity(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	chair := ctx.Value("chair").(*Chair)

	req := &postChairActivityRequest{}
	if err := bindJSON(r, req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	_, err := db.ExecContext(ctx, "UPDATE chairs SET is_active = ? WHERE id = ?", req.IsActive, chair.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	chair.IsActive = req.IsActive
	UpdateChair(chair, nil)
	matchingChannel <- chair.ID

	w.WriteHeader(http.StatusNoContent)
}

type chairPostCoordinateResponse struct {
	RecordedAt int64 `json:"recorded_at"`
}

func chairPostCoordinate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	req := &Coordinate{}
	if err := bindJSON(r, req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	chair := ctx.Value("chair").(*Chair)

	tx, err := db.Beginx()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	defer tx.Rollback()

	prevLocation := &ChairLocation{} // 一個前の座標
	if err := tx.GetContext(ctx, prevLocation, `SELECT * FROM chair_locations WHERE chair_id = ? ORDER BY created_at DESC LIMIT 1`, chair.ID); err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusInternalServerError, err)
			return
		} else {
			prevLocation = nil
		}
	}

	chairLocationID := ulid.Make().String()
	now := time.Now()
	cl := ChairLocation{
		ID:        chairLocationID,
		ChairID:   chair.ID,
		Latitude:  req.Latitude,
		Longitude: req.Longitude,
		CreatedAt: now,
	}
	InsertChairLocation(&cl)
	go func() {
		time.Sleep(90 * time.Second)
		db.ExecContext(
			ctx,
			`INSERT INTO chair_locations (id, chair_id, latitude, longitude, created_at) VALUES (?, ?, ?, ?, ?)`,
			chairLocationID, chair.ID, req.Latitude, req.Longitude, now,
		)
	}()
	/*

	 */

	location := &ChairLocation{
		ID:        chairLocationID,
		ChairID:   chair.ID,
		Latitude:  req.Latitude,
		Longitude: req.Longitude,
		CreatedAt: now,
	}

	if prevLocation != nil {
		_, err = tx.ExecContext(
			ctx,
			"INSERT INTO chair_locations_minus_distance (id, chair_id, distance) VALUES (?, ?, ?)",
			ulid.Make().String(), chair.ID, abs(prevLocation.Longitude-location.Longitude)+abs(prevLocation.Latitude-location.Latitude),
		)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
	}

	ride := &Ride{}
	if err := tx.GetContext(ctx, ride, `SELECT * FROM rides WHERE chair_id = ? ORDER BY updated_at DESC LIMIT 1`, chair.ID); err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
	} else {
		status, err := getLatestRideStatus(ctx, tx, ride.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		if status != "COMPLETED" && status != "CANCELED" {
			if req.Latitude == ride.PickupLatitude && req.Longitude == ride.PickupLongitude && status == "ENROUTE" {
				if _, err := tx.ExecContext(ctx, "INSERT INTO ride_statuses (id, ride_id, status) VALUES (?, ?, ?)", ulid.Make().String(), ride.ID, "PICKUP"); err != nil {
					writeError(w, http.StatusInternalServerError, err)
					return
				}
				err = sendAppGetNotificationChannel(ctx, tx, "PICKUP", ride)
				if err != nil {
					writeError(w, http.StatusInternalServerError, err)
					slog.Error("failed to send notification", "error", err)
					return
				}
				err = sendChairGetNotificationChannel(ctx, "PICKUP", ride, nil)
				if err != nil {
					writeError(w, http.StatusInternalServerError, err)
					slog.Error("failed to send notification", "error", err)
					return
				}
			}

			if req.Latitude == ride.DestinationLatitude && req.Longitude == ride.DestinationLongitude && status == "CARRYING" {
				if _, err := tx.ExecContext(ctx, "INSERT INTO ride_statuses (id, ride_id, status) VALUES (?, ?, ?)", ulid.Make().String(), ride.ID, "ARRIVED"); err != nil {
					writeError(w, http.StatusInternalServerError, err)
					return
				}
				err = sendAppGetNotificationChannel(ctx, tx, "ARRIVED", ride)
				if err != nil {
					writeError(w, http.StatusInternalServerError, err)
					slog.Error("failed to send notification", "error", err)
					return
				}
				err = sendChairGetNotificationChannel(ctx, "ARRIVED", ride, nil)
				if err != nil {
					writeError(w, http.StatusInternalServerError, err)
					slog.Error("failed to send notification", "error", err)
					return
				}
			}
		}
	}

	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	writeJSON(w, http.StatusOK, &chairPostCoordinateResponse{
		RecordedAt: location.CreatedAt.UnixMilli(),
	})
}

type simpleUser struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type chairGetNotificationResponse struct {
	Data         *chairGetNotificationResponseData `json:"data"`
	RetryAfterMs int                               `json:"retry_after_ms"`
}

type chairGetNotificationResponseData struct {
	RideID                string     `json:"ride_id"`
	User                  simpleUser `json:"user"`
	PickupCoordinate      Coordinate `json:"pickup_coordinate"`
	DestinationCoordinate Coordinate `json:"destination_coordinate"`
	Status                string     `json:"status"`
}

// chairGetNotificationChannel map[chairID]chan chairGetNotificationResponseData
var chairGetNotificationChannel = sync.Map{}

func sendChairGetNotificationChannel(ctx context.Context, status string, ride *Ride, user *User) error {
	if !ride.ChairID.Valid {
		return nil
	}
	c, ok := chairGetNotificationChannel.Load(ride.ChairID.String)
	if !ok {
		return nil
	}
	channel, ok := c.(chan chairGetNotificationResponseData)
	if !ok {
		return nil
	}
	if user == nil {
		user = &User{}
		err := db.GetContext(ctx, user, "SELECT * FROM users WHERE id = ?", ride.UserID)
		if err != nil {
			return fmt.Errorf("failed to get user: %w", err)
		}
	}
	response := chairGetNotificationResponseData{
		RideID: ride.ID,
		User: simpleUser{
			ID:   user.ID,
			Name: fmt.Sprintf("%s %s", user.Firstname, user.Lastname),
		},
		PickupCoordinate: Coordinate{
			Latitude:  ride.PickupLatitude,
			Longitude: ride.PickupLongitude,
		},
		DestinationCoordinate: Coordinate{
			Latitude:  ride.DestinationLatitude,
			Longitude: ride.DestinationLongitude,
		},
		Status: status,
	}
	channel <- response
	return nil
}

func chairGetNotification(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	chair := ctx.Value("chair").(*Chair)

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}

	c := make(chan chairGetNotificationResponseData, 100)
	chairGetNotificationChannel.Store(chair.ID, c)
	ride := &Ride{}
	if err := db.GetContext(ctx, ride, `SELECT * FROM rides WHERE chair_id = ? ORDER BY created_at DESC LIMIT 1`, chair.ID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			slog.Info("no rides", "chair_id", chair.ID)
			ride = nil
		} else {
			writeError(w, http.StatusInternalServerError, err)
			slog.Error("failed to get rides", "error", err, "chair_id", chair.ID)
			return
		}
	}
	if ride != nil {
		status := ""
		yetSentRideStatus := RideStatus{}
		if err := db.GetContext(ctx, &yetSentRideStatus, `SELECT * FROM ride_statuses WHERE ride_id = ? AND chair_sent_at IS NULL ORDER BY created_at ASC LIMIT 1`, ride.ID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				status, err = getLatestRideStatus(ctx, db, ride.ID)
				if err != nil {
					writeError(w, http.StatusInternalServerError, err)
					slog.Info("failed to get latest ride status", "ride_id", ride.ID, "error", err)
					return
				}
			} else {
				writeError(w, http.StatusInternalServerError, err)
				slog.Error("failed to get rides", "error", err, "ride_id", ride.ID)
				return
			}
		} else {
			status = yetSentRideStatus.Status
		}

		go func() {
			err := sendChairGetNotificationChannel(ctx, status, ride, nil)
			if err != nil {
				writeError(w, http.StatusInternalServerError, err)
				slog.Error("failed to send notification", "error", err)
				return
			}
		}()
	}

	for {
		select {
		case response := <-c:
			w.Write([]byte("data: "))
			if err := json.NewEncoder(w).Encode(response); err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				slog.Error("failed to write response to http writer", "error", err, "response", response)
				return
			}
			w.Write([]byte("\n"))
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			_, err := db.ExecContext(ctx, `UPDATE ride_statuses SET chair_sent_at = CURRENT_TIMESTAMP(6) WHERE ride_id = ? AND status = ?`, response.RideID, response.Status)
			if err != nil {
				writeError(w, http.StatusInternalServerError, err)
				slog.Error("failed to update ride_status.app_sent_at", "error", err, "ride_id", response.RideID)
				return
			}
		case <-ctx.Done():
			return
		}
	}
}

type postChairRidesRideIDStatusRequest struct {
	Status string `json:"status"`
}

func chairPostRideStatus(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	rideID := r.PathValue("ride_id")

	chair := ctx.Value("chair").(*Chair)

	req := &postChairRidesRideIDStatusRequest{}
	if err := bindJSON(r, req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	tx, err := db.Beginx()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	defer tx.Rollback()

	ride := &Ride{}
	if err := tx.GetContext(ctx, ride, "SELECT * FROM rides WHERE id = ? FOR UPDATE", rideID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, errors.New("ride not found"))
			return
		}
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	if ride.ChairID.String != chair.ID {
		writeError(w, http.StatusBadRequest, errors.New("not assigned to this ride"))
		return
	}

	switch req.Status {
	// Acknowledge the ride
	case "ENROUTE":
		if _, err := tx.ExecContext(ctx, "INSERT INTO ride_statuses (id, ride_id, status) VALUES (?, ?, ?)", ulid.Make().String(), ride.ID, "ENROUTE"); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		err = sendAppGetNotificationChannel(ctx, tx, "ENROUTE", ride)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			slog.Error("failed to send notification", "error", err)
			return
		}
		err = sendChairGetNotificationChannel(ctx, "ENROUTE", ride, nil)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			slog.Error("failed to send notification", "error", err)
			return
		}
	// After Picking up user
	case "CARRYING":
		status, err := getLatestRideStatus(ctx, tx, ride.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		if status != "PICKUP" {
			writeError(w, http.StatusBadRequest, errors.New("chair has not arrived yet"))
			return
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO ride_statuses (id, ride_id, status) VALUES (?, ?, ?)", ulid.Make().String(), ride.ID, "CARRYING"); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		err = sendAppGetNotificationChannel(ctx, tx, "CARRYING", ride)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			slog.Error("failed to send notification", "error", err)
			return
		}
		err = sendChairGetNotificationChannel(ctx, "CARRYING", ride, nil)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			slog.Error("failed to send notification", "error", err)
			return
		}
	default:
		writeError(w, http.StatusBadRequest, errors.New("invalid status"))
	}

	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
