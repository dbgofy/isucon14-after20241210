package main

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/oklog/ulid/v2"
)

const (
	initialFare     = 500
	farePerDistance = 100
)

type ownerPostOwnersRequest struct {
	Name string `json:"name"`
}

type ownerPostOwnersResponse struct {
	ID                 string `json:"id"`
	ChairRegisterToken string `json:"chair_register_token"`
}

func ownerPostOwners(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	req := &ownerPostOwnersRequest{}
	if err := bindJSON(r, req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, errors.New("some of required fields(name) are empty"))
		return
	}

	ownerID := ulid.Make().String()
	accessToken := secureRandomStr(32)
	chairRegisterToken := secureRandomStr(32)

	_, err := db.ExecContext(
		ctx,
		"INSERT INTO owners (id, name, access_token, chair_register_token) VALUES (?, ?, ?, ?)",
		ownerID, req.Name, accessToken, chairRegisterToken,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	http.SetCookie(w, &http.Cookie{
		Path:  "/",
		Name:  "owner_session",
		Value: accessToken,
	})

	writeJSON(w, http.StatusCreated, &ownerPostOwnersResponse{
		ID:                 ownerID,
		ChairRegisterToken: chairRegisterToken,
	})
}

type chairSales struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Sales int    `json:"sales"`
}

type modelSales struct {
	Model string `json:"model"`
	Sales int    `json:"sales"`
}

type ownerGetSalesResponse struct {
	TotalSales int          `json:"total_sales"`
	Chairs     []chairSales `json:"chairs"`
	Models     []modelSales `json:"models"`
}

func ownerGetSales(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	since := time.Unix(0, 0)
	until := time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC)
	if r.URL.Query().Get("since") != "" {
		parsed, err := strconv.ParseInt(r.URL.Query().Get("since"), 10, 64)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		since = time.UnixMilli(parsed)
	}
	if r.URL.Query().Get("until") != "" {
		parsed, err := strconv.ParseInt(r.URL.Query().Get("until"), 10, 64)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		until = time.UnixMilli(parsed)
	}

	owner := r.Context().Value("owner").(*Owner)

	tx, err := db.Beginx()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	defer tx.Rollback()

	chairs := []Chair{}
	if err := tx.SelectContext(ctx, &chairs, "SELECT * FROM chairs WHERE owner_id = ?", owner.ID); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	chairMap := make(map[string]Chair, len(chairs))
	for _, chair := range chairs {
		chairMap[chair.ID] = chair
	}

	res := ownerGetSalesResponse{
		TotalSales: 0,
	}

	rides := []Ride{}
	if err = tx.SelectContext(ctx, &rides, "SELECT rides.* FROM rides INNER JOIN chairs ON rides.chair_id = chairs.id WHERE chairs.owner_id = ? AND evaluation IS NOT NULL AND rides.updated_at BETWEEN ? AND ? + INTERVAL 999 MICROSECOND", owner.ID, since, until); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	chairModels := []ChairModel{}
	if err = tx.SelectContext(ctx, &chairModels, "SELECT * FROM chair_models"); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	modelSalesByModel := map[string]int{}
	for _, model := range chairModels {
		modelSalesByModel[model.Name] = 0
	}
	modelSalesByChair := map[string]int{}
	for _, ride := range rides {
		chair := chairMap[ride.ChairID.String]
		sales := calculateSale(ride)
		res.TotalSales += sales
		modelSalesByModel[chair.Model] += sales
		modelSalesByChair[chair.ID] += sales
	}
	for _, chair := range chairs {
		res.Chairs = append(res.Chairs, chairSales{
			ID:    chair.ID,
			Name:  chair.Name,
			Sales: modelSalesByChair[chair.ID],
		})
	}

	models := []modelSales{}
	for model, sales := range modelSalesByModel {
		models = append(models, modelSales{
			Model: model,
			Sales: sales,
		})
	}
	res.Models = models

	writeJSON(w, http.StatusOK, res)
}

func sumSales(rides []Ride) int {
	sale := 0
	for _, ride := range rides {
		sale += calculateSale(ride)
	}
	return sale
}

func calculateSale(ride Ride) int {
	return calculateFare(ride.PickupLatitude, ride.PickupLongitude, ride.DestinationLatitude, ride.DestinationLongitude)
}

type chairWithDetail struct {
	ID                     string       `db:"id"`
	OwnerID                string       `db:"owner_id"`
	Name                   string       `db:"name"`
	AccessToken            string       `db:"access_token"`
	Model                  string       `db:"model"`
	IsActive               bool         `db:"is_active"`
	CreatedAt              time.Time    `db:"created_at"`
	UpdatedAt              time.Time    `db:"updated_at"`
	TotalDistance          int          `db:"total_distance"`
	TotalDistanceUpdatedAt sql.NullTime `db:"total_distance_updated_at"`
}

type ownerGetChairResponse struct {
	Chairs []ownerGetChairResponseChair `json:"chairs"`
}

type ownerGetChairResponseChair struct {
	ID                     string `json:"id"`
	Name                   string `json:"name"`
	Model                  string `json:"model"`
	Active                 bool   `json:"active"`
	RegisteredAt           int64  `json:"registered_at"`
	TotalDistance          int    `json:"total_distance"`
	TotalDistanceUpdatedAt *int64 `json:"total_distance_updated_at,omitempty"`
}

func ownerGetChairs(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	owner := ctx.Value("owner").(*Owner)

	chairs := []chairWithDetail{}
	if err := db.SelectContext(ctx, &chairs, `SELECT id,
       owner_id,
       name,
       access_token,
       model,
       is_active,
       created_at,
       updated_at
FROM chairs
WHERE owner_id = ?
`, owner.ID); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	chairIDs := make([]string, 0, len(chairs))
	for _, chair := range chairs {
		chairIDs = append(chairIDs, chair.ID)
	}

	totalDistanceByChairID := make(map[string]int)
	for _, c := range chairs {
		locations := ListChairLocations(c.ID)
		var (
			prev     *ChairLocation
			distance int
		)
		for _, l := range locations {
			if prev == nil {
				prev = l
				continue
			}

			lat := abs(l.Latitude - prev.Latitude)

			lon := abs(l.Longitude - prev.Longitude)

			distance += lat + lon
			prev = l
		}
		totalDistanceByChairID[c.ID] = distance
	}

	res := ownerGetChairResponse{}
	for _, chair := range chairs {
		totalDistance := totalDistanceByChairID[chair.ID]
		chairLocation := GetChairLocation(chair.ID)
		c := ownerGetChairResponseChair{
			ID:            chair.ID,
			Name:          chair.Name,
			Model:         chair.Model,
			Active:        chair.IsActive,
			RegisteredAt:  chair.CreatedAt.UnixMilli(),
			TotalDistance: totalDistance,
		}
		if totalDistance > 0 {
			t := chairLocation.CreatedAt.UnixMilli()
			c.TotalDistanceUpdatedAt = &t
		}
		res.Chairs = append(res.Chairs, c)
	}
	writeJSON(w, http.StatusOK, res)
}
