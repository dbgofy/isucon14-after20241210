package main

import (
	crand "crypto/rand"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-sql-driver/mysql"
	"github.com/jmoiron/sqlx"
)

const RetryAfterMs = 1500

var db *sqlx.DB

var ChairMap = sync.Map{}
var ChairLocationMap = sync.Map{}

func UpdateChair(chair *Chair, updatedAt *time.Time) {
	if updatedAt != nil {
		chair.UpdatedAt = *updatedAt
	} else {
		chair.UpdatedAt = time.Now()
	}
	ChairMap.Store(chair.ID, chair)
	ChairMap.Store(chair.AccessToken, chair)
}

func InsertChairLocation(cl *ChairLocation) {
	ChairLocationMap.Store(cl.ID, cl)
	ChairLocationMap.Store(cl.ChairID, cl)
}

// GetChair
// AccessTokenかIDをキーにしてChairを取得する
func GetChair(key string) *Chair {
	if v, ok := ChairMap.Load(key); ok {
		return v.(*Chair)
	}
	return nil
}

// GetChairLocation
// ID か ChairID をキーにして ChairLocation を取得する
func GetChairLocation(key string) *ChairLocation {
	if v, ok := ChairLocationMap.Load(key); ok {
		return v.(*ChairLocation)
	}
	return nil
}

// ListChairLocations
// ChairID をキーにして ChairLocation list を取得する
func ListChairLocations(key string) (cls []*ChairLocation) {
	ChairLocationMap.Range(func(k, v any) bool {
		if key == k.(string) {
			return true
		}

		cl := v.(*ChairLocation)
		if cl.ChairID == key {
			cls = append(cls, cl)
		}
		return true
	})
	sort.Slice(cls, func(i, j int) bool {
		return cls[i].ID < cls[j].ID
	})
	return
}

func main() {
	mux := setup()
	slog.Info("Listening on :8080")
	http.ListenAndServe(":8080", mux)
}

func setup() http.Handler {
	host := os.Getenv("ISUCON_DB_HOST")
	if host == "" {
		host = "127.0.0.1"
	}
	port := os.Getenv("ISUCON_DB_PORT")
	if port == "" {
		port = "3306"
	}
	_, err := strconv.Atoi(port)
	if err != nil {
		panic(fmt.Sprintf("failed to convert DB port number from ISUCON_DB_PORT environment variable into int: %v", err))
	}
	user := os.Getenv("ISUCON_DB_USER")
	if user == "" {
		user = "isucon"
	}
	password := os.Getenv("ISUCON_DB_PASSWORD")
	if password == "" {
		password = "isucon"
	}
	dbname := os.Getenv("ISUCON_DB_NAME")
	if dbname == "" {
		dbname = "isuride"
	}

	dbConfig := mysql.NewConfig()
	dbConfig.User = user
	dbConfig.Passwd = password
	dbConfig.Addr = net.JoinHostPort(host, port)
	dbConfig.Net = "tcp"
	dbConfig.DBName = dbname
	dbConfig.ParseTime = true

	_db, err := sqlx.Connect("mysql", dbConfig.FormatDSN())
	if err != nil {
		panic(err)
	}
	db = _db

	{
		// chairの情報を起動時にメモリに持っておく
		ChairMap = sync.Map{}
		chairs := []Chair{}
		if err := db.Select(&chairs, "SELECT * FROM chairs"); err != nil {
			panic(err)
		}
		for _, chair := range chairs {
			UpdateChair(&chair, &chair.UpdatedAt)
		}
	}

	mux := chi.NewRouter()
	mux.Use(middleware.Logger)
	mux.Use(middleware.Recoverer)

	mux.HandleFunc("POST /api/initialize", postInitialize)

	// app handlers
	{
		mux.HandleFunc("POST /api/app/users", appPostUsers)

		authedMux := mux.With(appAuthMiddleware)
		authedMux.HandleFunc("POST /api/app/payment-methods", appPostPaymentMethods)
		authedMux.HandleFunc("GET /api/app/rides", appGetRides)
		authedMux.HandleFunc("POST /api/app/rides", appPostRides)
		authedMux.HandleFunc("POST /api/app/rides/estimated-fare", appPostRidesEstimatedFare)
		authedMux.HandleFunc("POST /api/app/rides/{ride_id}/evaluation", appPostRideEvaluatation)
		authedMux.HandleFunc("GET /api/app/notification", appGetNotification)
		authedMux.HandleFunc("GET /api/app/nearby-chairs", appGetNearbyChairs)
	}

	// owner handlers
	{
		mux.HandleFunc("POST /api/owner/owners", ownerPostOwners)

		authedMux := mux.With(ownerAuthMiddleware)
		authedMux.HandleFunc("GET /api/owner/sales", ownerGetSales)
		authedMux.HandleFunc("GET /api/owner/chairs", ownerGetChairs)
	}

	// chair handlers
	{
		mux.HandleFunc("POST /api/chair/chairs", chairPostChairs)

		authedMux := mux.With(chairAuthMiddleware)
		authedMux.HandleFunc("POST /api/chair/activity", chairPostActivity)
		authedMux.HandleFunc("POST /api/chair/coordinate", chairPostCoordinate)
		authedMux.HandleFunc("GET /api/chair/notification", chairGetNotification)
		authedMux.HandleFunc("POST /api/chair/rides/{ride_id}/status", chairPostRideStatus)
	}

	// internal handlers
	{
		mux.HandleFunc("GET /api/internal/matching", internalGetMatching)
	}

	// pprof
	mux.Mount("/debug", middleware.Profiler())

	return mux
}

type postInitializeRequest struct {
	PaymentServer string `json:"payment_server"`
}

type postInitializeResponse struct {
	Language string `json:"language"`
}

func postInitialize(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	req := &postInitializeRequest{}
	if err := bindJSON(r, req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	if out, err := exec.Command("../sql/init.sh").CombinedOutput(); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("failed to initialize: %s: %w", string(out), err))
		return
	}

	if _, err := db.ExecContext(ctx, "UPDATE settings SET value = ? WHERE name = 'payment_gateway_url'", req.PaymentServer); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	chairLocations := []ChairLocation{}
	if err := db.SelectContext(ctx, &chairLocations, "SELECT * FROM chair_locations ORDER BY created_at"); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	prevChairLocations := make(map[string]ChairLocation)
	distanceByChairID := make(map[string]int)
	for _, cl := range chairLocations {
		prevChairLocation, ok := prevChairLocations[cl.ChairID]
		prevChairLocations[cl.ChairID] = cl
		if !ok {
			continue
		}
		distanceByChairID[cl.ChairID] += abs(cl.Latitude-prevChairLocation.Latitude) + abs(cl.Longitude-prevChairLocation.Longitude)
	}
	chairTotalDistances := make([]ChairTotalDistance, 0, len(distanceByChairID))
	for chairID, distance := range distanceByChairID {
		chairTotalDistances = append(chairTotalDistances, ChairTotalDistance{
			ID:       ulid.Make().String(),
			ChairID:  chairID,
			Distance: distance,
		})
	}
	_, err := db.NamedExecContext(
		ctx,
		"INSERT INTO chair_locations_minus_distance (id, chair_id, distance) VALUES (:id, :chair_id, :distance)",
		chairTotalDistances,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	ChairMap = sync.Map{}
	chairs := []Chair{}
	if err := db.SelectContext(ctx, &chairs, "SELECT * FROM chairs"); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	for _, chair := range chairs {
		UpdateChair(&chair, &chair.UpdatedAt)
	}

	writeJSON(w, http.StatusOK, postInitializeResponse{Language: "go"})
}

type Coordinate struct {
	Latitude  int `json:"latitude"`
	Longitude int `json:"longitude"`
}

func bindJSON(r *http.Request, v interface{}) error {
	return json.NewDecoder(r.Body).Decode(v)
}

func writeJSON(w http.ResponseWriter, statusCode int, v interface{}) {
	w.Header().Set("Content-Type", "application/json;charset=utf-8")
	buf, err := json.Marshal(v)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.WriteHeader(statusCode)
	w.Write(buf)
}

func writeError(w http.ResponseWriter, statusCode int, err error) {
	w.Header().Set("Content-Type", "application/json;charset=utf-8")
	w.WriteHeader(statusCode)
	buf, marshalError := json.Marshal(map[string]string{"message": err.Error()})
	if marshalError != nil {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":"marshaling error failed"}`))
		return
	}
	w.Write(buf)

	slog.Error("error response wrote", err)
}

func secureRandomStr(b int) string {
	k := make([]byte, b)
	if _, err := crand.Read(k); err != nil {
		panic(err)
	}
	return fmt.Sprintf("%x", k)
}
